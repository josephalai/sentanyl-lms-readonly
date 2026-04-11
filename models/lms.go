package models

import (
	"time"

	"github.com/josephalai/sentanyl/pkg/utils"
	"gopkg.in/mgo.v2/bson"

	sharedmodels "github.com/josephalai/sentanyl/pkg/models"
)

// GenConfig holds configuration for AI generation pipelines.
type GenConfig struct {
	Instruction string   `bson:"instruction,omitempty" json:"instruction,omitempty"`
	References  []string `bson:"references,omitempty" json:"references,omitempty"`
	Theme       string   `bson:"theme,omitempty" json:"theme,omitempty"`
	ErrorMsg    string   `bson:"error_msg,omitempty" json:"error_msg,omitempty"`
}

// CourseModule represents a module within a course product.
type CourseModule struct {
	Slug     string          `bson:"slug" json:"slug"`
	Title    string          `bson:"title" json:"title"`
	Order    int             `bson:"order" json:"order"`
	Lessons  []*CourseLesson `bson:"lessons" json:"lessons"`
	QuizSlug string          `bson:"quiz_slug,omitempty" json:"quiz_slug,omitempty"`
}

// CourseLesson represents a lesson within a course module.
type CourseLesson struct {
	Slug             string     `bson:"slug" json:"slug"`
	Title            string     `bson:"title" json:"title"`
	Order            int        `bson:"order" json:"order"`
	VideoURL         string     `bson:"video_url,omitempty" json:"video_url,omitempty"`
	MediaPublicId    string     `bson:"media_public_id,omitempty" json:"media_public_id,omitempty"`
	Duration         string     `bson:"duration,omitempty" json:"duration,omitempty"`
	DurationSec      int64      `bson:"duration_sec,omitempty" json:"duration_sec,omitempty"`
	ContentHTML      string     `bson:"content_html,omitempty" json:"content_html,omitempty"`
	ContentGenStatus string     `bson:"content_gen_status,omitempty" json:"content_gen_status,omitempty"`
	ContentGenConfig *GenConfig `bson:"content_gen_config,omitempty" json:"content_gen_config,omitempty"`
	IsFree           bool       `bson:"is_free,omitempty" json:"is_free,omitempty"`
	IsDraft          bool       `bson:"is_draft,omitempty" json:"is_draft,omitempty"`
	DripDays         int        `bson:"drip_days,omitempty" json:"drip_days,omitempty"`
}

// Product mirrors the course-relevant fields from the monolith's Product entity.
type Product struct {
	Id                   bson.ObjectId   `bson:"_id" json:"id,omitempty"`
	PublicId             string          `bson:"public_id" json:"public_id,omitempty"`
	TenantID             bson.ObjectId   `bson:"tenant_id,omitempty" json:"tenant_id,omitempty"`
	SubscriberId         string          `bson:"subscriber_id" json:"subscriber_id,omitempty"`
	Name                 string          `bson:"name" json:"name,omitempty"`
	Description          string          `bson:"description,omitempty" json:"description,omitempty"`
	ProductType          string          `bson:"product_type,omitempty" json:"product_type,omitempty"`
	ThumbnailURL         string          `bson:"thumbnail_url,omitempty" json:"thumbnail_url,omitempty"`
	Status               string          `bson:"status,omitempty" json:"status,omitempty"`
	InstructorName       string          `bson:"instructor_name,omitempty" json:"instructor_name,omitempty"`
	CourseModules        []*CourseModule `bson:"course_modules,omitempty" json:"course_modules,omitempty"`
	TotalLessons         int             `bson:"total_lessons,omitempty" json:"total_lessons,omitempty"`
	TotalDurationSec     int64           `bson:"total_duration_sec,omitempty" json:"total_duration_sec,omitempty"`
	EnrollmentCount      int             `bson:"enrollment_count,omitempty" json:"enrollment_count,omitempty"`
	CompletionCount      int             `bson:"completion_count,omitempty" json:"completion_count,omitempty"`
	DescriptionGenStatus string          `bson:"description_gen_status,omitempty" json:"description_gen_status,omitempty"`
	DescriptionGenConfig *GenConfig      `bson:"description_gen_config,omitempty" json:"description_gen_config,omitempty"`
	sharedmodels.SoftDeletes `bson:"timestamps,omitempty" json:"timestamps,omitempty"`
}

