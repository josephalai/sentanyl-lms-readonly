package routes

import (
	"fmt"
	"strings"

	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// validatePublishCourse returns a list of human-readable validation errors
// that must be fixed before a course can transition to status="published".
// Returns an empty slice when the course is publishable.
func validatePublishCourse(title string, modules []*pkgmodels.CourseModule) []string {
	var errs []string

	if strings.TrimSpace(title) == "" {
		errs = append(errs, "title is required")
	}

	if len(modules) == 0 {
		errs = append(errs, "at least one module is required")
	}

	totalLessons := 0
	seenModuleSlugs := map[string]bool{}

	for mi, m := range modules {
		if m == nil {
			errs = append(errs, fmt.Sprintf("module #%d is empty", mi+1))
			continue
		}

		ctx := fmt.Sprintf("module #%d (%q)", mi+1, moduleLabel(m))

		if strings.TrimSpace(m.Slug) == "" {
			errs = append(errs, ctx+": slug is required")
		} else if seenModuleSlugs[m.Slug] {
			errs = append(errs, ctx+": duplicate module slug")
		} else {
			seenModuleSlugs[m.Slug] = true
		}
		if strings.TrimSpace(m.Title) == "" {
			errs = append(errs, ctx+": title is required")
		}
		if m.Order <= 0 {
			errs = append(errs, ctx+": order must be > 0")
		}

		seenLessonSlugs := map[string]bool{}
		for li, l := range m.Lessons {
			totalLessons++
			if l == nil {
				errs = append(errs, fmt.Sprintf("%s lesson #%d is empty", ctx, li+1))
				continue
			}
			lctx := fmt.Sprintf("%s lesson #%d (%q)", ctx, li+1, lessonLabel(l))

			if strings.TrimSpace(l.Slug) == "" {
				errs = append(errs, lctx+": slug is required")
			} else if seenLessonSlugs[l.Slug] {
				errs = append(errs, lctx+": duplicate lesson slug within module")
			} else {
				seenLessonSlugs[l.Slug] = true
			}
			if strings.TrimSpace(l.Title) == "" {
				errs = append(errs, lctx+": title is required")
			}
			if l.Order <= 0 {
				errs = append(errs, lctx+": order must be > 0")
			}

			if !l.IsDraft {
				if !lessonHasContent(l) {
					errs = append(errs, lctx+": published lesson must have content_markdown, content_html, video_url, media_public_id, or video_stub_script")
				}
				switch l.VideoMode {
				case pkgmodels.VideoModeUploaded:
					if strings.TrimSpace(l.VideoURL) == "" && strings.TrimSpace(l.MediaPublicId) == "" {
						errs = append(errs, lctx+": video_mode=uploaded requires video_url or media_public_id")
					}
				case pkgmodels.VideoModeStub:
					if strings.TrimSpace(l.VideoStubScript) == "" && strings.TrimSpace(l.ContentMarkdown) == "" {
						errs = append(errs, lctx+": video_mode=stub requires video_stub_script or content_markdown")
					}
				}
			}
		}
	}

	if totalLessons == 0 && len(modules) > 0 {
		errs = append(errs, "at least one lesson is required across all modules")
	}

	return errs
}

func lessonHasContent(l *pkgmodels.CourseLesson) bool {
	return strings.TrimSpace(l.ContentMarkdown) != "" ||
		strings.TrimSpace(l.ContentHTML) != "" ||
		strings.TrimSpace(l.VideoURL) != "" ||
		strings.TrimSpace(l.MediaPublicId) != "" ||
		strings.TrimSpace(l.VideoStubScript) != ""
}

func moduleLabel(m *pkgmodels.CourseModule) string {
	if m.Title != "" {
		return m.Title
	}
	return m.Slug
}

func lessonLabel(l *pkgmodels.CourseLesson) string {
	if l.Title != "" {
		return l.Title
	}
	return l.Slug
}
