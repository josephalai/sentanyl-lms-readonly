package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/llm"
	"github.com/josephalai/sentanyl/lms-service/queries"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
	"github.com/josephalai/sentanyl/pkg/utils"
)

// GenerationService is the shared service for staged course generation.
// Both the LMS UI and the SentanylScript compiler dispatch into this service
// so that one canonical pipeline drives all course generation and editing.
type GenerationService struct {
	// llmProvider is resolved lazily the first time it's needed. nil means
	// "no LLM configured — fall back to the deterministic stub".
	llmProvider     llm.Provider
	llmProviderErr  error
	llmProviderOnce sync.Once
}

// NewGenerationService creates a GenerationService.
func NewGenerationService() *GenerationService {
	return &GenerationService{}
}

// SetLLMProvider overrides the configured provider (useful for tests).
func (s *GenerationService) SetLLMProvider(p llm.Provider) {
	s.llmProviderOnce.Do(func() {}) // mark as initialised
	s.llmProvider = p
	s.llmProviderErr = nil
}

func (s *GenerationService) provider() llm.Provider {
	s.llmProviderOnce.Do(func() {
		p, err := llm.NewFromEnv()
		s.llmProvider = p
		s.llmProviderErr = err
		if err != nil {
			log.Printf("[LMS] LLM provider init error (falling back to stub): %v", err)
		} else if p != nil {
			log.Printf("[LMS] LLM provider: %s", p.Name())
		} else {
			log.Printf("[LMS] No LLM provider configured — generation will use deterministic stub")
		}
	})
	return s.llmProvider
}

// CourseOutline is the JSON structure returned by the outline generation stage.
type CourseOutline struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Modules     []OutlineModule `json:"modules"`
}

// OutlineModule represents a module inside a generated outline.
type OutlineModule struct {
	Title       string          `json:"title"`
	Order       int             `json:"order"`
	Lessons     []OutlineLesson `json:"lessons"`
	QuizEnabled bool            `json:"quiz_enabled,omitempty"`
}

// OutlineLesson represents a lesson inside a generated outline module.
type OutlineLesson struct {
	Title       string `json:"title"`
	Order       int    `json:"order"`
	VideoMode   string `json:"video_mode,omitempty"`
	Description string `json:"description,omitempty"`
}

// GenerateOutline creates a generation job and produces a structured outline.
// In production this would call an LLM; here it returns a deterministic outline
// based on the prompt and configuration so the pipeline can be tested end-to-end.
func (s *GenerationService) GenerateOutline(
	tenantID bson.ObjectId,
	productPublicId string,
	prompt string,
	audience string,
	outcome string,
	tone string,
	moduleCount int,
	quizzesEnabled bool,
	certEnabled bool,
	defaultMedia string,
	extraContext string,
	referenceIds []string,
) (*pkgmodels.GenerationJob, *CourseOutline, error) {
	return s.GenerateOutlineContext(context.Background(), tenantID, productPublicId, prompt, audience, outcome, tone, moduleCount, quizzesEnabled, certEnabled, defaultMedia, extraContext, referenceIds)
}

