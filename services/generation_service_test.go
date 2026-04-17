package services

import (
	"strings"
	"testing"
)

// TestBuildDeterministicOutline_UsesPromptTopic asserts that the outline title
// and module titles reflect the user's prompt rather than a generic
// "Module 1 / Lesson 1.1" template.
func TestBuildDeterministicOutline_UsesPromptTopic(t *testing.T) {
	outline := buildDeterministicOutline(
		"Make a course on the Bridge of Events, teaching people how to master manifesting by analyzing their manifestations.",
		"",
		"",
		3,
		true,
		"stub",
		"",
	)

	if !strings.Contains(outline.Title, "Bridge of Events") {
		t.Errorf("outline.Title = %q, want it to contain \"Bridge of Events\"", outline.Title)
	}
	if len(outline.Modules) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(outline.Modules))
	}

	// Every module title should reference the topic; hard-coded placeholders
	// like "Module 1" must no longer appear.
	for i, m := range outline.Modules {
		if !strings.Contains(m.Title, "Bridge of Events") {
			t.Errorf("module %d title = %q, expected it to reference the topic", i+1, m.Title)
		}
		if m.Title == "Module 1" || m.Title == "Module 2" || m.Title == "Module 3" {
			t.Errorf("module %d uses placeholder title %q", i+1, m.Title)
		}
	}
}

// TestBuildDeterministicOutline_UsesReferenceText asserts that uploaded
// reference content is chunked into lessons so the generated course actually
// carries the user's material forward instead of discarding it.
func TestBuildDeterministicOutline_UsesReferenceText(t *testing.T) {
	refText := strings.Join([]string{
		"Chapter 1: Foundations\nThe Bridge of Events begins with awareness.",
		"Chapter 2: Observation\nEach manifestation carries a trace of its origin.",
		"Chapter 3: Integration\nMastery is the practice of noticing what bridges appear.",
		"Chapter 4: Practice\nApply the framework daily.",
		"Chapter 5: Reflection\nReview the journal weekly.",
		"Chapter 6: Sharing\nTeach what you have learned.",
	}, "\n\n")

	outline := buildDeterministicOutline(
		"Make a course on the Bridge of Events",
		"aspiring manifesters",
		"master manifestation analysis",
		2,
		false,
		"none",
		refText,
	)

	// Concatenate all lesson descriptions; the reference text should be
	// distributed across them.
	var all strings.Builder
	for _, m := range outline.Modules {
		for _, l := range m.Lessons {
			all.WriteString(l.Description)
			all.WriteString("\n")
		}
	}
	joined := all.String()

	for _, want := range []string{"Foundations", "Observation", "Integration"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected reference chunk containing %q to appear in a lesson description, got:\n%s", want, joined)
		}
	}

	// Description field should reflect audience/outcome framing, not the
	// old "A course for %s to %s" template with empty inputs.
	if !strings.Contains(outline.Description, "aspiring manifesters") {
		t.Errorf("outline.Description = %q, expected to reference audience", outline.Description)
	}
}

