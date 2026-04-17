package services

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/queries"
	"github.com/josephalai/sentanyl/pkg/db"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// PatchService handles structured patch creation, validation, and application.
type PatchService struct{}

// NewPatchService creates a PatchService.
func NewPatchService() *PatchService {
	return &PatchService{}
}

// CreateEditPatch generates a structured patch from an edit prompt. In
// production this will be produced by an LLM; until then, we emit
// deterministic scope-aware operations so the pipeline can be exercised
// end-to-end.
func (s *PatchService) CreateEditPatch(
	tenantID bson.ObjectId,
	productPublicId string,
	prompt string,
	targetType string,
	targetId string,
	scope string,
) (*pkgmodels.ContentPatch, error) {
	if targetType == "" {
		targetType = "course"
	}
	if targetId == "" {
		targetId = productPublicId
	}

	product, err := queries.GetCourseProductByPublicId(tenantID, productPublicId)
	if err != nil {
		return nil, fmt.Errorf("course not found: %w", err)
	}

	ops, err := buildEditOperations(product, prompt, targetType, targetId, scope)
	if err != nil {
		return nil, err
	}

	patch := pkgmodels.NewContentPatch(tenantID, "", productPublicId, targetType, targetId)
	patch.Prompt = prompt
	patch.Operations = ops

	created, err := queries.CreateContentPatch(patch)
	if err != nil {
		return nil, fmt.Errorf("failed to create patch: %w", err)
	}
	return created, nil
}

