package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Provider abstracts over the underlying LLM vendor. Callers get structured
// outline and content responses without caring which API produced them; the
// provider is responsible for serialising prompts, enforcing JSON output, and
// surfacing vendor-specific errors as plain `error` values.
type Provider interface {
	// Name returns a human-readable identifier such as "openai:gpt-4o" or
	// "anthropic:claude-sonnet-4-6", used for logging and UI affordances.
	Name() string

	// GenerateOutline turns the user's framing plus any reference material
	// into a structured course outline. The reference material is context
	// *to teach from*, never content to copy verbatim.
	GenerateOutline(ctx context.Context, req OutlineRequest) (*OutlineResponse, error)

	// GenerateLessonContent produces lesson markdown for a single lesson,
	// grounded in the reference material. Called once per lesson during
	// MaterializeCourse.
	GenerateLessonContent(ctx context.Context, req LessonContentRequest) (string, error)

	// GenerateQuiz produces a quiz for a module, grounded in the module's
	// lesson titles and any reference material. Called once per quiz-enabled
	// module during MaterializeCourse.
	GenerateQuiz(ctx context.Context, req QuizRequest) (*QuizResponse, error)
}

// OutlineRequest carries everything the provider needs to generate an outline.
// ReferenceText may be long (transcripts, PDFs). Providers are responsible for
// truncating to fit the model's context window if necessary.
type OutlineRequest struct {
	Prompt         string
	Audience       string
	Outcome        string
	Tone           string
	ModuleCount    int
	QuizzesEnabled bool
	CertEnabled    bool
	DefaultMedia   string
	ExtraContext   string
	ReferenceText  string
}

// LessonContentRequest asks the provider to author lesson markdown for one
// specific lesson within the generated outline, grounded in shared reference
// material.
type LessonContentRequest struct {
	CourseTitle   string
	ModuleTitle   string
	LessonTitle   string
	LessonBrief   string // The outline-stage description/summary for this lesson.
	Audience      string
	Tone          string
	ReferenceText string
}

// QuizRequest asks the provider to author a module-level quiz. The lesson
// titles anchor the quiz to what the student has just learned; the reference
// material grounds the question content.
type QuizRequest struct {
	CourseTitle   string
	ModuleTitle   string
	LessonTitles  []string
	Audience      string
	Tone          string
	ReferenceText string
	QuestionCount int // target; defaults to 4 when zero
}

// NewFromEnv returns a Provider based on the LLM_PROVIDER env var, which may be
// "openai", "anthropic", or empty/"none". If no provider is configured, (nil, nil)
// is returned so callers can fall back to a deterministic stub without treating
// it as an error.
func NewFromEnv() (Provider, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))

	// If no explicit provider is set, infer from which key is present — this
	// lets existing docker-compose files that only define OPENAI_API_KEY work
	// out of the box.
	if provider == "" {
		switch {
		case os.Getenv("ANTHROPIC_API_KEY") != "":
			provider = "anthropic"
		case os.Getenv("OPENAI_API_KEY") != "":
			provider = "openai"
		default:
			return nil, nil
		}
	}

	switch provider {
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required when LLM_PROVIDER=openai")
		}
		model := os.Getenv("OPENAI_MODEL")
		if model == "" {
			model = "gpt-4o"
		}
		return &openAIProvider{apiKey: key, model: model, httpClient: defaultHTTPClient()}, nil
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required when LLM_PROVIDER=anthropic")
		}
		model := os.Getenv("ANTHROPIC_MODEL")
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		return &anthropicProvider{apiKey: key, model: model, httpClient: defaultHTTPClient()}, nil
	case "none", "disabled", "off":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q (supported: openai, anthropic, none)", provider)
	}
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

// ---------- OpenAI ----------

type openAIProvider struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func (p *openAIProvider) Name() string { return "openai:" + p.model }

func (p *openAIProvider) GenerateOutline(ctx context.Context, req OutlineRequest) (*OutlineResponse, error) {
	userMsg := renderOutlineUserPrompt(req)
	raw, err := p.chatJSON(ctx, OutlineSystemPrompt, userMsg)
	if err != nil {
		return nil, err
	}
	var out OutlineResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("openai outline: invalid JSON: %w\nraw: %s", err, raw)
	}
	return &out, nil
}