// GenerateOutlineContext is the cancellable form used by HTTP generation
// operations. GenerateOutline remains as a compatibility wrapper for script
// callers and tests that do not supply a request context.
func (s *GenerationService) GenerateOutlineContext(
	ctx context.Context,
	tenantID bson.ObjectId,
	productPublicId string,
	prompt string,
	audience string,
	outcome string,
	tone string,
	moduleCount int,
	quizzesEnabled bool,
	certEnabled bool,
	defaultMedia string,
	extraContext string,
	referenceIds []string,
) (*pkgmodels.GenerationJob, *CourseOutline, error) {
	job := pkgmodels.NewGenerationJob(tenantID, "", "create")
	job.ProductPublicId = productPublicId
	job.Prompt = prompt
	job.Audience = audience
	job.Outcome = outcome
	job.Tone = tone
	job.ModuleCount = moduleCount
	job.QuizzesEnabled = quizzesEnabled
	job.CertEnabled = certEnabled
	job.DefaultMedia = defaultMedia
	job.ExtraContext = extraContext
	job.ReferenceIds = referenceIds

	now := time.Now()
	job.StartedAt = &now
	job.Status = pkgmodels.GenStatusGeneratingOutline

	// Gather reference text for grounding
	var refText string
	for _, refId := range referenceIds {
		ref, err := queries.GetSourceReferenceByPublicId(tenantID, refId)
		if err == nil {
			refText += ref.ExtractedText + "\n"
		}
	}

	if moduleCount <= 0 {
		moduleCount = 3
	}

	// Prefer the real LLM when a provider is configured. It receives the
	// reference material as context to teach from — not as lesson content.
	var outline CourseOutline
	if p := s.provider(); p != nil {
		ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		llmOut, err := p.GenerateOutline(ctx, llm.OutlineRequest{
			Prompt:         prompt,
			Audience:       audience,
			Outcome:        outcome,
			Tone:           tone,
			ModuleCount:    moduleCount,
			QuizzesEnabled: quizzesEnabled,
			CertEnabled:    certEnabled,
			DefaultMedia:   defaultMedia,
			ExtraContext:   extraContext,
			ReferenceText:  refText,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			log.Printf("[LMS] LLM outline failed, falling back to deterministic stub: %v", err)
			outline = buildDeterministicOutline(prompt, audience, outcome, moduleCount, quizzesEnabled, defaultMedia, refText)
		} else {
			outline = outlineFromLLM(llmOut, defaultMedia, quizzesEnabled)
		}
	} else {
		outline = buildDeterministicOutline(prompt, audience, outcome, moduleCount, quizzesEnabled, defaultMedia, refText)
	}

	outlineBytes, _ := json.Marshal(outline)
	job.OutlineJSON = string(outlineBytes)
	job.Status = pkgmodels.GenStatusOutlineReady

	created, err := queries.CreateGenerationJob(job)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create generation job: %w", err)
	}

	return created, &outline, nil
}

// outlineFromLLM converts the provider's schema-typed response into the
// internal CourseOutline shape, backfilling video_mode and quiz_enabled from
// the user's request when the model omits them.
func outlineFromLLM(resp *llm.OutlineResponse, defaultMedia string, quizzesEnabled bool) CourseOutline {
	out := CourseOutline{
		Title:       strings.TrimSpace(resp.Title),
		Description: strings.TrimSpace(resp.Description),
	}
	for i, m := range resp.Modules {
		order := m.Order
		if order == 0 {
			order = i + 1
		}
		mod := OutlineModule{
			Title:       strings.TrimSpace(m.Title),
			Order:       order,
			QuizEnabled: m.QuizEnabled || quizzesEnabled,
		}
		for j, l := range m.Lessons {
			lOrder := l.Order
			if lOrder == 0 {
				lOrder = j + 1
			}
			videoMode := l.VideoMode
			if videoMode == "" {
				videoMode = defaultMedia
			}
			mod.Lessons = append(mod.Lessons, OutlineLesson{
				Title:       strings.TrimSpace(l.Title),
				Order:       lOrder,
				VideoMode:   videoMode,
				Description: strings.TrimSpace(l.Description),
			})
		}
		out.Modules = append(out.Modules, mod)
	}
	return out
}

// MaterializeCourse takes an approved outline and generates the full course tree.
func (s *GenerationService) MaterializeCourse(
	tenantID bson.ObjectId,
	productPublicId string,
	jobPublicId string,
	outline CourseOutline,
	defaultMedia string,
	quizzesEnabled bool,
	certEnabled bool,
) (*pkgmodels.GenerationJob, error) {
	return s.MaterializeCourseContext(context.Background(), tenantID, productPublicId, jobPublicId, outline, defaultMedia, quizzesEnabled, certEnabled)
}

