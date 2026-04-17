package services

import (
	"strings"
	"testing"

	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// ---------- helpers ----------

func newTestProduct() *pkgmodels.Product {
	return &pkgmodels.Product{
		Name:           "Course",
		Description:    "original description",
		InstructorName: "Alice",
		CourseModules: []*pkgmodels.CourseModule{
			{
				Slug: "m1", Title: "Module 1", Order: 1,
				Lessons: []*pkgmodels.CourseLesson{
					{Slug: "l1", Title: "Lesson 1", Order: 1, ContentMarkdown: "original content"},
					{Slug: "l2", Title: "Lesson 2", Order: 2},
				},
			},
			{
				Slug: "m2", Title: "Module 2", Order: 2,
				Lessons: []*pkgmodels.CourseLesson{
					{Slug: "l3", Title: "Lesson 3", Order: 1},
				},
			},
		},
		TotalLessons: 3,
	}
}

// ---------- replace ----------

func TestApplyOperation_ReplaceCourseDescription(t *testing.T) {
	p := newTestProduct()
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op: "replace", Path: "/description", Value: "new description",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Description != "new description" {
		t.Errorf("Description = %q, want %q", p.Description, "new description")
	}
}

func TestApplyOperation_ReplaceInstructor(t *testing.T) {
	p := newTestProduct()
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op: "replace", Path: "/instructor_name", Value: "Bob",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.InstructorName != "Bob" {
		t.Errorf("InstructorName = %q, want %q", p.InstructorName, "Bob")
	}
}

func TestApplyOperation_ReplaceLessonContentMarkdown(t *testing.T) {
	p := newTestProduct()
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op:    "replace",
		Path:  "/module/m1/lesson/l1/content_markdown",
		Value: "rewritten content",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.CourseModules[0].Lessons[0].ContentMarkdown != "rewritten content" {
		t.Errorf("ContentMarkdown = %q, want %q",
			p.CourseModules[0].Lessons[0].ContentMarkdown, "rewritten content")
	}
}

func TestApplyOperation_ReplaceLessonVideoStubFields(t *testing.T) {
	p := newTestProduct()
	ops := []pkgmodels.PatchOperation{
		{Op: "replace", Path: "/module/m1/lesson/l1/video_mode", Value: "stub"},
		{Op: "replace", Path: "/module/m1/lesson/l1/video_stub_script", Value: "script"},
		{Op: "replace", Path: "/module/m1/lesson/l1/video_stub_description", Value: "desc"},
		{Op: "replace", Path: "/module/m1/lesson/l1/video_upload_pending", Value: true},
		{Op: "replace", Path: "/module/m1/lesson/l1/drip_days", Value: 5},
	}
	for _, op := range ops {
		if err := applyOperation(p, op); err != nil {
			t.Fatalf("op %s %s: %v", op.Op, op.Path, err)
		}
	}
	l := p.CourseModules[0].Lessons[0]
	if l.VideoMode != pkgmodels.VideoModeStub {
		t.Errorf("VideoMode = %q, want stub", l.VideoMode)
	}
	if l.VideoStubScript != "script" {
		t.Errorf("VideoStubScript = %q", l.VideoStubScript)
	}
	if l.VideoStubDescription != "desc" {
		t.Errorf("VideoStubDescription = %q", l.VideoStubDescription)
	}
	if !l.VideoUploadPending {
		t.Error("VideoUploadPending should be true")
	}
	if l.DripDays != 5 {
		t.Errorf("DripDays = %d, want 5", l.DripDays)
	}
}

func TestApplyOperation_ReplaceLesson_BareSlugFallback(t *testing.T) {
	p := newTestProduct()
	// Path references module "wrong-mod" but lesson l2 actually lives under m1.
	// The defensive fallback should locate l2 and apply anyway.
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op:    "replace",
		Path:  "/module/wrong-mod/lesson/l2/content_markdown",
		Value: "found-it",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.CourseModules[0].Lessons[1].ContentMarkdown != "found-it" {
		t.Errorf("expected l2 content_markdown to be set via fallback, got %q",
			p.CourseModules[0].Lessons[1].ContentMarkdown)
	}
}

// ---------- add ----------

func TestApplyOperation_AddModule_RecalculatesTotalLessons(t *testing.T) {
	p := newTestProduct()
	err := applyOperation(p, pkgmodels.PatchOperation{
		Op:   "add",
		Path: "/modules",
		Value: map[string]interface{}{
			"slug":  "m3",
			"title": "Module 3",
			"order": 3,
			"lessons": []interface{}{
				map[string]interface{}{"slug": "l4", "title": "Lesson 4", "order": 1},
				map[string]interface{}{"slug": "l5", "title": "Lesson 5", "order": 2},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := recalculateTotalLessons(p); got != 5 {
		t.Errorf("TotalLessons = %d, want 5", got)
	}
	if len(p.CourseModules) != 3 || p.CourseModules[2].Slug != "m3" {
		t.Errorf("expected m3 at index 2, got modules=%+v", p.CourseModules)
	}
	if len(p.CourseModules[2].Lessons) != 2 {
		t.Errorf("expected 2 lessons in m3, got %d", len(p.CourseModules[2].Lessons))
	}
}

func TestApplyOperation_AddLesson_RecalculatesTotalLessons(t *testing.T) {
	p := newTestProduct()
	err := applyOperation(p, pkgmodels.PatchOperation{
		Op:   "add",
		Path: "/module/m1/lessons",
		Value: map[string]interface{}{
			"slug": "l1b", "title": "Lesson 1B",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := recalculateTotalLessons(p); got != 4 {
		t.Errorf("TotalLessons = %d, want 4", got)
	}
	if p.CourseModules[0].Lessons[2].Slug != "l1b" {
		t.Errorf("expected new lesson appended; got slugs %v",
			lessonSlugs(p.CourseModules[0].Lessons))
	}
	if p.CourseModules[0].Lessons[2].Order != 3 {
		t.Errorf("expected auto-assigned order 3, got %d", p.CourseModules[0].Lessons[2].Order)
	}
}

// ---------- remove ----------

func TestApplyOperation_RemoveModule_RecalculatesTotalLessons(t *testing.T) {
	p := newTestProduct()
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op: "remove", Path: "/module/m1",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.CourseModules) != 1 || p.CourseModules[0].Slug != "m2" {
		t.Errorf("expected only m2 to remain, got %v", moduleSlugs(p.CourseModules))
	}
	if got := recalculateTotalLessons(p); got != 1 {
		t.Errorf("TotalLessons = %d, want 1", got)
	}
}

func TestApplyOperation_RemoveLesson_RecalculatesTotalLessons(t *testing.T) {
	p := newTestProduct()
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op: "remove", Path: "/module/m1/lesson/l1",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.CourseModules[0].Lessons) != 1 || p.CourseModules[0].Lessons[0].Slug != "l2" {
		t.Errorf("expected only l2 to remain in m1, got %v",
			lessonSlugs(p.CourseModules[0].Lessons))
	}
	if got := recalculateTotalLessons(p); got != 2 {
		t.Errorf("TotalLessons = %d, want 2", got)
	}
}

// ---------- reorder ----------

func TestApplyOperation_ReorderModules(t *testing.T) {
	p := newTestProduct()
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op: "reorder", Path: "/modules", Value: []interface{}{"m2", "m1"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.CourseModules[0].Slug != "m2" || p.CourseModules[1].Slug != "m1" {
		t.Errorf("reorder failed: %v", moduleSlugs(p.CourseModules))
	}
}

func TestApplyOperation_ReorderLessons(t *testing.T) {
	p := newTestProduct()
	if err := applyOperation(p, pkgmodels.PatchOperation{
		Op: "reorder", Path: "/module/m1/lessons", Value: []interface{}{"l2", "l1"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.CourseModules[0].Lessons[0].Slug != "l2" {
		t.Errorf("lesson reorder failed: %v", lessonSlugs(p.CourseModules[0].Lessons))
	}
}

// ---------- unsupported op ----------

func TestApplyOperation_UnsupportedOp(t *testing.T) {
	p := newTestProduct()
	err := applyOperation(p, pkgmodels.PatchOperation{Op: "bogus", Path: "/description", Value: "x"})
	if err == nil {
		t.Fatal("expected error for unsupported op")
	}
}

// ---------- splitLessonTarget ----------

func TestSplitLessonTarget_ModuleLessonForm(t *testing.T) {
	p := newTestProduct()
	mod, lesson := splitLessonTarget(p, "m1/l1")
	if mod != "m1" || lesson != "l1" {
		t.Errorf("got (%q,%q), want (m1,l1)", mod, lesson)
	}
}

func TestSplitLessonTarget_BareLessonSlug_ResolvesModule(t *testing.T) {
	p := newTestProduct()
	mod, lesson := splitLessonTarget(p, "l3")
	if mod != "m2" || lesson != "l3" {
		t.Errorf("got (%q,%q), want (m2,l3)", mod, lesson)
	}
}

// ---------- buildEditOperations ----------

func TestBuildEditOperations_RewriteCourse(t *testing.T) {
	p := newTestProduct()
	ops, err := buildEditOperations(p, "shorter intro", "course", p.Name, "rewrite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Op != "replace" || ops[0].Path != "/description" {
		t.Errorf("unexpected ops: %+v", ops)
	}
}

func TestBuildEditOperations_RewriteLesson_BareSlugResolves(t *testing.T) {
	p := newTestProduct()
	ops, err := buildEditOperations(p, "clarify", "lesson", "l1", "rewrite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	wantPath := "/module/m1/lesson/l1/content_markdown"
	if ops[0].Path != wantPath {
		t.Errorf("path = %q, want %q", ops[0].Path, wantPath)
	}
}

func TestBuildEditOperations_ConvertToStub(t *testing.T) {
	p := newTestProduct()
	ops, err := buildEditOperations(p, "convert this lesson to a stub video", "lesson", "m1/l1", "convert")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 4 {
		t.Fatalf("expected 4 ops, got %d", len(ops))
	}
	paths := make([]string, len(ops))
	for i, o := range ops {
		paths[i] = o.Path
	}
	wantSuffixes := []string{"/video_mode", "/video_stub_script", "/video_stub_description", "/video_upload_pending"}
	for _, s := range wantSuffixes {
		found := false
		for _, p := range paths {
			if strings.HasSuffix(p, s) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing op with suffix %q in %v", s, paths)
		}
	}
}

func TestBuildEditOperations_QuizScope_Errors(t *testing.T) {
	p := newTestProduct()
	_, err := buildEditOperations(p, "change something", "quiz", "q1", "quiz")
	if err == nil {
		t.Fatal("expected error for quiz scope")
	}
	if !strings.Contains(err.Error(), "quiz") {
		t.Errorf("error should mention quiz: %v", err)
	}
}

func TestBuildEditOperations_CertificateScope_Errors(t *testing.T) {
	p := newTestProduct()
	_, err := buildEditOperations(p, "change cert", "certificate", "c1", "certificate")
	if err == nil {
		t.Fatal("expected error for certificate scope")
	}
}

func TestBuildEditOperations_AddModule(t *testing.T) {
	p := newTestProduct()
	ops, err := buildEditOperations(p, "add a module about deployment", "course", p.Name, "add")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Op != "add" || ops[0].Path != "/modules" {
		t.Errorf("unexpected ops: %+v", ops)
	}
}

func TestBuildEditOperations_AddLessonUnderModule(t *testing.T) {
	p := newTestProduct()
	ops, err := buildEditOperations(p, "add a lesson about testing", "module", "m1", "add")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Op != "add" || ops[0].Path != "/module/m1/lessons" {
		t.Errorf("unexpected ops: %+v", ops)
	}
}

func TestBuildEditOperations_RemoveLesson(t *testing.T) {
	p := newTestProduct()
	ops, err := buildEditOperations(p, "drop this", "lesson", "l1", "remove")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Op != "remove" || ops[0].Path != "/module/m1/lesson/l1" {
		t.Errorf("unexpected ops: %+v", ops)
	}
}

// ---------- recalculateTotalLessons ----------

func TestRecalculateTotalLessons(t *testing.T) {
	p := newTestProduct()
	if got := recalculateTotalLessons(p); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

// ---------- util ----------

func lessonSlugs(ls []*pkgmodels.CourseLesson) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Slug
	}
	return out
}

func moduleSlugs(ms []*pkgmodels.CourseModule) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Slug
	}
	return out
}