func (p *openAIProvider) GenerateLessonContent(ctx context.Context, req LessonContentRequest) (string, error) {
	userMsg := renderLessonContentUserPrompt(req)
	// Lesson content is plain markdown — force JSON with a single "markdown"
	// field so we can extract reliably without stripping fences.
	raw, err := p.chatJSON(ctx, LessonContentSystemPrompt, userMsg)
	if err != nil {
		return "", err
	}
	var wrap ContentResponse
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		return "", fmt.Errorf("openai lesson content: invalid JSON: %w\nraw: %s", err, raw)
	}
	return strings.TrimSpace(wrap.Markdown), nil
}

func (p *openAIProvider) GenerateQuiz(ctx context.Context, req QuizRequest) (*QuizResponse, error) {
	userMsg := renderQuizUserPrompt(req)
	raw, err := p.chatJSON(ctx, QuizSystemPrompt, userMsg)
	if err != nil {
		return nil, err
	}
	var out QuizResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("openai quiz: invalid JSON: %w\nraw: %s", err, raw)
	}
	return &out, nil
}

func (p *openAIProvider) chatJSON(ctx context.Context, systemMsg, userMsg string) (string, error) {
	body := map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemMsg},
			{"role": "user", "content": userMsg},
		},
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0.4,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai status %d: %s", resp.StatusCode, truncateForError(string(respBody)))
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("openai parse response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices in response")
	}
	return apiResp.Choices[0].Message.Content, nil
}

// ---------- Anthropic (Claude) ----------

type anthropicProvider struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func (p *anthropicProvider) Name() string { return "anthropic:" + p.model }

func (p *anthropicProvider) GenerateOutline(ctx context.Context, req OutlineRequest) (*OutlineResponse, error) {
	userMsg := renderOutlineUserPrompt(req)
	raw, err := p.messages(ctx, OutlineSystemPrompt, userMsg)
	if err != nil {
		return nil, err
	}
	raw = extractJSONObject(raw)
	var out OutlineResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("anthropic outline: invalid JSON: %w\nraw: %s", err, raw)
	}
	return &out, nil
}

func (p *anthropicProvider) GenerateLessonContent(ctx context.Context, req LessonContentRequest) (string, error) {
	userMsg := renderLessonContentUserPrompt(req)
	raw, err := p.messages(ctx, LessonContentSystemPrompt, userMsg)
	if err != nil {
		return "", err
	}
	raw = extractJSONObject(raw)
	var wrap ContentResponse
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		return "", fmt.Errorf("anthropic lesson content: invalid JSON: %w\nraw: %s", err, raw)
	}
	return strings.TrimSpace(wrap.Markdown), nil
}

func (p *anthropicProvider) GenerateQuiz(ctx context.Context, req QuizRequest) (*QuizResponse, error) {
	userMsg := renderQuizUserPrompt(req)
	raw, err := p.messages(ctx, QuizSystemPrompt, userMsg)
	if err != nil {
		return nil, err
	}
	raw = extractJSONObject(raw)
	var out QuizResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("anthropic quiz: invalid JSON: %w\nraw: %s", err, raw)
	}
	return &out, nil
}

func (p *anthropicProvider) messages(ctx context.Context, systemMsg, userMsg string) (string, error) {
	body := map[string]any{
		"model":      p.model,
		"system":     systemMsg,
		"max_tokens": 8000,
		"messages": []map[string]string{
			{"role": "user", "content": userMsg},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("anthropic read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic status %d: %s", resp.StatusCode, truncateForError(string(respBody)))
	}

	var apiResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("anthropic parse response: %w", err)
	}
	var sb strings.Builder
	for _, c := range apiResp.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

// ---------- shared helpers ----------

// extractJSONObject pulls the first top-level `{...}` span out of a response.
// Claude sometimes wraps JSON in prose or a markdown code fence; OpenAI's
// json_object mode doesn't, but we run this defensively anyway.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	// Strip a leading ```json\n ... \n``` fence.
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	// Trim to the first balanced braces.
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

func truncateForError(s string) string {
	if len(s) > 500 {
		return s[:500] + "…"
	}
	return s
}
