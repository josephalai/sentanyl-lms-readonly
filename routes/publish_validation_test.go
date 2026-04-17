package routes

import (
	"strings"
	"testing"

	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

func TestValidatePublishCourse_ValidCourse(t *testing.T) {
	modules := []*pkgmodels.CourseModule{
		{
			Slug: "m1", Title: "Module 1", Order: 1,
			Lessons: []*pkgmodels.CourseLesson{
				{Slug: "l1", Title: "Lesson 1", Order: 1, ContentMarkdown: "hello"},
			},
		},
	}
	errs := validatePublishCourse("My Course", modules)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidatePublishCourse_EmptyCourse(t *testing.T) {
	errs := validatePublishCourse("", nil)
	if len(errs) == 0 {
		t.Fatal("expected errors for empty course")
	}
	joined := strings.Join(errs, "|")
	if !strings.Contains(joined, "title is required") {
		t.Errorf("missing title error: %v", errs)
	}
	if !strings.Contains(joined, "at least one module is required") {
		t.Errorf("missing modules error: %v", errs)
	}
}

func TestValidatePublishCourse_ModuleWithNoLessons(t *testing.T) {
	modules := []*pkgmodels.CourseModule{
		{Slug: "m1", Title: "Module 1", Order: 1},
	}
	errs := validatePublishCourse("Course", modules)
	if len(errs) == 0 {
		t.Fatal("expected errors for module with no lessons")
	}
	joined := strings.Join(errs, "|")
	if !strings.Contains(joined, "at least one lesson is required") {
		t.Errorf("expected at-least-one-lesson error, got %v", errs)
	}
}

func TestValidatePublishCourse_LessonMissingContent(t *testing.T) {
	modules := []*pkgmodels.CourseModule{
		{
			Slug: "m1", Title: "Module 1", Order: 1,
			Lessons: []*pkgmodels.CourseLesson{
				{Slug: "l1", Title: "Lesson 1", Order: 1},
			},
		},
	}
	errs := validatePublishCourse("Course", modules)
	if len(errs) == 0 {
		t.Fatal("expected error for lesson with no content")
	}
	joined := strings.Join(errs, "|")
	if !strings.Contains(joined, "must have content_markdown") {
		t.Errorf("expected missing-content error, got %v", errs)
	}
}

func TestValidatePublishCourse_DraftLessonsAllowed(t *testing.T) {
	modules := []*pkgmodels.CourseModule{
		{
			Slug: "m1", Title: "Module 1", Order: 1,
			Lessons: []*pkgmodels.CourseLesson{
				{Slug: "l1", Title: "Lesson 1", Order: 1, IsDraft: true},
				{Slug: "l2", Title: "Lesson 2", Order: 2, ContentMarkdown: "x"},
			},
		},
	}
	errs := validatePublishCourse("Course", modules)
	if len(errs) != 0 {
		t.Errorf("expected no errors when a draft lesson has no content, got %v", errs)
	}
}

func TestValidatePublishCourse_VideoModeUploaded_NeedsSource(t *testing.T) {
	modules := []*pkgmodels.CourseModule{
		{
			Slug: "m1", Title: "Module 1", Order: 1,
			Lessons: []*pkgmodels.CourseLesson{
				{
					Slug: "l1", Title: "Lesson 1", Order: 1,
					ContentMarkdown: "placeholder",
					VideoMode:       pkgmodels.VideoModeUploaded,
				},
			},
		},
	}
	errs := validatePublishCourse("Course", modules)
	if len(errs) == 0 {
		t.Fatal("expected error for uploaded video without source")
	}
	joined := strings.Join(errs, "|")
	if !strings.Contains(joined, "video_mode=uploaded") {
		t.Errorf("expected video_mode=uploaded error, got %v", errs)
	}
}

func TestValidatePublishCourse_VideoModeStub_NeedsScript(t *testing.T) {
	modules := []*pkgmodels.CourseModule{
		{
			Slug: "m1", Title: "Module 1", Order: 1,
			Lessons: []*pkgmodels.CourseLesson{
				{
					Slug: "l1", Title: "Lesson 1", Order: 1,
					VideoMode: pkgmodels.VideoModeStub,
					VideoURL:  "https://example.com/v.mp4",
				},
			},
		},
	}
	errs := validatePublishCourse("Course", modules)
	if len(errs) == 0 {
		t.Fatal("expected error for stub video without script/content")
	}
	joined := strings.Join(errs, "|")
	if !strings.Contains(joined, "video_mode=stub") {
		t.Errorf("expected video_mode=stub error, got %v", errs)
	}
}

func TestValidatePublishCourse_DuplicateSlugs(t *testing.T) {
	modules := []*pkgmodels.CourseModule{
		{
			Slug: "m1", Title: "Module 1", Order: 1,
			Lessons: []*pkgmodels.CourseLesson{
				{Slug: "l1", Title: "L1", Order: 1, ContentMarkdown: "x"},
				{Slug: "l1", Title: "L1 dup", Order: 2, ContentMarkdown: "x"},
			},
		},
		{
			Slug: "m1", Title: "Module 1 dup", Order: 2,
			Lessons: []*pkgmodels.CourseLesson{
				{Slug: "l2", Title: "L2", Order: 1, ContentMarkdown: "x"},
			},
		},
	}
	errs := validatePublishCourse("Course", modules)
	joined := strings.Join(errs, "|")
	if !strings.Contains(joined, "duplicate module slug") {
		t.Errorf("expected duplicate module slug error, got %v", errs)
	}
	if !strings.Contains(joined, "duplicate lesson slug") {
		t.Errorf("expected duplicate lesson slug error, got %v", errs)
	}
}