// MaterializeCourseContext propagates cancellation through every parallel
// lesson and quiz provider call.
func (s *GenerationService) MaterializeCourseContext(
	ctx context.Context,
	tenantID bson.ObjectId,
	productPublicId string,
	jobPublicId string,
	outline CourseOutline,
	defaultMedia string,
	quizzesEnabled bool,
	certEnabled bool,
) (*pkgmodels.GenerationJob, error) {
	product, err := queries.GetCourseProductByPublicId(tenantID, productPublicId)
	if err != nil {
		return nil, fmt.Errorf("course not found: %w", err)
	}

	// Update job status
	if jobPublicId != "" {
		queries.UpdateGenerationJob(tenantID, jobPublicId, bson.M{
			"status": string(pkgmodels.GenStatusGeneratingContent),
		})
	}

	// Pull the job so we can reuse the user's framing (audience, tone,
	// reference IDs) when generating per-lesson content with the LLM.
	var (
		audience string
		tone     string
		refText  string
	)
	if jobPublicId != "" {
		if job, err := queries.GetGenerationJobByPublicId(tenantID, jobPublicId); err == nil {
			audience = job.Audience
			tone = job.Tone
			for _, refId := range job.ReferenceIds {
				if ref, err := queries.GetSourceReferenceByPublicId(tenantID, refId); err == nil {
					refText += ref.ExtractedText + "\n\n"
				}
			}
		}
	}

	// Generate lesson markdown and module quizzes via the LLM in parallel.
	// Each fan-out is independently bounded so we hit the provider with at
	// most 4+4 concurrent calls and let them finish as fast as the network
	// allows. Skipped cleanly when no provider is configured.
	var (
		lessonBodies map[string]string
		quizzes      map[int]*llm.QuizResponse
	)
	{
		var pwg sync.WaitGroup
		pwg.Add(2)
		go func() {
			defer pwg.Done()
			lessonBodies = s.generateLessonBodiesContext(ctx, outline, audience, tone, refText)
		}()
		go func() {
			defer pwg.Done()
			quizzes = s.generateQuizzesContext(ctx, outline, audience, tone, refText, quizzesEnabled)
		}()
		pwg.Wait()
	}
	if err := ctx.Err(); err != nil {
		if jobPublicId != "" {
			queries.UpdateGenerationJob(tenantID, jobPublicId, bson.M{"status": string(pkgmodels.GenStatusFailed), "error_message": err.Error()})
		}
		return nil, err
	}

	// Build course modules from outline
	var modules []*pkgmodels.CourseModule
	totalLessons := 0
	for i, om := range outline.Modules {
		mod := &pkgmodels.CourseModule{
			Slug:  fmt.Sprintf("mod-%d", i+1),
			Title: om.Title,
			Order: om.Order,
		}
		for j, ol := range om.Lessons {
			videoMode := pkgmodels.VideoMode(defaultMedia)
			if ol.VideoMode != "" {
				videoMode = pkgmodels.VideoMode(ol.VideoMode)
			}
			if videoMode == "" {
				videoMode = pkgmodels.VideoModeNone
			}

			lesson := &pkgmodels.CourseLesson{
				Slug:      fmt.Sprintf("lesson-%d-%d", i+1, j+1),
				Title:     ol.Title,
				Order:     ol.Order,
				VideoMode: videoMode,
			}

			// Prefer the LLM-generated body; fall back to the outline
			// description; fall back to a placeholder.
			var markdownBody string
			if body, ok := lessonBodies[lessonKey(i, j)]; ok {
				markdownBody = body
			}
			outlineBrief := strings.TrimSpace(ol.Description)
			if markdownBody == "" {
				if outlineBrief != "" {
					markdownBody = outlineBrief
				} else {
					markdownBody = fmt.Sprintf("Content for **%s** will be developed next. Use the AI Assistant to flesh this lesson out.", ol.Title)
				}
			}
			lesson.ContentMarkdown = markdownBody

			if videoMode == pkgmodels.VideoModeStub {
				narration := markdownBody
				if outlineBrief != "" {
					narration = outlineBrief
				}
				lesson.VideoStubScript = fmt.Sprintf("Narration draft for \"%s\":\n\n%s", ol.Title, truncate(narration, 800))
				if outlineBrief != "" {
					lesson.VideoStubDescription = truncate(outlineBrief, 200)
				} else {
					lesson.VideoStubDescription = truncate(markdownBody, 200)
				}
				lesson.VideoUploadPending = true
			}
			mod.Lessons = append(mod.Lessons, lesson)
			totalLessons++
		}

		// Add quiz if enabled. The LLM-generated response is preferred; when
		// no provider is configured or the call failed, fall back to a
		// single-question placeholder so the module still has a quiz entity.
		if quizzesEnabled && om.QuizEnabled {
			quizSlug := fmt.Sprintf("quiz-%d", i+1)
			mod.QuizSlug = quizSlug
			quiz := buildModuleQuiz(tenantID, product.Id, mod.Slug, quizSlug, om.Title, quizzes[i])
			queries.CreateLMSQuiz(quiz)
		}

		modules = append(modules, mod)
	}

	// Persist the tree
	updateDoc := bson.M{
		"course_modules":        modules,
		"total_lessons":         totalLessons,
		"timestamps.updated_at": time.Now(),
	}
	if product.Status == "" || product.Status == "draft" {
		updateDoc["status"] = "draft"
	}

	err = queries.UpdateProduct(tenantID, product.PublicId, updateDoc)
	if err != nil {
		return nil, fmt.Errorf("failed to update course tree: %w", err)
	}

	// Create certificate template if requested
	if certEnabled {
		tmpl := pkgmodels.NewCertificateTemplate(tenantID, product.Id, "", product.PublicId, outline.Title+" Certificate")
		tmpl.Enabled = true
		queries.CreateCertificateTemplate(tmpl)
	}

	// Update job to completed
	if jobPublicId != "" {
		queries.UpdateGenerationJob(tenantID, jobPublicId, bson.M{
			"status":       string(pkgmodels.GenStatusDraftReady),
			"completed_at": time.Now(),
		})
	}

	// Record revision event
	revEvent := pkgmodels.NewCourseRevisionEvent(tenantID, productPublicId, "generation",
		"Full course generated from outline")
	queries.CreateCourseRevisionEvent(revEvent)

	// Return the updated job
	if jobPublicId != "" {
		job, _ := queries.GetGenerationJobByPublicId(tenantID, jobPublicId)
		return job, nil
	}
	return nil, nil
}

