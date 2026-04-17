package llm

// Contracts defines the typed response structures expected from LLM calls
// during course generation and editing.

// OutlineResponse is the expected structure from outline generation.
type OutlineResponse struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Modules     []OutlineModuleResp  `json:"modules"`
}

// OutlineModuleResp is a module within an outline response.
type OutlineModuleResp struct {
	Title       string               `json:"title"`
	Order       int                  `json:"order"`
	Lessons     []OutlineLessonResp  `json:"lessons"`
	QuizEnabled bool                 `json:"quiz_enabled,omitempty"`
}

// OutlineLessonResp is a lesson within an outline module response.
type OutlineLessonResp struct {
	Title       string `json:"title"`
	Order       int    `json:"order"`
	VideoMode   string `json:"video_mode,omitempty"`
	Description string `json:"description,omitempty"`
}

// ContentResponse is the expected structure from content generation.
type ContentResponse struct {
	Markdown string `json:"markdown"`
}

// PatchResponse is the expected structure from edit prompt generation.
type PatchResponse struct {
	Operations []PatchOpResp `json:"operations"`
}

// PatchOpResp is a single patch operation returned from the LLM.
type PatchOpResp struct {
	Op    string      `json:"op"`    // replace, add, remove, reorder
	Path  string      `json:"path"`  // JSON path
	Value interface{} `json:"value,omitempty"`
}

// QuizResponse is the expected structure from quiz generation.
type QuizResponse struct {
	Title         string             `json:"title"`
	PassThreshold int                `json:"pass_threshold"`
	MaxAttempts   int                `json:"max_attempts"`
	Questions     []QuestionResp     `json:"questions"`
}

// QuestionResp is a question within a quiz response.
type QuestionResp struct {
	Slug          string   `json:"slug"`
	Type          string   `json:"type"`
	Title         string   `json:"title"`
	Options       []string `json:"options,omitempty"`
	CorrectAnswer int      `json:"correct_answer,omitempty"`
	CorrectText   string   `json:"correct_text,omitempty"`
}

// CertConfigResponse is the expected structure from certificate config generation.
type CertConfigResponse struct {
	Title        string `json:"title"`
	TemplateName string `json:"template_name"`
	Enabled      bool   `json:"enabled"`
}
