package services

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/llm"
)

// ---------- fake provider ----------

type fakeProvider struct {
	name           string
	outlineCalls   int32
	contentCalls   int32
	quizCalls      int32
	outline        *llm.OutlineResponse
	outlineErr     error
	contentByTitle map[string]string
	contentErr     error
	quizByModule   map[string]*llm.QuizResponse
	quizErr        error
	lastOutlineReq llm.OutlineRequest
	lastContentReq llm.LessonContentRequest
	lastQuizReq    llm.QuizRequest
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) GenerateOutline(ctx context.Context, req llm.OutlineRequest) (*llm.OutlineResponse, error) {
	atomic.AddInt32(&f.outlineCalls, 1)
	f.lastOutlineReq = req
	if f.outlineErr != nil {
		return nil, f.outlineErr
	}
	return f.outline, nil
}

func (f *fakeProvider) GenerateLessonContent(ctx context.Context, req llm.LessonContentRequest) (string, error) {
	atomic.AddInt32(&f.contentCalls, 1)
	f.lastContentReq = req
	if f.contentErr != nil {
		return "", f.contentErr
	}
	if md, ok := f.contentByTitle[req.LessonTitle]; ok {
		return md, nil
	}
	return "## " + req.LessonTitle + "\n\nGenerated body.", nil
}

func (f *fakeProvider) GenerateQuiz(ctx context.Context, req llm.QuizRequest) (*llm.QuizResponse, error) {
	atomic.AddInt32(&f.quizCalls, 1)
	f.lastQuizReq = req
	if f.quizErr != nil {
		return nil, f.quizErr
	}
	if resp, ok := f.quizByModule[req.ModuleTitle]; ok {
		return resp, nil
	}
	return &llm.QuizResponse{
		Title:         req.ModuleTitle + " Check-In",
		PassThreshold: 70,
		MaxAttempts:   3,
		Questions: []llm.QuestionResp{
			{Slug: "q1", Type: "multiple_choice", Title: "What does " + req.ModuleTitle + " cover?",
				Options: []string{"A", "B", "C", "D"}, CorrectAnswer: 1, CorrectText: "Because B."},
		},
	}, nil
}

// ---------- tests ----------

// TestOutlineFromLLM_PreservesLLMContent verifies the outline envelope
// returned to the caller reflects exactly what the LLM produced — module
// titles, lesson titles, and rich descriptions — rather than being
// post-processed into generic templates.
func TestOutlineFromLLM_PreservesLLMContent(t *testing.T) {
	resp := &llm.OutlineResponse{
		Title:       "The Bridge of Events",
		Description: "A practical course on how manifestation unfolds through the sequence of events.",
		Modules: []llm.OutlineModuleResp{
			{
				Title:       "Foundations",
				Order:       1,
				QuizEnabled: true,
				Lessons: []llm.OutlineLessonResp{
					{Title: "Why the Bridge Is Not About Time", Order: 1, Description: "Distinguishes chronological time from the change-sequence that links imagination to realisation."},
					{Title: "The Guarantee Principle", Order: 2, Description: "Explains why impressing the subconscious mind binds reality to rearrange."},
				},
			},
			{
				Title: "Common Misconceptions",
				Order: 2,
				Lessons: []llm.OutlineLessonResp{
					{Title: "\"Time\" vs. Change", Order: 1, Description: "Corrects the framing that bridges imply a delay."},
				},
			},
		},
	}
	out := outlineFromLLM(resp, "stub", true)

	if out.Title != "The Bridge of Events" {
		t.Errorf("title not preserved: %q", out.Title)
	}
	if !strings.Contains(out.Description, "manifestation") {
		t.Errorf("description not preserved: %q", out.Description)
	}
	if len(out.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(out.Modules))
	}
	if out.Modules[0].Title != "Foundations" {
		t.Errorf("module title not preserved: %q", out.Modules[0].Title)
	}
	if !out.Modules[0].QuizEnabled {
		t.Error("expected quiz_enabled to survive")
	}
	if out.Modules[0].Lessons[0].Title != "Why the Bridge Is Not About Time" {
		t.Errorf("lesson title not preserved: %q", out.Modules[0].Lessons[0].Title)
	}
	if !strings.Contains(out.Modules[0].Lessons[0].Description, "chronological time") {
		t.Errorf("lesson description not preserved: %q", out.Modules[0].Lessons[0].Description)
	}
	if out.Modules[0].Lessons[0].VideoMode != "stub" {
		t.Errorf("default video_mode not applied: %q", out.Modules[0].Lessons[0].VideoMode)
	}

	// Module 2 had no quiz_enabled flag; the user's quizzesEnabled=true
	// should propagate down as the default.
	if !out.Modules[1].QuizEnabled {
		t.Error("quizzesEnabled default did not propagate to module with no flag")
	}
}