// lessonKey is a stable map key for the (moduleIndex, lessonIndex) pair used
// when stitching parallel LLM lesson-body results back into the outline.
func lessonKey(moduleIdx, lessonIdx int) string {
	return fmt.Sprintf("%d:%d", moduleIdx, lessonIdx)
}

// generateLessonBodies produces per-lesson markdown content by calling the
// configured LLM provider in parallel with bounded concurrency. Returns an
// empty map when no provider is configured or when every call failed — the
// caller is responsible for applying a fallback per lesson.
func (s *GenerationService) generateLessonBodies(outline CourseOutline, audience, tone, refText string) map[string]string {
	return s.generateLessonBodiesContext(context.Background(), outline, audience, tone, refText)
}

func (s *GenerationService) generateLessonBodiesContext(parent context.Context, outline CourseOutline, audience, tone, refText string) map[string]string {
	bodies := map[string]string{}
	p := s.provider()
	if p == nil {
		return bodies
	}

	type task struct {
		key, modTitle, lessonTitle, brief string
	}
	var tasks []task
	for i, m := range outline.Modules {
		for j, l := range m.Lessons {
			tasks = append(tasks, task{
				key:         lessonKey(i, j),
				modTitle:    m.Title,
				lessonTitle: l.Title,
				brief:       l.Description,
			})
		}
	}
	if len(tasks) == 0 {
		return bodies
	}

	const concurrency = 4
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, t := range tasks {
		t := t
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(parent, 90*time.Second)
			defer cancel()

			md, err := p.GenerateLessonContent(ctx, llm.LessonContentRequest{
				CourseTitle:   outline.Title,
				ModuleTitle:   t.modTitle,
				LessonTitle:   t.lessonTitle,
				LessonBrief:   t.brief,
				Audience:      audience,
				Tone:          tone,
				ReferenceText: refText,
			})
			if err != nil {
				log.Printf("[LMS] LLM lesson content failed for %q: %v", t.lessonTitle, err)
				return
			}
			md = strings.TrimSpace(md)
			if md == "" {
				return
			}
			mu.Lock()
			bodies[t.key] = md
			mu.Unlock()
		}()
	}
	wg.Wait()
	return bodies
}