// CourseEnrollment tracks a contact's enrollment in a course.
type CourseEnrollment struct {
	Id              bson.ObjectId     `bson:"_id" json:"id,omitempty"`
	PublicId        string            `bson:"public_id" json:"public_id,omitempty"`
	TenantID        bson.ObjectId     `bson:"tenant_id" json:"tenant_id,omitempty"`
	SubscriberId    string            `bson:"subscriber_id" json:"subscriber_id,omitempty"`
	ContactID       bson.ObjectId     `bson:"contact_id" json:"contact_id,omitempty"`
	ProductID       bson.ObjectId     `bson:"product_id" json:"product_id,omitempty"`
	ProductPublicId string            `bson:"product_public_id" json:"product_public_id,omitempty"`
	EnrollmentBadge string            `bson:"enrollment_badge,omitempty" json:"enrollment_badge,omitempty"`
	Status          string            `bson:"status" json:"status,omitempty"`
	Progress        []*LessonProgress `bson:"progress,omitempty" json:"progress,omitempty"`
	OverallPercent  int               `bson:"overall_percent" json:"overall_percent"`
	EnrolledAt      time.Time         `bson:"enrolled_at" json:"enrolled_at,omitempty"`
	CompletedAt     *time.Time        `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	RevokedAt       *time.Time        `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	ExpiresAt       *time.Time        `bson:"expires_at,omitempty" json:"expires_at,omitempty"`
	sharedmodels.SoftDeletes `bson:"timestamps,omitempty" json:"timestamps,omitempty"`
}

// LessonProgress tracks a contact's progress on a specific lesson.
type LessonProgress struct {
	LessonSlug      string     `bson:"lesson_slug" json:"lesson_slug"`
	ModuleSlug      string     `bson:"module_slug" json:"module_slug"`
	WatchPercent    int        `bson:"watch_percent" json:"watch_percent"`
	LastPositionSec int        `bson:"last_position_sec" json:"last_position_sec"`
	Completed       bool       `bson:"completed" json:"completed"`
	CompletedAt     *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	QuizPassed      *bool      `bson:"quiz_passed,omitempty" json:"quiz_passed,omitempty"`
}

// LessonCompletion is an immutable event record created when a lesson is completed.
type LessonCompletion struct {
	Id           bson.ObjectId `bson:"_id" json:"id,omitempty"`
	TenantID     bson.ObjectId `bson:"tenant_id" json:"tenant_id,omitempty"`
	SubscriberId string        `bson:"subscriber_id" json:"subscriber_id,omitempty"`
	ContactID    bson.ObjectId `bson:"contact_id" json:"contact_id,omitempty"`
	ProductID    bson.ObjectId `bson:"product_id" json:"product_id,omitempty"`
	EnrollmentID bson.ObjectId `bson:"enrollment_id" json:"enrollment_id,omitempty"`
	ModuleSlug   string        `bson:"module_slug" json:"module_slug,omitempty"`
	LessonSlug   string        `bson:"lesson_slug" json:"lesson_slug,omitempty"`
	WatchPercent int           `bson:"watch_percent" json:"watch_percent"`
	CompletedAt  time.Time     `bson:"completed_at" json:"completed_at,omitempty"`
}

// Certificate represents a course completion certificate.
type Certificate struct {
	Id              bson.ObjectId  `bson:"_id" json:"id,omitempty"`
	PublicId        string         `bson:"public_id" json:"public_id,omitempty"`
	TenantID        bson.ObjectId  `bson:"tenant_id" json:"tenant_id,omitempty"`
	SubscriberId    string         `bson:"subscriber_id" json:"subscriber_id,omitempty"`
	ContactID       bson.ObjectId  `bson:"contact_id" json:"contact_id,omitempty"`
	ProductID       bson.ObjectId  `bson:"product_id" json:"product_id,omitempty"`
	ProductPublicId string         `bson:"product_public_id" json:"product_public_id,omitempty"`
	EnrollmentID    bson.ObjectId  `bson:"enrollment_id" json:"enrollment_id,omitempty"`
	ContactName     string         `bson:"contact_name" json:"contact_name,omitempty"`
	CourseTitle     string         `bson:"course_title" json:"course_title,omitempty"`
	CompletedAt     time.Time      `bson:"completed_at" json:"completed_at,omitempty"`
	AssetID         *bson.ObjectId `bson:"asset_id,omitempty" json:"asset_id,omitempty"`
	AssetURL        string         `bson:"asset_url,omitempty" json:"asset_url,omitempty"`
	Template        string         `bson:"template" json:"template,omitempty"`
	GenStatus       string         `bson:"gen_status" json:"gen_status,omitempty"`
	GenErrorMsg     string         `bson:"gen_error_msg,omitempty" json:"gen_error_msg,omitempty"`
	IssuedAt        *time.Time     `bson:"issued_at,omitempty" json:"issued_at,omitempty"`
	sharedmodels.SoftDeletes `bson:"timestamps,omitempty" json:"timestamps,omitempty"`
}