// TestOutlineFromLLM_BackfillsOrder assigns 1-indexed order values when the
// provider omits them, so the frontend sees a clean numbered list.
func TestOutlineFromLLM_BackfillsOrder(t *testing.T) {
	resp := &llm.OutlineResponse{
		Title: "T",
		Modules: []llm.OutlineModuleResp{
			{Title: "A", Lessons: []llm.OutlineLessonResp{{Title: "L1"}, {Title: "L2"}}},
			{Title: "B", Lessons: []llm.OutlineLessonResp{{Title: "L3"}}},
		},
	}
	out := outlineFromLLM(resp, "none", false)
	if out.Modules[0].Order != 1 || out.Modules[1].Order != 2 {
		t.Errorf("module orders = %d, %d", out.Modules[0].Order, out.Modules[1].Order)
	}
	if out.Modules[0].Lessons[0].Order != 1 || out.Modules[0].Lessons[1].Order != 2 {
		t.Errorf("lesson orders = %d, %d", out.Modules[0].Lessons[0].Order, out.Modules[0].Lessons[1].Order)
	}
}

// TestGenerateLessonBodies_ParallelCallsLLM ensures that when a provider is
// configured, generateLessonBodies invokes it once per lesson and collects
// the returned markdown keyed by lesson position.
func TestGenerateLessonBodies_ParallelCallsLLM(t *testing.T) {
	fp := &fakeProvider{
		name:           "fake:test",
		contentByTitle: map[string]string{
			"Lesson A": "# A body",
			"Lesson B": "# B body",
			"Lesson C": "# C body",
		},
	}
	svc := NewGenerationService()
	svc.SetLLMProvider(fp)

	outline := CourseOutline{
		Title: "Course",
		Modules: []OutlineModule{
			{Title: "Mod 1", Lessons: []OutlineLesson{
				{Title: "Lesson A", Description: "brief A"},
				{Title: "Lesson B", Description: "brief B"},
			}},
			{Title: "Mod 2", Lessons: []OutlineLesson{
				{Title: "Lesson C", Description: "brief C"},
			}},
		},
	}
	bodies := svc.generateLessonBodies(outline, "devs", "friendly", "reference text")

	if atomic.LoadInt32(&fp.contentCalls) != 3 {
		t.Errorf("expected 3 LLM calls, got %d", fp.contentCalls)
	}
	if bodies[lessonKey(0, 0)] != "# A body" {
		t.Errorf("lesson A body = %q", bodies[lessonKey(0, 0)])
	}
	if bodies[lessonKey(0, 1)] != "# B body" {
		t.Errorf("lesson B body = %q", bodies[lessonKey(0, 1)])
	}
	if bodies[lessonKey(1, 0)] != "# C body" {
		t.Errorf("lesson C body = %q", bodies[lessonKey(1, 0)])
	}
}

// TestGenerateLessonBodies_NoProvider returns an empty map so the caller
// will apply its per-lesson fallback.
func TestGenerateLessonBodies_NoProvider(t *testing.T) {
	svc := NewGenerationService()
	svc.SetLLMProvider(nil)
	outline := CourseOutline{
		Title: "Course",
		Modules: []OutlineModule{
			{Title: "Mod 1", Lessons: []OutlineLesson{{Title: "L1"}}},
		},
	}
	bodies := svc.generateLessonBodies(outline, "", "", "")
	if len(bodies) != 0 {
		t.Errorf("expected empty map when no provider; got %v", bodies)
	}
}

// TestGenerateLessonBodies_ProviderError drops failed lessons silently so the
// caller can apply its fallback per-lesson without the whole batch failing.
func TestGenerateLessonBodies_ProviderError(t *testing.T) {
	fp := &fakeProvider{
		name:       "fake:test",
		contentErr: context.Canceled, // any error works
	}
	svc := NewGenerationService()
	svc.SetLLMProvider(fp)

	outline := CourseOutline{
		Title: "Course",
		Modules: []OutlineModule{
			{Title: "Mod 1", Lessons: []OutlineLesson{{Title: "L1"}, {Title: "L2"}}},
		},
	}
	bodies := svc.generateLessonBodies(outline, "", "", "")
	if len(bodies) != 0 {
		t.Errorf("expected empty map on provider error; got %v", bodies)
	}
}