// TestExtractTopic handles a few realistic phrasings so the prompt→topic
// extraction stays robust against cosmetic punctuation.
func TestExtractTopic(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Make a course on the Bridge of Events", "the Bridge of Events"},
		{"Create a course about quantum gardening", "quantum gardening"},
		{"Build a curriculum covering Go concurrency", "Go concurrency"},
		{"Please generate a course on Stoic journaling, aimed at beginners.", "Stoic journaling"},
		{"Teach me something", "Teach me something"}, // no imperative match — pass through
	}
	for _, tc := range cases {
		got := extractTopic(tc.in)
		if got != tc.want {
			t.Errorf("extractTopic(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestChunkReferenceText(t *testing.T) {
	chunks := chunkReferenceText("Para one.\n\nPara two is longer.\n\nPara three.")
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d: %v", len(chunks), chunks)
	}

	// Very long single paragraph should be subdivided.
	long := strings.Repeat("Sentence one. ", 200)
	subs := chunkReferenceText(long)
	if len(subs) < 2 {
		t.Errorf("expected long paragraph to subdivide, got %d chunks", len(subs))
	}
}

// TestDeriveLessonTitle_LongProse ensures that prose paragraphs — where the
// first line is a long sentence, not a short heading — still produce a
// readable lesson title instead of falling back to "Lesson N.N".
func TestDeriveLessonTitle_LongProse(t *testing.T) {
	chunk := "Manifestation is the practice of noticing and naming what we bridge between inner vision and outer events, and it takes time to master."
	title := deriveLessonTitle(chunk, "Lesson 1.1")
	if title == "Lesson 1.1" {
		t.Error("expected a prose-derived title, got fallback")
	}
	if len([]rune(title)) > 80 {
		t.Errorf("title too long (%d runes): %q", len([]rune(title)), title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Errorf("expected truncation ellipsis, got %q", title)
	}
	if !strings.Contains(strings.ToLower(title), "manifestation") {
		t.Errorf("title should reflect chunk content, got %q", title)
	}
}

// TestDeriveLessonTitle_MarkdownHeading ensures leading markdown headings are
// preferred over a truncated sentence.
func TestDeriveLessonTitle_MarkdownHeading(t *testing.T) {
	chunk := "## Chapter Three: Observation\n\nThe second stage is observation, and it precedes analysis."
	title := deriveLessonTitle(chunk, "fallback")
	if title != "Chapter Three: Observation" {
		t.Errorf("expected markdown heading extraction, got %q", title)
	}
}

// TestDeriveLessonTitle_ShortFirstSentence returns short opening sentences
// verbatim (no ellipsis) so punchy titles survive intact.
func TestDeriveLessonTitle_ShortFirstSentence(t *testing.T) {
	title := deriveLessonTitle("In other words, what happened three days ago.", "fallback")
	if title != "In other words, what happened three days ago." {
		t.Errorf("expected full sentence title, got %q", title)
	}
}

// TestBuildDeterministicOutline_NoEmptyLessons asserts the outline never
// contains lessons with blank titles or descriptions when chunks are present:
// every generated lesson must carry real content.
func TestBuildDeterministicOutline_NoEmptyLessons(t *testing.T) {
	refText := strings.Join([]string{
		"Manifestation starts with awareness of intention. Attention shapes attention.",
		"Observation means watching without judging the process of your own desires.",
		"Integration is where daily practice becomes identity.",
		"Reflection at the end of the day surfaces the patterns you missed in the moment.",
		"Sharing what you've learned reinforces the neural pathways of your practice.",
	}, "\n\n")
	outline := buildDeterministicOutline("Make a course on manifestation", "", "", 4, false, "none", refText)

	seenPlaceholder := false
	seenEmpty := false
	for _, m := range outline.Modules {
		for _, l := range m.Lessons {
			if strings.HasPrefix(l.Title, "Lesson ") && strings.Count(l.Title, ".") == 1 {
				seenPlaceholder = true
			}
			if strings.TrimSpace(l.Description) == "" {
				seenEmpty = true
			}
		}
	}
	if seenPlaceholder {
		t.Error("expected no placeholder \"Lesson N.N\" titles when chunks are present")
	}
	if seenEmpty {
		t.Error("expected no lessons with empty descriptions when chunks are present")
	}
}

// TestBuildDeterministicOutline_CompressesToChunkCount ensures that when the
// user requests more modules than we have material, we produce fewer modules
// rather than padding with empty ones.
func TestBuildDeterministicOutline_CompressesToChunkCount(t *testing.T) {
	refText := "Only one chunk here.\n\nAnother chunk."
	outline := buildDeterministicOutline("course on foo", "", "", 5, false, "none", refText)
	if len(outline.Modules) > 2 {
		t.Errorf("expected at most 2 modules for 2 chunks, got %d", len(outline.Modules))
	}
	total := 0
	for _, m := range outline.Modules {
		total += len(m.Lessons)
	}
	if total != 2 {
		t.Errorf("expected 2 total lessons, got %d", total)
	}
}

// TestBuildDeterministicOutline_MarkdownHeadingsDriveModules verifies that
// when the reference text carries its own top-level structure, we adopt it
// for module titles instead of overriding with generic templates.
func TestBuildDeterministicOutline_MarkdownHeadingsDriveModules(t *testing.T) {
	refText := "# Awareness\n\nThe first stage is awareness of what you want.\n\n# Observation\n\nThe second stage is observing how desire moves.\n\n# Practice\n\nThe third stage is the daily practice of the bridge."
	outline := buildDeterministicOutline("Course on the Bridge", "", "", 3, false, "none", refText)
	if len(outline.Modules) < 2 {
		t.Fatalf("expected at least 2 modules, got %d", len(outline.Modules))
	}
	titles := make([]string, 0, len(outline.Modules))
	for _, m := range outline.Modules {
		titles = append(titles, m.Title)
	}
	joined := strings.Join(titles, "|")
	for _, want := range []string{"Awareness", "Observation"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected module title containing %q, got %v", want, titles)
		}
	}
}
