package llm

import (
	"fmt"
	"strings"
)

// OutlineSystemPrompt instructs the LLM to treat reference material as SOURCE
// CONTEXT — transcripts, PDF dumps, notes, outlines — and to DESIGN a course
// that teaches the subject matter the references are about, not to copy the
// references verbatim.
const OutlineSystemPrompt = `You are an expert curriculum designer.

You will be given:
- a user prompt describing the course they want to build,
- optional audience / outcome / tone framing,
- optional REFERENCE MATERIAL that is CONTEXT ONLY (transcripts, pasted notes, outlines, PDF extracts, rough drafts — often messy, with timestamps or speaker labels).

Your job is to DESIGN a coherent course that teaches the subject the reference material is about. You are NOT transcribing, summarising verbatim, or copy-pasting the reference. Extract concepts, restructure them into a logical teaching progression, write teacher-voice lesson titles and descriptions, and decide a sensible module/lesson count based on the material.

HARD RULES:
1. Never reproduce transcripts verbatim. Never include speaker tags like "Speaker 0 [00:01]:", timestamps, filler words, stage directions, or meta commentary from the source. Rewrite everything in a teaching voice.
2. Lesson titles must describe what the lesson TEACHES (e.g. "Why the Bridge of Events Is Not About Time"), not fragments of a sentence from the source.
3. Lesson descriptions (2–4 sentences) summarise what the student will learn and the key points to be covered, written in plain instructional prose.
4. Module titles announce a teaching phase (e.g. "Foundations", "Common Misconceptions", "Applying the Framework"), optionally with the subject ("Foundations of X"). Avoid "Module 1".
5. Pick a module count that suits the material. If the user suggests a count, respect it within one or two modules; never leave a module empty.
6. Aim for 3–6 lessons per module. Distribute concepts so every lesson has a clear, distinct teaching focus.
7. Output ONLY a valid JSON object matching the schema below. No prose, no markdown fences, no preamble.

JSON schema:
{
  "title":       string,  // concise course title in the subject's vocabulary
  "description": string,  // 1–2 sentences describing what the course teaches
  "modules": [
    {
      "title":        string,
      "order":        int,   // 1-indexed
      "quiz_enabled": bool,  // honour the user's QuizzesEnabled flag
      "lessons": [
        {
          "title":       string,
          "order":       int,   // 1-indexed within the module
          "video_mode":  string,  // honour the user's DefaultMedia flag
          "description": string   // 2–4 sentence teacher-voice summary
        }
      ]
    }
  ]
}`

// LessonContentSystemPrompt instructs the LLM to author a single lesson in
// clean teaching markdown, grounded in the reference material but never
// copying it.
const LessonContentSystemPrompt = `You are an expert instructor writing a single lesson for an online course.

Inputs describe ONE lesson within a larger course: the lesson's title and brief, the surrounding module/course context, and optional REFERENCE MATERIAL that is CONTEXT ONLY (transcripts, notes, etc.).

Write the lesson's content in clean, teachable markdown. Use short paragraphs, subheadings (##), bullet lists, and concrete examples. Assume the student is an adult learner with the stated audience/tone framing.

HARD RULES:
1. Never reproduce transcripts, speaker tags, timestamps, or filler from the reference material. Rewrite in a teaching voice.
2. Do not include the lesson title as an H1 inside the markdown — it is rendered separately by the course builder.
3. Do not include "Module 1: …" or other structural references to the rest of the course.
4. 250–600 words per lesson is typical. Stay focused on THIS lesson's scope.
5. Output ONLY JSON: {"markdown": "…"}. No prose outside the JSON, no markdown fences around the JSON.`

// renderOutlineUserPrompt builds the user-side message for an outline request.
// It includes the user's framing and the (potentially very long) reference
// material, clearly labelled so the model doesn't confuse it with instructions.
func renderOutlineUserPrompt(req OutlineRequest) string {
	var sb strings.Builder

	sb.WriteString("# Course request\n\n")
	if s := strings.TrimSpace(req.Prompt); s != "" {
		fmt.Fprintf(&sb, "User prompt: %s\n", s)
	}
	if s := strings.TrimSpace(req.Audience); s != "" {
		fmt.Fprintf(&sb, "Audience: %s\n", s)
	}
	if s := strings.TrimSpace(req.Outcome); s != "" {
		fmt.Fprintf(&sb, "Outcome: %s\n", s)
	}
	if s := strings.TrimSpace(req.Tone); s != "" {
		fmt.Fprintf(&sb, "Tone: %s\n", s)
	}
	moduleCount := req.ModuleCount
	if moduleCount <= 0 {
		moduleCount = 4
	}
	fmt.Fprintf(&sb, "Preferred module count: %d (approximate — adjust if material suggests otherwise)\n", moduleCount)
	fmt.Fprintf(&sb, "QuizzesEnabled: %v\n", req.QuizzesEnabled)
	fmt.Fprintf(&sb, "CertEnabled: %v\n", req.CertEnabled)
	if s := strings.TrimSpace(req.DefaultMedia); s != "" {
		fmt.Fprintf(&sb, "DefaultMedia: %s (use this as each lesson's video_mode)\n", s)
	}
	if s := strings.TrimSpace(req.ExtraContext); s != "" {
		fmt.Fprintf(&sb, "Extra context from the author: %s\n", s)
	}

	ref := strings.TrimSpace(req.ReferenceText)
	if ref != "" {
		sb.WriteString("\n# Reference material (CONTEXT ONLY — do not copy verbatim)\n\n")
		// Cap to keep us safely inside smaller models' context windows.
		// Most commercial models handle ≥ 128k tokens, so 60k characters
		// (~15k tokens) is conservative and keeps room for output.
		if len(ref) > 60000 {
			ref = ref[:60000] + "\n\n[… reference truncated for length …]"
		}
		sb.WriteString(ref)
		sb.WriteString("\n")
	}

	sb.WriteString("\nGenerate the JSON outline now.")
	return sb.String()
}

