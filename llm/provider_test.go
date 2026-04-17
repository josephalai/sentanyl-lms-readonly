package llm

import (
	"strings"
	"testing"
)

func TestNewFromEnv_NoKeysReturnsNil(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	p, err := NewFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil provider when no keys are set, got %s", p.Name())
	}
}

func TestNewFromEnv_ExplicitNone(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "none")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	p, err := NewFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil provider when LLM_PROVIDER=none, got %s", p.Name())
	}
}

func TestNewFromEnv_AutoDetectsOpenAI(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	p, err := NewFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || !strings.HasPrefix(p.Name(), "openai:") {
		t.Errorf("expected openai provider, got %v", p)
	}
	if !strings.HasSuffix(p.Name(), "gpt-4o") {
		t.Errorf("expected default model gpt-4o, got %q", p.Name())
	}
}

func TestNewFromEnv_AnthropicPreferredWhenBothKeys(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("ANTHROPIC_API_KEY", "sk-anthropic")
	p, err := NewFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || !strings.HasPrefix(p.Name(), "anthropic:") {
		t.Errorf("expected anthropic when both keys are set, got %v", p)
	}
}

func TestNewFromEnv_ExplicitOpenAIWithoutKey(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error when LLM_PROVIDER=openai but no key")
	}
}

func TestNewFromEnv_UnknownProvider(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "ollama")
	_, err := NewFromEnv()
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown-provider error, got %v", err)
	}
}

func TestExtractJSONObject_RawJSON(t *testing.T) {
	in := `{"title":"T","description":"D","modules":[]}`
	got := extractJSONObject(in)
	if got != in {
		t.Errorf("raw JSON should pass through, got %q", got)
	}
}

func TestExtractJSONObject_MarkdownFenced(t *testing.T) {
	in := "```json\n{\"title\":\"T\"}\n```"
	got := extractJSONObject(in)
	if got != `{"title":"T"}` {
		t.Errorf("fenced JSON not extracted cleanly: %q", got)
	}
}

func TestExtractJSONObject_JSONWithPrefix(t *testing.T) {
	in := "Here is the outline:\n{\"title\":\"T\",\"modules\":[{\"title\":\"M\"}]}"
	got := extractJSONObject(in)
	if got != `{"title":"T","modules":[{"title":"M"}]}` {
		t.Errorf("prose-prefixed JSON not extracted cleanly: %q", got)
	}
}

func TestRenderOutlineUserPrompt_IncludesReferenceLabel(t *testing.T) {
	msg := renderOutlineUserPrompt(OutlineRequest{
		Prompt:        "Course on manifestation",
		ReferenceText: "Speaker 0 [00:00]: The first thing to know is…",
	})
	if !strings.Contains(msg, "CONTEXT ONLY") {
		t.Errorf("user prompt missing reference labeling: %s", msg)
	}
	if !strings.Contains(msg, "Speaker 0 [00:00]") {
		t.Errorf("user prompt missing reference body")
	}
}

func TestRenderOutlineUserPrompt_TruncatesLongReference(t *testing.T) {
	long := strings.Repeat("word ", 20000) // 100k chars
	msg := renderOutlineUserPrompt(OutlineRequest{Prompt: "x", ReferenceText: long})
	if !strings.Contains(msg, "reference truncated") {
		t.Errorf("very long reference should be truncated with a marker")
	}
	if len(msg) > 80000 {
		t.Errorf("prompt too long (%d chars) after truncation", len(msg))
	}
}

func TestRenderQuizUserPrompt_IncludesLessonTitles(t *testing.T) {
	msg := renderQuizUserPrompt(QuizRequest{
		CourseTitle:  "Bridge of Events",
		ModuleTitle:  "Foundations",
		LessonTitles: []string{"Why Not Time", "The Guarantee Principle"},
		Audience:     "manifesters",
	})
	if !strings.Contains(msg, "Foundations") {
		t.Errorf("missing module title in quiz prompt")
	}
	if !strings.Contains(msg, "Why Not Time") || !strings.Contains(msg, "The Guarantee Principle") {
		t.Errorf("missing lesson titles in quiz prompt:\n%s", msg)
	}
	if !strings.Contains(msg, "Target question count: 4") {
		t.Errorf("expected default question count of 4; got:\n%s", msg)
	}
}

func TestRenderQuizUserPrompt_RespectsCustomQuestionCount(t *testing.T) {
	msg := renderQuizUserPrompt(QuizRequest{ModuleTitle: "M", QuestionCount: 6})
	if !strings.Contains(msg, "Target question count: 6") {
		t.Errorf("expected question count 6, got:\n%s", msg)
	}
}