// TestGenerateQuizzes_CallsProviderForQuizEnabledModules verifies that only
// modules with quiz_enabled=true trigger an LLM call, and that the response
// is keyed by module index.
func TestGenerateQuizzes_CallsProviderForQuizEnabledModules(t *testing.T) {
	fp := &fakeProvider{
		name: "fake:test",
		quizByModule: map[string]*llm.QuizResponse{
			"Mod A": {
				Title:         "Mod A Check-In",
				PassThreshold: 75,
				MaxAttempts:   2,
				Questions: []llm.QuestionResp{
					{Slug: "q1", Type: "multiple_choice", Title: "Question 1?", Options: []string{"a", "b", "c", "d"}, CorrectAnswer: 2, CorrectText: "c is right because…"},
					{Slug: "q2", Type: "short_answer", Title: "Reflect.", CorrectText: "Any thoughtful response."},
				},
			},
			"Mod C": {
				Title:         "Mod C Quiz",
				PassThreshold: 70,
				MaxAttempts:   3,
				Questions: []llm.QuestionResp{
					{Slug: "q1", Type: "multiple_choice", Title: "C question?", Options: []string{"w", "x", "y", "z"}, CorrectAnswer: 0},
				},
			},
		},
	}
	svc := NewGenerationService()
	svc.SetLLMProvider(fp)

	outline := CourseOutline{
		Title: "Course",
		Modules: []OutlineModule{
			{Title: "Mod A", QuizEnabled: true, Lessons: []OutlineLesson{{Title: "LA1"}, {Title: "LA2"}}},
			{Title: "Mod B", QuizEnabled: false, Lessons: []OutlineLesson{{Title: "LB1"}}}, // no quiz
			{Title: "Mod C", QuizEnabled: true, Lessons: []OutlineLesson{{Title: "LC1"}}},
		},
	}

	quizzes := svc.generateQuizzes(outline, "students", "friendly", "ref", true)

	if atomic.LoadInt32(&fp.quizCalls) != 2 {
		t.Errorf("expected 2 quiz calls (A and C), got %d", fp.quizCalls)
	}
	if _, ok := quizzes[1]; ok {
		t.Error("module B (quiz_enabled=false) should not produce a quiz")
	}
	if got := quizzes[0]; got == nil || got.PassThreshold != 75 || len(got.Questions) != 2 {
		t.Errorf("module A quiz not preserved: %+v", got)
	}
	if got := quizzes[2]; got == nil || got.Questions[0].Options[0] != "w" {
		t.Errorf("module C quiz not preserved: %+v", got)
	}
}

// TestGenerateQuizzes_SkippedWhenQuizzesDisabled short-circuits when the
// user's QuizzesEnabled flag is false, regardless of per-module flags.
func TestGenerateQuizzes_SkippedWhenQuizzesDisabled(t *testing.T) {
	fp := &fakeProvider{name: "fake:test"}
	svc := NewGenerationService()
	svc.SetLLMProvider(fp)

	outline := CourseOutline{
		Modules: []OutlineModule{{Title: "M", QuizEnabled: true, Lessons: []OutlineLesson{{Title: "L"}}}},
	}
	quizzes := svc.generateQuizzes(outline, "", "", "", false)
	if len(quizzes) != 0 {
		t.Errorf("expected no quizzes when quizzesEnabled=false; got %v", quizzes)
	}
	if fp.quizCalls != 0 {
		t.Errorf("expected no provider calls when quizzesEnabled=false; got %d", fp.quizCalls)
	}
}

// TestGenerateQuizzes_NoProvider returns an empty map so the caller will
// apply its per-module fallback (single placeholder question).
func TestGenerateQuizzes_NoProvider(t *testing.T) {
	svc := NewGenerationService()
	svc.SetLLMProvider(nil)
	outline := CourseOutline{
		Modules: []OutlineModule{{Title: "M", QuizEnabled: true, Lessons: []OutlineLesson{{Title: "L"}}}},
	}
	quizzes := svc.generateQuizzes(outline, "", "", "", true)
	if len(quizzes) != 0 {
		t.Errorf("expected empty map when no provider; got %v", quizzes)
	}
}