// generateQuizzes produces one LMS quiz per quiz-enabled module by calling
// the configured LLM provider in parallel with bounded concurrency. Keyed by
// the module's index in outline.Modules. Returns an empty map when no
// provider is configured or when quizzes are globally disabled — the caller
// is responsible for applying a per-module fallback.
func (s *GenerationService) generateQuizzes(
	outline CourseOutline,
	audience, tone, refText string,
	quizzesEnabled bool,
) map[int]*llm.QuizResponse {
	return s.generateQuizzesContext(context.Background(), outline, audience, tone, refText, quizzesEnabled)
}

func (s *GenerationService) generateQuizzesContext(
	parent context.Context,
	outline CourseOutline,
	audience, tone, refText string,
	quizzesEnabled bool,
) map[int]*llm.QuizResponse {
	out := map[int]*llm.QuizResponse{}
	if !quizzesEnabled {
		return out
	}
	p := s.provider()
	if p == nil {
		return out
	}

	type task struct {
		idx    int
		module OutlineModule
	}
	var tasks []task
	for i, m := range outline.Modules {
		if m.QuizEnabled {
			tasks = append(tasks, task{idx: i, module: m})
		}
	}
	if len(tasks) == 0 {
		return out
	}

	const concurrency = 4
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, t := range tasks {
		t := t
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(parent, 90*time.Second)
			defer cancel()

			titles := make([]string, 0, len(t.module.Lessons))
			for _, l := range t.module.Lessons {
				titles = append(titles, l.Title)
			}

			resp, err := p.GenerateQuiz(ctx, llm.QuizRequest{
				CourseTitle:   outline.Title,
				ModuleTitle:   t.module.Title,
				LessonTitles:  titles,
				Audience:      audience,
				Tone:          tone,
				ReferenceText: refText,
				QuestionCount: 4,
			})
			if err != nil {
				log.Printf("[LMS] LLM quiz failed for module %q: %v", t.module.Title, err)
				return
			}
			mu.Lock()
			out[t.idx] = resp
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// buildModuleQuiz converts an LLM quiz response into a persisted LMSQuiz.
// When resp is nil (LLM failed, provider absent, or quiz marked disabled at
// generation time) a single-question placeholder is returned so the module
// always has an addressable quiz document the author can fill in.
func buildModuleQuiz(
	tenantID bson.ObjectId,
	productID bson.ObjectId,
	moduleSlug string,
	quizSlug string,
	moduleTitle string,
	resp *llm.QuizResponse,
) *pkgmodels.LMSQuiz {
	q := &pkgmodels.LMSQuiz{
		Id:            bson.NewObjectId(),
		PublicId:      utils.GeneratePublicId(),
		TenantID:      tenantID,
		ProductID:     productID,
		ModuleSlug:    moduleSlug,
		Slug:          quizSlug,
		PassThreshold: 70,
		MaxAttempts:   3,
	}

	if resp != nil && len(resp.Questions) > 0 {
		if t := strings.TrimSpace(resp.Title); t != "" {
			q.Title = t
		}
		if resp.PassThreshold > 0 && resp.PassThreshold <= 100 {
			q.PassThreshold = resp.PassThreshold
		}
		if resp.MaxAttempts > 0 {
			q.MaxAttempts = resp.MaxAttempts
		}
		for i, qr := range resp.Questions {
			slug := strings.TrimSpace(qr.Slug)
			if slug == "" {
				slug = fmt.Sprintf("q%d", i+1)
			}
			qType := strings.TrimSpace(qr.Type)
			if qType != "multiple_choice" && qType != "short_answer" && qType != "true_false" {
				qType = "multiple_choice"
			}
			question := &pkgmodels.LMSQuizQuestion{
				Slug:          slug,
				Type:          qType,
				Title:         strings.TrimSpace(qr.Title),
				Options:       qr.Options,
				CorrectAnswer: qr.CorrectAnswer,
				CorrectText:   strings.TrimSpace(qr.CorrectText),
				Order:         i + 1,
			}
			q.Questions = append(q.Questions, question)
		}
	}

	if q.Title == "" {
		q.Title = fmt.Sprintf("%s Quiz", moduleTitle)
	}
	if len(q.Questions) == 0 {
		q.Questions = []*pkgmodels.LMSQuizQuestion{
			{
				Slug:          "q1",
				Type:          "multiple_choice",
				Title:         fmt.Sprintf("Question about %s", moduleTitle),
				Options:       []string{"Option A", "Option B", "Option C", "Option D"},
				CorrectAnswer: 0,
				Order:         1,
			},
		}
	}

	q.SetCreated()
	return q
}

// buildDeterministicOutline produces a course outline from the user's prompt,
// audience/outcome framing, and any uploaded reference text. This is the
// fallback used until a real LLM integration is wired in — it is deliberately
// extractive, not generative, so the output reflects what the user actually
// submitted instead of hard-coded placeholders.
func buildDeterministicOutline(prompt, audience, outcome string, moduleCount int, quizzesEnabled bool, defaultMedia string, refText string) CourseOutline {
	topic := extractTopic(prompt)
	title := titleCase(topic)
	if title == "" {
		title = titleCase(firstSentence(prompt))
	}
	if title == "" {
		title = "New Course"
	}

	description := composeDescription(prompt, audience, outcome)

	chunks := chunkReferenceText(refText)
	refHeadings := extractMarkdownHeadings(refText)

	outline := CourseOutline{
		Title:       title,
		Description: description,
	}

	if len(chunks) == 0 {
		// No reference text: fall back to a topic-templated skeleton with
		// prompt-derived descriptions so every lesson still says something
		// related to what the user asked for.
		outline.Modules = buildTemplateModules(topic, moduleCount, quizzesEnabled, defaultMedia)
		return outline
	}

	// Reference-driven path: size the outline to the material so we never pad
	// with empty placeholder lessons. The user's moduleCount is the ceiling;
	// if they uploaded fewer chunks than that, we compress.
	modules := moduleCount
	if modules < 1 {
		modules = 3
	}
	if modules > len(chunks) {
		modules = len(chunks)
	}
	// Distribute chunks across modules, spreading the remainder across the
	// first few modules so each lesson carries content.
	base := len(chunks) / modules
	extra := len(chunks) % modules

	chunkIdx := 0
	for i := 0; i < modules; i++ {
		lessonsThisModule := base
		if i < extra {
			lessonsThisModule++
		}

		// Pick a module title: prefer a matching markdown heading from the
		// reference (one per module, in order); fall back to a topic template.
		var modTitle string
		if i < len(refHeadings) {
			modTitle = refHeadings[i]
		}
		if modTitle == "" {
			modTitle = templatedModuleTitle(i+1, topic)
		}

		mod := OutlineModule{
			Title:       modTitle,
			Order:       i + 1,
			QuizEnabled: quizzesEnabled,
		}

		for j := 0; j < lessonsThisModule && chunkIdx < len(chunks); j++ {
			chunk := chunks[chunkIdx]
			chunkIdx++
			mod.Lessons = append(mod.Lessons, OutlineLesson{
				Title:       deriveLessonTitle(chunk, fmt.Sprintf("Lesson %d.%d", i+1, j+1)),
				Order:       j + 1,
				VideoMode:   defaultMedia,
				Description: chunk,
			})
		}
		outline.Modules = append(outline.Modules, mod)
	}

	return outline
}

// buildTemplateModules produces a skeletal module list when no reference text
// is available. Every lesson still carries a topic-aware placeholder
// description so the outline review never shows blank rows.
func buildTemplateModules(topic string, moduleCount int, quizzesEnabled bool, defaultMedia string) []OutlineModule {
	if moduleCount < 1 {
		moduleCount = 3
	}

	lessonFocus := []string{
		"Orientation and key terms",
		"Core principles and why they work",
		"Walkthrough with a concrete example",
	}

	var mods []OutlineModule
	for i := 1; i <= moduleCount; i++ {
		mod := OutlineModule{
			Title:       templatedModuleTitle(i, topic),
			Order:       i,
			QuizEnabled: quizzesEnabled,
		}
		for j, focus := range lessonFocus {
			var desc string
			if topic != "" {
				desc = fmt.Sprintf("%s — framed around %s. Expand this lesson with your own examples, references, or AI edits.", focus, topic)
			} else {
				desc = fmt.Sprintf("%s. Expand this lesson with your own examples, references, or AI edits.", focus)
			}
			mod.Lessons = append(mod.Lessons, OutlineLesson{
				Title:       titleCase(focus),
				Order:       j + 1,
				VideoMode:   defaultMedia,
				Description: desc,
			})
		}
		mods = append(mods, mod)
	}
	return mods
}

var moduleTemplates = []string{
	"Introduction to %s",
	"Foundations of %s",
	"Core Principles of %s",
	"Working with %s in Practice",
	"Advanced %s",
	"Applying %s to Real Cases",
	"Mastering %s",
}

func templatedModuleTitle(order int, topic string) string {
	if topic == "" {
		return fmt.Sprintf("Module %d", order)
	}
	template := moduleTemplates[(order-1)%len(moduleTemplates)]
	return strings.TrimSpace(fmt.Sprintf(template, topic))
}

// deriveLessonTitle produces a human-readable lesson title from a reference
// chunk. Prefers explicit markdown headings, then a truncated first sentence,
// then the supplied fallback.
func deriveLessonTitle(chunk string, fallback string) string {
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return fallback
	}

	// 1. Leading markdown heading (`# `, `## `, `### `, etc.).
	for _, line := range strings.Split(chunk, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if heading != "" {
				return smartTruncate(heading, 80)
			}
		}
		break
	}

	// 2. First sentence, truncated to a readable length at a word boundary.
	first := firstSentence(chunk)
	if first == "" {
		return fallback
	}
	title := smartTruncate(first, 72)
	if title == "" {
		return fallback
	}
	return title
}