// QuizSystemPrompt instructs the LLM to write a module-level quiz whose
// questions test comprehension of THIS module's lessons, grounded in — but
// not copied from — the reference material.
const QuizSystemPrompt = `You are an expert assessment designer for online courses.

You will be given:
- the course title and module title,
- the ordered list of lesson titles within this module,
- optional audience / tone framing,
- optional REFERENCE MATERIAL that is CONTEXT ONLY (transcripts, notes, etc.).

Your job is to design a short quiz that tests the student's comprehension of THIS module's lessons. Questions should be pedagogically useful — not trivia, not gotcha, not verbatim transcript extraction.

HARD RULES:
1. Never reproduce transcripts, speaker tags, timestamps, or filler from the reference material. Write all question text in plain teacher voice.
2. Aim for 3–6 questions per quiz. Default to 4 if the user did not specify.
3. Prefer multiple_choice questions with exactly 4 plausible options. Include at most one short_answer question per quiz for open reflection.
4. For multiple_choice: provide 4 options, set correct_answer to the 0-based index of the correct option, and write a 1–3 sentence correct_text explaining WHY that answer is correct (shown to the student after they attempt).
5. For short_answer: leave options empty, set correct_answer to 0, and put the model answer or acceptable answer range in correct_text.
6. Each question's slug must be a stable short id like "q1", "q2", "q3". Order must be 1-indexed and match the slug number.
7. Set pass_threshold to 70 and max_attempts to 3 unless the audience clearly calls for different values.
8. Output ONLY a valid JSON object matching the schema below. No prose, no markdown fences, no preamble.

JSON schema:
{
  "title":          string,  // short quiz title like "Foundations Check-In" or "<Module Title> Quiz"
  "pass_threshold": int,     // percentage 0–100
  "max_attempts":   int,
  "questions": [
    {
      "slug":           string,   // "q1", "q2", ...
      "type":           string,   // "multiple_choice" or "short_answer"
      "title":          string,   // the question stem
      "options":        [string], // 4 options for multiple_choice; empty for short_answer
      "correct_answer": int,      // 0-based index of the correct option (0 for short_answer)
      "correct_text":   string,   // short explanation or model answer
      "order":          int       // 1-indexed
    }
  ]
}`

// renderQuizUserPrompt builds the user-side message for a quiz generation
// request.
func renderQuizUserPrompt(req QuizRequest) string {
	var sb strings.Builder

	sb.WriteString("# Quiz to design\n\n")
	fmt.Fprintf(&sb, "Course: %s\n", req.CourseTitle)
	fmt.Fprintf(&sb, "Module: %s\n", req.ModuleTitle)
	sb.WriteString("Lessons in this module:\n")
	if len(req.LessonTitles) == 0 {
		sb.WriteString("  (no lessons — base questions on the module topic)\n")
	} else {
		for i, t := range req.LessonTitles {
			fmt.Fprintf(&sb, "  %d. %s\n", i+1, t)
		}
	}
	if s := strings.TrimSpace(req.Audience); s != "" {
		fmt.Fprintf(&sb, "Audience: %s\n", s)
	}
	if s := strings.TrimSpace(req.Tone); s != "" {
		fmt.Fprintf(&sb, "Tone: %s\n", s)
	}
	questionCount := req.QuestionCount
	if questionCount <= 0 {
		questionCount = 4
	}
	fmt.Fprintf(&sb, "Target question count: %d\n", questionCount)

	ref := strings.TrimSpace(req.ReferenceText)
	if ref != "" {
		sb.WriteString("\n# Reference material (CONTEXT ONLY — do not copy verbatim)\n\n")
		if len(ref) > 30000 {
			ref = ref[:30000] + "\n\n[… reference truncated for length …]"
		}
		sb.WriteString(ref)
		sb.WriteString("\n")
	}

	sb.WriteString("\nWrite the quiz JSON now.")
	return sb.String()
}

// renderLessonContentUserPrompt builds the user-side message for a per-lesson
// content request.
func renderLessonContentUserPrompt(req LessonContentRequest) string {
	var sb strings.Builder

	sb.WriteString("# Lesson to write\n\n")
	fmt.Fprintf(&sb, "Course: %s\n", req.CourseTitle)
	fmt.Fprintf(&sb, "Module: %s\n", req.ModuleTitle)
	fmt.Fprintf(&sb, "Lesson title: %s\n", req.LessonTitle)
	if s := strings.TrimSpace(req.LessonBrief); s != "" {
		fmt.Fprintf(&sb, "Lesson brief from outline: %s\n", s)
	}
	if s := strings.TrimSpace(req.Audience); s != "" {
		fmt.Fprintf(&sb, "Audience: %s\n", s)
	}
	if s := strings.TrimSpace(req.Tone); s != "" {
		fmt.Fprintf(&sb, "Tone: %s\n", s)
	}

	ref := strings.TrimSpace(req.ReferenceText)
	if ref != "" {
		sb.WriteString("\n# Reference material (CONTEXT ONLY — do not copy verbatim)\n\n")
		if len(ref) > 40000 {
			ref = ref[:40000] + "\n\n[… reference truncated for length …]"
		}
		sb.WriteString(ref)
		sb.WriteString("\n")
	}

	sb.WriteString("\nWrite the lesson markdown now, returning JSON of the form {\"markdown\": \"…\"}.")
	return sb.String()
}
