package services

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/queries"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
	"github.com/josephalai/sentanyl/pkg/utils"
)

// GenerationService is the shared service for staged course generation.
// Both the LMS UI and the SentanylScript compiler dispatch into this service
// so that one canonical pipeline drives all course generation and editing.
type GenerationService struct{}

// NewGenerationService creates a GenerationService.
func NewGenerationService() *GenerationService {
	return &GenerationService{}
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

	// Build outline — in production, this is an LLM call
	if moduleCount <= 0 {
		moduleCount = 3
	}
	outline := buildDeterministicOutline(prompt, audience, outcome, moduleCount, quizzesEnabled, defaultMedia)

	outlineBytes, _ := json.Marshal(outline)
	job.OutlineJSON = string(outlineBytes)
	job.Status = pkgmodels.GenStatusOutlineReady

	created, err := queries.CreateGenerationJob(job)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create generation job: %w", err)
	}

	_ = refText // used for grounding in real LLM call
	return created, &outline, nil
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
	product, err := queries.GetCourseProductByPublicId(tenantID, productPublicId)
	if err != nil {
		return nil, fmt.Errorf("course not found: %w", err)
	}

	// Update job status
	if jobPublicId != "" {
		queries.UpdateGenerationJob(tenantID, jobPublicId, bson.M{
			"status": string(pkgmodels.GenStatusGeneratingTree),
		})
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
			if videoMode == pkgmodels.VideoModeStub {
				lesson.VideoStubScript = fmt.Sprintf("Video script for: %s", ol.Title)
				lesson.VideoStubDescription = ol.Description
				lesson.VideoUploadPending = true
			}
			lesson.ContentMarkdown = fmt.Sprintf("# %s\n\nContent for this lesson.", ol.Title)
			mod.Lessons = append(mod.Lessons, lesson)
			totalLessons++
		}

		// Add quiz if enabled
		if quizzesEnabled && om.QuizEnabled {
			quizSlug := fmt.Sprintf("quiz-%d", i+1)
			mod.QuizSlug = quizSlug
			quiz := &pkgmodels.LMSQuiz{
				Id:            bson.NewObjectId(),
				PublicId:      utils.GeneratePublicId(),
				TenantID:      tenantID,
				ProductID:     product.Id,
				ModuleSlug:    mod.Slug,
				Slug:          quizSlug,
				Title:         fmt.Sprintf("%s Quiz", om.Title),
				PassThreshold: 70,
				MaxAttempts:   3,
				Questions: []*pkgmodels.LMSQuizQuestion{
					{
						Slug:          "q1",
						Type:          "multiple_choice",
						Title:         fmt.Sprintf("Question about %s", om.Title),
						Options:       []string{"Option A", "Option B", "Option C", "Option D"},
						CorrectAnswer: 0,
						Order:         1,
					},
				},
			}
			quiz.SetCreated()
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

func buildDeterministicOutline(prompt, audience, outcome string, moduleCount int, quizzesEnabled bool, defaultMedia string) CourseOutline {
	outline := CourseOutline{
		Title:       prompt,
		Description: fmt.Sprintf("A course for %s to %s", audience, outcome),
	}

	for i := 1; i <= moduleCount; i++ {
		mod := OutlineModule{
			Title:       fmt.Sprintf("Module %d", i),
			Order:       i,
			QuizEnabled: quizzesEnabled,
		}
		lessonsPerModule := 3
		for j := 1; j <= lessonsPerModule; j++ {
			lesson := OutlineLesson{
				Title:       fmt.Sprintf("Lesson %d.%d", i, j),
				Order:       j,
				VideoMode:   defaultMedia,
				Description: fmt.Sprintf("Learn about module %d, lesson %d", i, j),
			}
			mod.Lessons = append(mod.Lessons, lesson)
		}
		outline.Modules = append(outline.Modules, mod)
	}

	return outline
}

func init() {
	log.Println("[LMS] Generation service initialized")
}