// extractMarkdownHeadings pulls H1/H2 headings out of the reference text in
// document order so they can seed module titles when the source is
// structured.
func extractMarkdownHeadings(refText string) []string {
	var out []string
	for _, line := range strings.Split(refText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if heading != "" {
				out = append(out, smartTruncate(heading, 80))
			}
		}
	}
	return out
}

// smartTruncate returns s cut to at most max characters, preferring a word
// boundary, trimming trailing punctuation, and appending an ellipsis when
// truncation happened.
func smartTruncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	cut := max
	// Prefer cutting at the last space within the window (but not too close
	// to the start, or we'd produce a one-word title).
	for i := max - 1; i > max/2; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	trimmed := strings.TrimRight(strings.TrimSpace(string(runes[:cut])), ".,;:!?-")
	return trimmed + "…"
}

// composeDescription builds a short, user-visible course description from
// whatever framing fields were provided. If audience/outcome are empty we fall
// back to the prompt itself so the description is never a hollow template.
func composeDescription(prompt, audience, outcome string) string {
	audience = strings.TrimSpace(audience)
	outcome = strings.TrimSpace(outcome)
	prompt = strings.TrimSpace(prompt)

	var parts []string
	if audience != "" {
		parts = append(parts, "For "+audience)
	}
	if outcome != "" {
		parts = append(parts, outcome)
	}
	framing := strings.Join(parts, ". ")

	if framing != "" && prompt != "" {
		return framing + ". " + firstSentence(prompt)
	}
	if framing != "" {
		return framing
	}
	if prompt != "" {
		return truncate(prompt, 280)
	}
	return "A draft course ready for editing."
}

