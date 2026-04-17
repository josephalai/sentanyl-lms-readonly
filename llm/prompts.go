package llm

// PromptTemplates contains central prompt templates for LMS course generation
// and editing. In production, these are sent to an LLM with structured output
// instructions. The templates define the expected behavior for each generation stage.

// OutlinePromptTemplate is the system prompt for outline generation.
const OutlinePromptTemplate = `You are a course design expert. Given the following inputs, generate a structured course outline.

Inputs:
- Course goal/prompt: {{.Prompt}}
- Target audience: {{.Audience}}
- Desired outcome: {{.Outcome}}
- Tone: {{.Tone}}
- Module count: {{.ModuleCount}} (approximate)
- Include quizzes: {{.QuizzesEnabled}}
- Include certificate: {{.CertEnabled}}
- Default media mode: {{.DefaultMedia}}
- Extra context: {{.ExtraContext}}
- Reference material: {{.ReferenceText}}

Output a JSON object matching the CourseOutline schema with title, description, and modules.
Each module should have title, order, lessons (each with title, order, video_mode, description),
and quiz_enabled flag.`

// ContentPromptTemplate is the system prompt for lesson content generation.
const ContentPromptTemplate = `Generate lesson content for the following lesson in markdown format.

Lesson title: {{.Title}}
Module context: {{.ModuleTitle}}
Course context: {{.CourseTitle}}
Audience: {{.Audience}}
Tone: {{.Tone}}
Reference material: {{.ReferenceText}}

Output well-structured markdown content suitable for an online course lesson.`

// EditPromptTemplate is the system prompt for patch-based editing.
const EditPromptTemplate = `You are editing an existing course tree. Given the current state and the user's edit instruction, produce a set of JSON Patch operations.

Current state:
{{.CurrentState}}

User instruction: {{.Prompt}}
Target type: {{.TargetType}}
Target ID: {{.TargetId}}
Scope: {{.Scope}}

Output a JSON array of patch operations with "op" (replace/add/remove), "path", and "value" fields.
Never overwrite the entire tree. Make minimal, targeted changes.`

// QuizPromptTemplate is the system prompt for quiz generation.
const QuizPromptTemplate = `Generate a quiz for the following module.

Module title: {{.ModuleTitle}}
Lesson titles: {{.LessonTitles}}
Audience: {{.Audience}}
Tone: {{.Tone}}

Output a JSON object with title, pass_threshold (default 70), max_attempts (default 3),
and questions array. Each question has slug, type ("multiple_choice" or "short_answer"),
title, options (for multiple choice), and correct_answer (index) or correct_text.`

// CertConfigPromptTemplate is the prompt for certificate configuration.
const CertConfigPromptTemplate = `Generate certificate configuration for the following course.

Course title: {{.CourseTitle}}
Course description: {{.Description}}
Tone: {{.Tone}}

Output a JSON object with title, template_name, and enabled (boolean).`