// LMSQuiz represents a quiz attached to a course module.
type LMSQuiz struct {
	Id            bson.ObjectId      `bson:"_id" json:"id,omitempty"`
	PublicId      string             `bson:"public_id" json:"public_id,omitempty"`
	TenantID      bson.ObjectId      `bson:"tenant_id" json:"tenant_id,omitempty"`
	SubscriberId  string             `bson:"subscriber_id" json:"subscriber_id,omitempty"`
	ProductID     bson.ObjectId      `bson:"product_id" json:"product_id,omitempty"`
	ModuleSlug    string             `bson:"module_slug" json:"module_slug,omitempty"`
	Slug          string             `bson:"slug" json:"slug,omitempty"`
	Title         string             `bson:"title" json:"title,omitempty"`
	PassThreshold int                `bson:"pass_threshold" json:"pass_threshold"`
	MaxAttempts   int                `bson:"max_attempts" json:"max_attempts"`
	Questions     []*LMSQuizQuestion `bson:"questions" json:"questions,omitempty"`
	sharedmodels.SoftDeletes `bson:"timestamps,omitempty" json:"timestamps,omitempty"`
}

// LMSQuizQuestion represents a question within an LMS quiz.
type LMSQuizQuestion struct {
	Slug          string   `bson:"slug" json:"slug"`
	Type          string   `bson:"type" json:"type"`
	Title         string   `bson:"title" json:"title"`
	Options       []string `bson:"options,omitempty" json:"options,omitempty"`
	CorrectAnswer int      `bson:"correct_answer,omitempty" json:"correct_answer,omitempty"`
	CorrectText   string   `bson:"correct_text,omitempty" json:"correct_text,omitempty"`
	Order         int      `bson:"order" json:"order"`
}

// QuizAttempt records a contact's attempt at a quiz.
type QuizAttempt struct {
	Id            bson.ObjectId        `bson:"_id" json:"id,omitempty"`
	TenantID      bson.ObjectId        `bson:"tenant_id" json:"tenant_id,omitempty"`
	SubscriberId  string               `bson:"subscriber_id" json:"subscriber_id,omitempty"`
	ContactID     bson.ObjectId        `bson:"contact_id" json:"contact_id,omitempty"`
	QuizID        bson.ObjectId        `bson:"quiz_id" json:"quiz_id,omitempty"`
	EnrollmentID  bson.ObjectId        `bson:"enrollment_id" json:"enrollment_id,omitempty"`
	Answers       []*QuizAttemptAnswer `bson:"answers" json:"answers,omitempty"`
	Score         int                  `bson:"score" json:"score"`
	Passed        bool                 `bson:"passed" json:"passed"`
	AttemptNumber int                  `bson:"attempt_number" json:"attempt_number"`
	SubmittedAt   time.Time            `bson:"submitted_at" json:"submitted_at,omitempty"`
}

// QuizAttemptAnswer represents an answer in a quiz attempt.
type QuizAttemptAnswer struct {
	QuestionSlug string `bson:"question_slug" json:"question_slug"`
	AnswerIndex  int    `bson:"answer_index,omitempty" json:"answer_index,omitempty"`
	AnswerText   string `bson:"answer_text,omitempty" json:"answer_text,omitempty"`
	IsCorrect    bool   `bson:"is_correct" json:"is_correct"`
}

// NewCourseEnrollment creates a new enrollment with defaults.
func NewCourseEnrollment(tenantID, contactID, productID bson.ObjectId, productPublicId, enrollmentBadge string) *CourseEnrollment {
	return &CourseEnrollment{
		Id:              bson.NewObjectId(),
		PublicId:        utils.GeneratePublicId(),
		TenantID:        tenantID,
		ContactID:       contactID,
		ProductID:       productID,
		ProductPublicId: productPublicId,
		EnrollmentBadge: enrollmentBadge,
		Status:          "active",
		OverallPercent:  0,
		EnrolledAt:      time.Now(),
	}
}

// NewCertificate creates a new certificate with defaults.
func NewCertificate(tenantID, contactID, productID, enrollmentID bson.ObjectId, productPublicId, contactName, courseTitle, template string, completedAt time.Time) *Certificate {
	return &Certificate{
		Id:              bson.NewObjectId(),
		PublicId:        utils.GeneratePublicId(),
		TenantID:        tenantID,
		ContactID:       contactID,
		ProductID:       productID,
		ProductPublicId: productPublicId,
		EnrollmentID:    enrollmentID,
		ContactName:     contactName,
		CourseTitle:     courseTitle,
		CompletedAt:     completedAt,
		Template:        template,
		GenStatus:       "pending",
	}
}