// extractTopic pulls a concise topic phrase out of a free-form prompt like
// "Make a course on the Bridge of Events, teaching people how to...". It
// strips common imperative/framing clauses and returns the remainder.
func extractTopic(prompt string) string {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return ""
	}

	// Drop everything after a sentence-splitting comma followed by a framing
	// clause (", teaching...", ", helping...", etc.) so we don't splice the
	// audience description into the topic.
	if idx := topicClauseSplit.FindStringIndex(p); idx != nil {
		p = p[:idx[0]]
	}

	// Strip known imperative openers.
	p = topicOpenerPattern.ReplaceAllString(p, "")
	// Trim trailing punctuation and filler words.
	p = strings.Trim(p, " .,:;!?\n\t")

	// Title-case "the X of Y" style phrases look better when we preserve
	// them verbatim rather than force sentence case downstream.
	return p
}

var (
	topicOpenerPattern = regexp.MustCompile(`(?i)^\s*(please\s+)?(make|create|build|design|generate|produce|write|give\s+me|i\s+want|i\s+need)\s+(a\s+|an\s+|the\s+)?(course|lesson|lessons|curriculum|module|modules)\s+(on|about|for|covering|that\s+teaches|to\s+teach|around)\s+`)
	topicClauseSplit   = regexp.MustCompile(`(?i),\s*(teaching|helping|showing|so\s+that|for\s+people|aimed\s+at|to\s+help|to\s+teach|that\s+helps|that\s+teaches)\b`)
)