// ApplyPatch applies a patch to the course tree and records a revision event.
func (s *PatchService) ApplyPatch(
	tenantID bson.ObjectId,
	patchPublicId string,
) (*pkgmodels.ContentPatch, error) {
	patch, err := queries.GetContentPatchByPublicId(tenantID, patchPublicId)
	if err != nil {
		return nil, fmt.Errorf("patch not found: %w", err)
	}

	if patch.Status != "pending" {
		return nil, fmt.Errorf("patch is not in pending state (current: %s)", patch.Status)
	}

	product, err := queries.GetCourseProductByPublicId(tenantID, patch.ProductPublicId)
	if err != nil {
		return nil, fmt.Errorf("course not found: %w", err)
	}

	snapshotBefore := product.CourseModules

	for _, op := range patch.Operations {
		if err := applyOperation(product, op); err != nil {
			return nil, fmt.Errorf("failed to apply operation %s %s: %w", op.Op, op.Path, err)
		}
	}

	product.TotalLessons = recalculateTotalLessons(product)

	updateDoc := bson.M{
		"name":                  product.Name,
		"description":           product.Description,
		"instructor_name":       product.InstructorName,
		"course_modules":        product.CourseModules,
		"total_lessons":         product.TotalLessons,
		"timestamps.updated_at": time.Now(),
	}
	err = db.GetCollection(pkgmodels.ProductCollection).Update(
		bson.M{"_id": product.Id},
		bson.M{"$set": updateDoc},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to apply patch to course: %w", err)
	}

	revEvent := pkgmodels.NewCourseRevisionEvent(tenantID, patch.ProductPublicId, "patch_apply",
		fmt.Sprintf("Applied patch %s (%s/%s)", patchPublicId, patch.TargetType, patch.TargetId))
	revEvent.PatchPublicId = patchPublicId
	revEvent.SnapshotBefore = snapshotBefore
	revEvent.SnapshotAfter = product.CourseModules
	queries.CreateCourseRevisionEvent(revEvent)

	now := time.Now()
	updated, err := queries.UpdateContentPatch(tenantID, patchPublicId, bson.M{
		"status":     "applied",
		"applied_at": now,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update patch status: %w", err)
	}

	return updated, nil
}

// applyOperation applies a single patch operation to the product in memory.
// Exported via tests in the same package.
func applyOperation(product *pkgmodels.Product, op pkgmodels.PatchOperation) error {
	switch op.Op {
	case "replace":
		return applyReplace(product, op)
	case "add":
		return applyAdd(product, op)
	case "remove":
		return applyRemove(product, op)
	case "reorder":
		return applyReorder(product, op)
	default:
		return fmt.Errorf("unsupported op %q", op.Op)
	}
}

func applyReplace(product *pkgmodels.Product, op pkgmodels.PatchOperation) error {
	switch op.Path {
	case "/title", "/name":
		if v, ok := op.Value.(string); ok {
			product.Name = v
			return nil
		}
	case "/description":
		if v, ok := op.Value.(string); ok {
			product.Description = v
			return nil
		}
	case "/instructor_name":
		if v, ok := op.Value.(string); ok {
			product.InstructorName = v
			return nil
		}
	}

	// /module/<slug>/... (title or lesson sub-path)
	if modSlug, rest, ok := parseModulePath(op.Path); ok {
		// Lesson sub-path: try named module first, then scan all modules for
		// the lesson slug (defensive fallback for clients that pass a bare
		// lesson slug or a stale module slug).
		if lessonSlug, field, isLesson := parseLessonSubPath(rest); isLesson {
			var lesson *pkgmodels.CourseLesson
			if mod := findModule(product, modSlug); mod != nil {
				lesson = findLesson(mod, lessonSlug)
			}
			if lesson == nil {
				if found, _ := findLessonAnyModule(product, lessonSlug); found != nil {
					lesson = found
				}
			}
			if lesson == nil {
				return fmt.Errorf("lesson %q not found", lessonSlug)
			}
			return applyLessonFieldReplace(lesson, field, op.Value)
		}

		mod := findModule(product, modSlug)
		if mod == nil {
			return fmt.Errorf("module %q not found", modSlug)
		}
		if rest == "/title" {
			if v, ok := op.Value.(string); ok {
				mod.Title = v
				return nil
			}
			return fmt.Errorf("invalid value type for module title")
		}
	}

	// Defensive: bare lesson-scoped path like `/content_markdown` with the
	// patch's target_id being a lesson slug. Locate the lesson anywhere.
	if strings.HasPrefix(op.Path, "/") && !strings.HasPrefix(op.Path, "/module/") && !strings.HasPrefix(op.Path, "/modules") {
		return fmt.Errorf("unsupported replace path %q", op.Path)
	}

	return fmt.Errorf("unsupported replace path %q", op.Path)
}

func applyLessonFieldReplace(lesson *pkgmodels.CourseLesson, field string, value interface{}) error {
	switch field {
	case "title":
		if v, ok := value.(string); ok {
			lesson.Title = v
			return nil
		}
	case "content_markdown":
		if v, ok := value.(string); ok {
			lesson.ContentMarkdown = v
			return nil
		}
	case "content_html":
		if v, ok := value.(string); ok {
			lesson.ContentHTML = v
			return nil
		}
	case "video_mode":
		if v, ok := value.(string); ok {
			lesson.VideoMode = pkgmodels.VideoMode(v)
			return nil
		}
	case "video_stub_script":
		if v, ok := value.(string); ok {
			lesson.VideoStubScript = v
			return nil
		}
	case "video_stub_description":
		if v, ok := value.(string); ok {
			lesson.VideoStubDescription = v
			return nil
		}
	case "video_upload_pending":
		if v, ok := value.(bool); ok {
			lesson.VideoUploadPending = v
			return nil
		}
	case "video_url":
		if v, ok := value.(string); ok {
			lesson.VideoURL = v
			return nil
		}
	case "media_public_id":
		if v, ok := value.(string); ok {
			lesson.MediaPublicId = v
			return nil
		}
	case "duration":
		if v, ok := value.(string); ok {
			lesson.Duration = v
			return nil
		}
	case "is_free":
		if v, ok := value.(bool); ok {
			lesson.IsFree = v
			return nil
		}
	case "is_draft":
		if v, ok := value.(bool); ok {
			lesson.IsDraft = v
			return nil
		}
	case "drip_days":
		if v, ok := asInt(value); ok {
			lesson.DripDays = v
			return nil
		}
	}
	return fmt.Errorf("unsupported lesson field %q or invalid value type", field)
}

func applyAdd(product *pkgmodels.Product, op pkgmodels.PatchOperation) error {
	switch op.Path {
	case "/modules":
		mod := &pkgmodels.CourseModule{}
		if err := unmarshalPatchValue(op.Value, mod); err != nil {
			return fmt.Errorf("invalid module value: %w", err)
		}
		if mod.Slug == "" {
			return fmt.Errorf("added module requires a slug")
		}
		if mod.Order == 0 {
			mod.Order = len(product.CourseModules) + 1
		}
		product.CourseModules = append(product.CourseModules, mod)
		return nil
	}
	// /module/<slug>/lessons
	if modSlug, rest, ok := parseModulePath(op.Path); ok && rest == "/lessons" {
		mod := findModule(product, modSlug)
		if mod == nil {
			return fmt.Errorf("module %q not found", modSlug)
		}
		lesson := &pkgmodels.CourseLesson{}
		if err := unmarshalPatchValue(op.Value, lesson); err != nil {
			return fmt.Errorf("invalid lesson value: %w", err)
		}
		if lesson.Slug == "" {
			return fmt.Errorf("added lesson requires a slug")
		}
		if lesson.Order == 0 {
			lesson.Order = len(mod.Lessons) + 1
		}
		mod.Lessons = append(mod.Lessons, lesson)
		return nil
	}
	return fmt.Errorf("unsupported add path %q", op.Path)
}

func applyRemove(product *pkgmodels.Product, op pkgmodels.PatchOperation) error {
	// /module/<slug>
	if modSlug, rest, ok := parseModulePath(op.Path); ok {
		if rest == "" {
			next := product.CourseModules[:0]
			for _, m := range product.CourseModules {
				if m.Slug != modSlug {
					next = append(next, m)
				}
			}
			product.CourseModules = next
			return nil
		}
		// /module/<slug>/lesson/<slug>
		if strings.HasPrefix(rest, "/lesson/") {
			lessonSlug := strings.TrimPrefix(rest, "/lesson/")
			if lessonSlug == "" || strings.Contains(lessonSlug, "/") {
				return fmt.Errorf("invalid remove path %q", op.Path)
			}
			mod := findModule(product, modSlug)
			if mod == nil {
				return fmt.Errorf("module %q not found", modSlug)
			}
			next := mod.Lessons[:0]
			for _, l := range mod.Lessons {
				if l.Slug != lessonSlug {
					next = append(next, l)
				}
			}
			mod.Lessons = next
			return nil
		}
	}
	return fmt.Errorf("unsupported remove path %q", op.Path)
}

func applyReorder(product *pkgmodels.Product, op pkgmodels.PatchOperation) error {
	orderedSlugs, err := toStringSlice(op.Value)
	if err != nil {
		return fmt.Errorf("invalid reorder value: %w", err)
	}

	if op.Path == "/modules" {
		idx := map[string]int{}
		for i, s := range orderedSlugs {
			idx[s] = i + 1
		}
		for _, m := range product.CourseModules {
			if o, ok := idx[m.Slug]; ok {
				m.Order = o
			}
		}
		sort.SliceStable(product.CourseModules, func(i, j int) bool {
			return product.CourseModules[i].Order < product.CourseModules[j].Order
		})
		return nil
	}

	if modSlug, rest, ok := parseModulePath(op.Path); ok && rest == "/lessons" {
		mod := findModule(product, modSlug)
		if mod == nil {
			return fmt.Errorf("module %q not found", modSlug)
		}
		idx := map[string]int{}
		for i, s := range orderedSlugs {
			idx[s] = i + 1
		}
		for _, l := range mod.Lessons {
			if o, ok := idx[l.Slug]; ok {
				l.Order = o
			}
		}
		sort.SliceStable(mod.Lessons, func(i, j int) bool {
			return mod.Lessons[i].Order < mod.Lessons[j].Order
		})
		return nil
	}

	return fmt.Errorf("unsupported reorder path %q", op.Path)
}

// ---------- Helpers ----------

func recalculateTotalLessons(product *pkgmodels.Product) int {
	total := 0
	for _, m := range product.CourseModules {
		total += len(m.Lessons)
	}
	return total
}

// splitLessonTarget normalizes a patch TargetId for lessons. Accepts
// "moduleSlug/lessonSlug" and returns (moduleSlug, lessonSlug). If the
// targetId is only a lesson slug, scans the product tree for the owning
// module. Returns empty strings if nothing is found.
func splitLessonTarget(product *pkgmodels.Product, targetId string) (string, string) {
	if targetId == "" {
		return "", ""
	}
	if i := strings.Index(targetId, "/"); i >= 0 {
		return targetId[:i], targetId[i+1:]
	}
	if product == nil {
		return "", targetId
	}
	_, modSlug := findLessonAnyModule(product, targetId)
	return modSlug, targetId
}

func findModule(product *pkgmodels.Product, slug string) *pkgmodels.CourseModule {
	if product == nil {
		return nil
	}
	for _, m := range product.CourseModules {
		if m.Slug == slug {
			return m
		}
	}
	return nil
}

func findLesson(mod *pkgmodels.CourseModule, slug string) *pkgmodels.CourseLesson {
	if mod == nil {
		return nil
	}
	for _, l := range mod.Lessons {
		if l.Slug == slug {
			return l
		}
	}
	return nil
}

func findLessonAnyModule(product *pkgmodels.Product, slug string) (*pkgmodels.CourseLesson, string) {
	if product == nil {
		return nil, ""
	}
	for _, m := range product.CourseModules {
		for _, l := range m.Lessons {
			if l.Slug == slug {
				return l, m.Slug
			}
		}
	}
	return nil, ""
}

// parseModulePath parses a path shaped "/module/<slug>[/rest]". Returns
// (slug, rest, ok). rest includes the leading slash or is "".
func parseModulePath(path string) (string, string, bool) {
	const prefix = "/module/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	tail := path[len(prefix):]
	if tail == "" {
		return "", "", false
	}
	slashIdx := strings.Index(tail, "/")
	if slashIdx < 0 {
		return tail, "", true
	}
	slug := tail[:slashIdx]
	if slug == "" {
		return "", "", false
	}
	return slug, tail[slashIdx:], true
}

// parseLessonSubPath parses a rest of form "/lesson/<slug>/<field>".
func parseLessonSubPath(rest string) (string, string, bool) {
	const prefix = "/lesson/"
	if !strings.HasPrefix(rest, prefix) {
		return "", "", false
	}
	tail := rest[len(prefix):]
	slashIdx := strings.Index(tail, "/")
	if slashIdx <= 0 {
		return "", "", false
	}
	slug := tail[:slashIdx]
	field := tail[slashIdx+1:]
	if field == "" {
		return "", "", false
	}
	return slug, field, true
}

// unmarshalPatchValue coerces an operation value (which may arrive as a
// Go struct, a map[string]interface{} from BSON, or a *bson.M) into the
// target struct pointer using a BSON round-trip.
func unmarshalPatchValue(value interface{}, out interface{}) error {
	if value == nil {
		return fmt.Errorf("nil value")
	}
	b, err := bson.Marshal(value)
	if err != nil {
		return err
	}
	return bson.Unmarshal(b, out)
}

// toStringSlice coerces value into a []string. Accepts []string,
// []interface{} of strings, or a single string (single-element result).
func toStringSlice(value interface{}) ([]string, error) {
	switch v := value.(type) {
	case []string:
		return v, nil
	case []interface{}:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("element %d is not a string", i)
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		return []string{v}, nil
	}
	return nil, fmt.Errorf("value is not a []string")
}

func asInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float32:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

// ---------- CreateEditPatch operations builder ----------

// buildEditOperations is the deterministic scope-aware operation builder
// used until a real LLM integration ships. It is kept pure so tests can
// exercise it without hitting the database.
func buildEditOperations(
	product *pkgmodels.Product,
	prompt string,
	targetType string,
	targetId string,
	scope string,
) ([]pkgmodels.PatchOperation, error) {
	lowerPrompt := strings.ToLower(prompt)

	switch scope {
	case "rewrite", "":
		switch targetType {
		case "course", "":
			return []pkgmodels.PatchOperation{{
				Op:    "replace",
				Path:  "/description",
				Value: rewriteFor(prompt, product.Description, "course description"),
			}}, nil
		case "module":
			modSlug := targetId
			return []pkgmodels.PatchOperation{{
				Op:    "replace",
				Path:  fmt.Sprintf("/module/%s/title", modSlug),
				Value: rewriteFor(prompt, lookupModuleTitle(product, modSlug), "module title"),
			}}, nil
		case "lesson":
			modSlug, lessonSlug := splitLessonTarget(product, targetId)
			if modSlug == "" || lessonSlug == "" {
				return nil, fmt.Errorf("could not resolve lesson %q for rewrite", targetId)
			}
			return []pkgmodels.PatchOperation{{
				Op:    "replace",
				Path:  fmt.Sprintf("/module/%s/lesson/%s/content_markdown", modSlug, lessonSlug),
				Value: rewriteFor(prompt, lookupLessonContent(product, modSlug, lessonSlug), "lesson content"),
			}}, nil
		}
	case "convert":
		if targetType != "lesson" {
			return nil, fmt.Errorf("convert scope only supports target_type=lesson (got %q)", targetType)
		}
		modSlug, lessonSlug := splitLessonTarget(product, targetId)
		if modSlug == "" || lessonSlug == "" {
			return nil, fmt.Errorf("could not resolve lesson %q for convert", targetId)
		}
		if strings.Contains(lowerPrompt, "stub") || strings.Contains(lowerPrompt, "video") {
			prefix := fmt.Sprintf("/module/%s/lesson/%s", modSlug, lessonSlug)
			return []pkgmodels.PatchOperation{
				{Op: "replace", Path: prefix + "/video_mode", Value: "stub"},
				{Op: "replace", Path: prefix + "/video_stub_script", Value: fmt.Sprintf("[stub script placeholder based on: %s]", prompt)},
				{Op: "replace", Path: prefix + "/video_stub_description", Value: fmt.Sprintf("[stub description placeholder based on: %s]", prompt)},
				{Op: "replace", Path: prefix + "/video_upload_pending", Value: true},
			}, nil
		}
		return nil, fmt.Errorf("convert scope not recognised for prompt %q (expected mention of stub or video)", prompt)
	case "add":
		// Default: add a module; if prompt clearly says "lesson", add a lesson under targetId.
		if strings.Contains(lowerPrompt, "lesson") && targetType == "module" {
			mod := findModule(product, targetId)
			if mod == nil {
				return nil, fmt.Errorf("module %q not found for add", targetId)
			}
			newSlug := generateUniqueLessonSlug(mod, "new-lesson")
			return []pkgmodels.PatchOperation{{
				Op:   "add",
				Path: fmt.Sprintf("/module/%s/lessons", targetId),
				Value: map[string]interface{}{
					"slug":             newSlug,
					"title":            "New Lesson",
					"order":            len(mod.Lessons) + 1,
					"content_markdown": fmt.Sprintf("Placeholder lesson generated from prompt: %s", prompt),
					"is_draft":         true,
				},
			}}, nil
		}
		newSlug := generateUniqueModuleSlug(product, "new-module")
		return []pkgmodels.PatchOperation{{
			Op:   "add",
			Path: "/modules",
			Value: map[string]interface{}{
				"slug":    newSlug,
				"title":   "New Module",
				"order":   len(product.CourseModules) + 1,
				"lessons": []interface{}{},
			},
		}}, nil
	case "remove":
		switch targetType {
		case "module":
			return []pkgmodels.PatchOperation{{
				Op:   "remove",
				Path: fmt.Sprintf("/module/%s", targetId),
			}}, nil
		case "lesson":
			modSlug, lessonSlug := splitLessonTarget(product, targetId)
			if modSlug == "" || lessonSlug == "" {
				return nil, fmt.Errorf("could not resolve lesson %q for remove", targetId)
			}
			return []pkgmodels.PatchOperation{{
				Op:   "remove",
				Path: fmt.Sprintf("/module/%s/lesson/%s", modSlug, lessonSlug),
			}}, nil
		}
	case "quiz", "certificate":
		return nil, fmt.Errorf("scope %q is not supported for AI edits; use the %s API directly", scope, scope)
	}

	return nil, fmt.Errorf("unsupported scope %q for target_type %q", scope, targetType)
}

func rewriteFor(prompt, existing, kind string) string {
	prefix := fmt.Sprintf("[%s rewritten based on: %s]", kind, prompt)
	if existing == "" {
		return prefix
	}
	return prefix + "\n\n" + existing
}

func lookupModuleTitle(product *pkgmodels.Product, slug string) string {
	if m := findModule(product, slug); m != nil {
		return m.Title
	}
	return ""
}

func lookupLessonContent(product *pkgmodels.Product, modSlug, lessonSlug string) string {
	m := findModule(product, modSlug)
	if m == nil {
		return ""
	}
	l := findLesson(m, lessonSlug)
	if l == nil {
		return ""
	}
	if l.ContentMarkdown != "" {
		return l.ContentMarkdown
	}
	return l.ContentHTML
}

func generateUniqueModuleSlug(product *pkgmodels.Product, base string) string {
	used := map[string]bool{}
	for _, m := range product.CourseModules {
		used[m.Slug] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		s := fmt.Sprintf("%s-%d", base, i)
		if !used[s] {
			return s
		}
	}
}

func generateUniqueLessonSlug(mod *pkgmodels.CourseModule, base string) string {
	used := map[string]bool{}
	for _, l := range mod.Lessons {
		used[l.Slug] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		s := fmt.Sprintf("%s-%d", base, i)
		if !used[s] {
			return s
		}
	}
}

func init() {
	log.Println("[LMS] Patch service initialized")
}