// TestGenerateQuizzes_ProviderError drops failed quizzes silently.
func TestGenerateQuizzes_ProviderError(t *testing.T) {
	fp := &fakeProvider{name: "fake:test", quizErr: context.Canceled}
	svc := NewGenerationService()
	svc.SetLLMProvider(fp)
	outline := CourseOutline{
		Modules: []OutlineModule{{Title: "M", QuizEnabled: true, Lessons: []OutlineLesson{{Title: "L"}}}},
	}
	quizzes := svc.generateQuizzes(outline, "", "", "", true)
	if len(quizzes) != 0 {
		t.Errorf("expected empty map on provider error; got %v", quizzes)
	}
}

// TestBuildModuleQuiz_UsesLLMResponseWhenPresent verifies that the LMSQuiz
// persisted to the database reflects the LLM's questions and scoring config.
func TestBuildModuleQuiz_UsesLLMResponseWhenPresent(t *testing.T) {
	tenantID := bson.NewObjectId()
	productID := bson.NewObjectId()
	resp := &llm.QuizResponse{
		Title:         "Foundations Check-In",
		PassThreshold: 80,
		MaxAttempts:   2,
		Questions: []llm.QuestionResp{
			{Slug: "q1", Type: "multiple_choice", Title: "Why not time?", Options: []string{"t", "c", "x", "y"}, CorrectAnswer: 1, CorrectText: "Because it's change, not time."},
			{Slug: "q2", Type: "short_answer", Title: "Reflect.", CorrectText: "Any thoughtful response."},
		},
	}

	q := buildModuleQuiz(tenantID, productID, "mod-1", "quiz-1", "Foundations", resp)

	if q.Title != "Foundations Check-In" {
		t.Errorf("title = %q", q.Title)
	}
	if q.PassThreshold != 80 {
		t.Errorf("pass_threshold = %d", q.PassThreshold)
	}
	if q.MaxAttempts != 2 {
		t.Errorf("max_attempts = %d", q.MaxAttempts)
	}
	if len(q.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(q.Questions))
	}
	if q.Questions[0].CorrectAnswer != 1 || q.Questions[0].Options[1] != "c" {
		t.Errorf("first question not preserved: %+v", q.Questions[0])
	}
	if q.Questions[1].Type != "short_answer" {
		t.Errorf("second question type = %q", q.Questions[1].Type)
	}
	if q.Questions[1].Order != 2 {
		t.Errorf("order not 1-indexed: %d", q.Questions[1].Order)
	}
	if !q.Id.Valid() || q.PublicId == "" {
		t.Error("expected identifiers to be populated")
	}
}

// TestBuildModuleQuiz_FallsBackWhenLLMMissing ensures the module still has
// a persisted quiz with a placeholder question when no LLM response is
// available (provider absent, error, or empty response).
func TestBuildModuleQuiz_FallsBackWhenLLMMissing(t *testing.T) {
	tenantID := bson.NewObjectId()
	productID := bson.NewObjectId()

	q := buildModuleQuiz(tenantID, productID, "mod-1", "quiz-1", "Foundations", nil)

	if q.Title != "Foundations Quiz" {
		t.Errorf("default title wrong: %q", q.Title)
	}
	if len(q.Questions) != 1 {
		t.Fatalf("expected 1 placeholder question, got %d", len(q.Questions))
	}
	if q.Questions[0].Slug != "q1" {
		t.Errorf("placeholder slug = %q", q.Questions[0].Slug)
	}
	if q.PassThreshold != 70 || q.MaxAttempts != 3 {
		t.Errorf("expected default scoring (70/3), got %d/%d", q.PassThreshold, q.MaxAttempts)
	}
}

// TestBuildModuleQuiz_InvalidQuestionTypeCoerced defensively rewrites an
// unknown question type to multiple_choice so persisted data stays valid
// even if the LLM returns something odd.
func TestBuildModuleQuiz_InvalidQuestionTypeCoerced(t *testing.T) {
	resp := &llm.QuizResponse{
		Title: "Q", Questions: []llm.QuestionResp{
			{Slug: "q1", Type: "matching", Title: "bogus type", Options: []string{"a", "b"}, CorrectAnswer: 0},
		},
	}
	q := buildModuleQuiz(bson.NewObjectId(), bson.NewObjectId(), "m1", "q-1", "M", resp)
	if q.Questions[0].Type != "multiple_choice" {
		t.Errorf("expected coerced type, got %q", q.Questions[0].Type)
	}
}