// chunkReferenceText splits raw reference text into digestible, lesson-sized
// chunks. Prefers markdown/prose paragraph boundaries; falls back to a
// character-count split for very long single paragraphs.
func chunkReferenceText(refText string) []string {
	refText = strings.TrimSpace(refText)
	if refText == "" {
		return nil
	}

	// First split on paragraph boundaries (double newline).
	raw := regexp.MustCompile(`\r?\n\s*\r?\n`).Split(refText, -1)
	var chunks []string
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Subdivide oversized paragraphs so one giant wall of text doesn't
		// swallow the whole outline.
		for _, piece := range splitByLength(p, 1200) {
			piece = strings.TrimSpace(piece)
			if piece != "" {
				chunks = append(chunks, piece)
			}
		}
	}
	return chunks
}

func splitByLength(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var out []string
	// Prefer splitting on sentence boundaries near the max.
	for len(s) > max {
		cut := max
		if idx := strings.LastIndexAny(s[:max], ".!?\n"); idx > max/2 {
			cut = idx + 1
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if strings.TrimSpace(s) != "" {
		out = append(out, s)
	}
	return out
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexAny(s, ".!?"); idx > 0 {
		return strings.TrimSpace(s[:idx+1])
	}
	return s
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max
	if idx := strings.LastIndex(s[:max], " "); idx > max/2 {
		cut = idx
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func titleCase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Preserve the user's casing for phrases like "Bridge of Events"; just
	// make sure the very first character is uppercase.
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

func init() {
	log.Println("[LMS] Generation service initialized")
}
