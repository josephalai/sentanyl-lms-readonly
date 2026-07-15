package routes

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/queries"
	"github.com/josephalai/sentanyl/lms-service/services"
	"github.com/josephalai/sentanyl/pkg/aigov"
	"github.com/josephalai/sentanyl/pkg/auth"
	"github.com/josephalai/sentanyl/pkg/db"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
	"github.com/josephalai/sentanyl/pkg/utils"
)

// RegisterLMSRoutes registers all LMS endpoints under the given router group.
func RegisterLMSRoutes(tenant *gin.RouterGroup) {
	lms := tenant.Group("/lms")
	{
		// Courses
		lms.GET("/courses", handleListCourses)
		lms.POST("/courses", handleCreateCourse)
		lms.GET("/courses/:courseId", handleGetCourse)
		lms.PUT("/courses/:courseId", handleUpdateCourse)
		lms.DELETE("/courses/:courseId", handleDeleteCourse)

		// Enrollments
		lms.GET("/enrollments", handleListEnrollments)
		lms.POST("/enrollments", handleCreateEnrollment)
		lms.GET("/enrollments/:enrollmentId", handleGetEnrollment)
		lms.POST("/enrollments/:enrollmentId/progress", handleUpdateLessonProgress)
		lms.POST("/enrollments/:enrollmentId/revoke", handleRevokeEnrollment)

		// Quizzes
		lms.GET("/quizzes", handleListQuizzes)
		lms.POST("/quizzes", handleCreateQuiz)
		lms.GET("/quizzes/:quizId", handleGetQuiz)
		lms.PUT("/quizzes/:quizId", handleUpdateQuiz)
		lms.DELETE("/quizzes/:quizId", handleDeleteQuiz)
		lms.POST("/quizzes/:quizId/attempt", handleSubmitQuizAttempt)
		lms.GET("/quizzes/:quizId/attempts", handleListQuizAttempts)

		// Certificates
		lms.GET("/certificates", handleListLMSCertificates)
		lms.GET("/certificates/:certId", handleGetLMSCertificate)
		lms.POST("/certificates/:certId/regenerate", handleRegenerateCertificate)

		// Generation Jobs
		lms.POST("/courses/:courseId/generate-outline", handleGenerateOutline)
		lms.POST("/courses/:courseId/generate-full", handleGenerateFullCourse)
		lms.POST("/courses/:courseId/edit-prompt", handleEditCoursePrompt)
		lms.GET("/courses/:courseId/generation-jobs", handleListGenerationJobs)
		lms.GET("/generation-jobs/:jobId", handleGetGenerationJob)

		// Content Patches
		lms.GET("/courses/:courseId/patches", handleListPatches)
		lms.GET("/patches/:patchId", handleGetPatch)
		lms.POST("/patches/:patchId/approve", handleApprovePatch)
		lms.POST("/patches/:patchId/reject", handleRejectPatch)

		// Source References
		lms.POST("/courses/:courseId/references", handleUploadReference)
		lms.GET("/courses/:courseId/references", handleListReferences)
		lms.DELETE("/references/:refId", handleDeleteReference)

		// Certificate Templates
		lms.GET("/courses/:courseId/certificate-template", handleGetCertificateTemplate)
		lms.PUT("/courses/:courseId/certificate-template", handleUpdateCertificateTemplate)
	}
}

func admitLMSAI(c *gin.Context, tenantID bson.ObjectId, surface string, inputChars, outputTokens int64) (*pkgmodels.AIOperation, context.Context, context.CancelFunc, bool) {
	op, err := aigov.Begin(tenantID, surface, aigov.Estimate{InputCharacters: inputChars, OutputTokens: outputTokens}, time.Now().UTC())
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error(), "code": "ai_admission_failed"})
		return nil, nil, nil, false
	}
	ctx, cancel := aigov.Context(c.Request.Context(), op)
	c.Header("X-AI-Operation-Id", op.PublicID)
	return op, ctx, cancel, true
}

func settleLMSAI(op *pkgmodels.AIOperation, err error) {
	if err != nil {
		_ = aigov.Fail(op, err, time.Now().UTC())
		return
	}
	_ = aigov.Complete(op, aigov.Usage{}, time.Now().UTC())
}

// ---------- Course Handlers ----------

func handleListCourses(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	status := c.DefaultQuery("status", "")
	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 100 {
		limit = 100
	}

	products, err := queries.ListCourseProducts(tenantID, status, skip, limit)
	if err != nil {
		log.Printf("[LMS] Error listing courses: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list courses"})
		return
	}

	total, _ := queries.CountCourseProducts(tenantID, status)

	type courseItem struct {
		PublicId        string `json:"public_id"`
		Title           string `json:"title"`
		Description     string `json:"description"`
		InstructorName  string `json:"instructor_name"`
		Status          string `json:"status"`
		TotalLessons    int    `json:"total_lessons"`
		TotalModules    int    `json:"total_modules"`
		EnrollmentCount int    `json:"enrollment_count"`
		CompletionCount int    `json:"completion_count"`
		Thumbnail       string `json:"thumbnail,omitempty"`
		CreatedAt       string `json:"created_at"`
	}

	items := make([]courseItem, 0, len(products))
	for _, p := range products {
		created := ""
		if p.CreatedAt != nil {
			created = p.CreatedAt.Format(time.RFC3339)
		}
		items = append(items, courseItem{
			PublicId:        p.PublicId,
			Title:           p.Name,
			Description:     p.Description,
			InstructorName:  p.InstructorName,
			Status:          p.Status,
			TotalLessons:    p.TotalLessons,
			TotalModules:    len(p.CourseModules),
			EnrollmentCount: p.EnrollmentCount,
			CompletionCount: p.CompletionCount,
			Thumbnail:       p.ThumbnailURL,
			CreatedAt:       created,
		})
	}

	c.JSON(http.StatusOK, gin.H{"courses": items, "total": total})
}

func handleCreateCourse(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Title               string     `json:"title" binding:"required"`
		Description         string     `json:"description"`
		InstructorName      string     `json:"instructor_name"`
		Thumbnail           string     `json:"thumbnail"`
		CertificateEnabled  *bool      `json:"certificate_enabled"`
		CertificateTemplate string     `json:"certificate_template"`
		SequentialGating    bool       `json:"sequential_gating"`
		RequireQuizPass     bool       `json:"require_quiz_pass"`
		DripAnchor          string     `json:"drip_anchor"`
		DripAnchorDate      *time.Time `json:"drip_anchor_date"`
		Modules             []struct {
			Slug     string `json:"slug" binding:"required"`
			Title    string `json:"title" binding:"required"`
			Order    int    `json:"order"`
			QuizSlug string `json:"quiz_slug"`
			Lessons  []struct {
				Slug                 string                                  `json:"slug" binding:"required"`
				Title                string                                  `json:"title" binding:"required"`
				Order                int                                     `json:"order"`
				VideoURL             string                                  `json:"video_url"`
				MediaPublicId        string                                  `json:"media_public_id"`
				Duration             string                                  `json:"duration"`
				DurationSec          int64                                   `json:"duration_sec"`
				ContentHTML          string                                  `json:"content_html"`
				ContentGenStatus     string                                  `json:"content_gen_status"`
				ContentGenConfig     map[string]interface{}                  `json:"content_gen_config"`
				IsFree               bool                                    `json:"is_free"`
				IsDraft              bool                                    `json:"is_draft"`
				DripDays             int                                     `json:"drip_days"`
				DripHours            int                                     `json:"drip_hours"`
				DripMinutes          int                                     `json:"drip_minutes"`
				LiveStartsAt         *time.Time                              `json:"live_starts_at,omitempty"`
				LiveEndsAt           *time.Time                              `json:"live_ends_at,omitempty"`
				BadgeRules           []*pkgmodels.MediaBadgeRule             `json:"badge_rules,omitempty"`
				Translations         map[string]*pkgmodels.LessonTranslation `json:"translations,omitempty"`
				VideoMode            string                                  `json:"video_mode"`
				VideoStubScript      string                                  `json:"video_stub_script"`
				VideoStubDescription string                                  `json:"video_stub_description"`
				VideoUploadPending   bool                                    `json:"video_upload_pending"`
				ContentMarkdown      string                                  `json:"content_markdown"`
			} `json:"lessons"`
		} `json:"modules"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	product := &pkgmodels.Product{
		Id:                  bson.NewObjectId(),
		PublicId:            utils.GeneratePublicId(),
		TenantID:            tenantID,
		Name:                req.Title,
		Description:         req.Description,
		ProductType:         "course",
		InstructorName:      req.InstructorName,
		ThumbnailURL:        req.Thumbnail,
		Status:              "draft",
		CertificateEnabled:  req.CertificateEnabled,
		CertificateTemplate: req.CertificateTemplate,
		SequentialGating:    req.SequentialGating,
		RequireQuizPass:     req.RequireQuizPass,
		DripAnchor:          req.DripAnchor,
		DripAnchorDate:      req.DripAnchorDate,
	}

	totalLessons := 0
	for _, m := range req.Modules {
		mod := &pkgmodels.CourseModule{
			Slug:     m.Slug,
			Title:    m.Title,
			Order:    m.Order,
			QuizSlug: m.QuizSlug,
		}
		for _, l := range m.Lessons {
			lesson := &pkgmodels.CourseLesson{
				Slug:                 l.Slug,
				Title:                l.Title,
				Order:                l.Order,
				VideoURL:             l.VideoURL,
				MediaPublicId:        l.MediaPublicId,
				Duration:             l.Duration,
				DurationSec:          l.DurationSec,
				ContentHTML:          l.ContentHTML,
				ContentGenStatus:     l.ContentGenStatus,
				IsFree:               l.IsFree,
				IsDraft:              l.IsDraft,
				DripDays:             l.DripDays,
				DripHours:            l.DripHours,
				DripMinutes:          l.DripMinutes,
				LiveStartsAt:         l.LiveStartsAt,
				LiveEndsAt:           l.LiveEndsAt,
				BadgeRules:           l.BadgeRules,
				Translations:         l.Translations,
				VideoMode:            pkgmodels.VideoMode(l.VideoMode),
				VideoStubScript:      l.VideoStubScript,
				VideoStubDescription: l.VideoStubDescription,
				VideoUploadPending:   l.VideoUploadPending,
				ContentMarkdown:      l.ContentMarkdown,
			}
			if l.ContentGenConfig != nil {
				lesson.ContentGenConfig = &pkgmodels.GenConfig{}
				if v, ok := l.ContentGenConfig["instruction"].(string); ok {
					lesson.ContentGenConfig.Instruction = v
				}
				if v, ok := l.ContentGenConfig["theme"].(string); ok {
					lesson.ContentGenConfig.Theme = v
				}
			}
			mod.Lessons = append(mod.Lessons, lesson)
			totalLessons++
		}
		product.CourseModules = append(product.CourseModules, mod)
	}
	product.TotalLessons = totalLessons

	product.SetCreated()
	if err := queries.InsertProduct(*product); err != nil {
		log.Printf("[LMS] Error creating course: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create course"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"public_id":       product.PublicId,
		"title":           product.Name,
		"status":          product.Status,
		"total_lessons":   product.TotalLessons,
		"total_modules":   len(product.CourseModules),
		"instructor_name": product.InstructorName,
	})
}

func handleGetCourse(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	product, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	created := ""
	if product.CreatedAt != nil {
		created = product.CreatedAt.Format(time.RFC3339)
	}
	updated := ""
	if product.UpdatedAt != nil {
		updated = product.UpdatedAt.Format(time.RFC3339)
	}

	resp := gin.H{
		"id":                   product.Id.Hex(),
		"public_id":            product.PublicId,
		"title":                product.Name,
		"description":          product.Description,
		"instructor_name":      product.InstructorName,
		"status":               product.Status,
		"thumbnail":            product.ThumbnailURL,
		"modules":              product.CourseModules,
		"total_lessons":        product.TotalLessons,
		"total_duration_sec":   product.TotalDurationSec,
		"enrollment_count":     product.EnrollmentCount,
		"completion_count":     product.CompletionCount,
		"created_at":           created,
		"updated_at":           updated,
		"certificate_enabled":  product.CertificateEnabled,
		"certificate_template": product.CertificateTemplate,
		"sequential_gating":    product.SequentialGating,
		"require_quiz_pass":    product.RequireQuizPass,
		"drip_anchor":          product.DripAnchor,
		"translations":         product.Translations,
	}
	if product.DripAnchorDate != nil {
		resp["drip_anchor_date"] = product.DripAnchorDate.Format(time.RFC3339)
	}
	c.JSON(http.StatusOK, resp)
}

func handleUpdateCourse(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	product, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	var req struct {
		Title               *string                                  `json:"title,omitempty"`
		Description         *string                                  `json:"description,omitempty"`
		InstructorName      *string                                  `json:"instructor_name,omitempty"`
		Thumbnail           *string                                  `json:"thumbnail,omitempty"`
		Status              *string                                  `json:"status,omitempty"`
		CertificateEnabled  *bool                                    `json:"certificate_enabled,omitempty"`
		CertificateTemplate *string                                  `json:"certificate_template,omitempty"`
		SequentialGating    *bool                                    `json:"sequential_gating,omitempty"`
		RequireQuizPass     *bool                                    `json:"require_quiz_pass,omitempty"`
		DripAnchor          *string                                  `json:"drip_anchor,omitempty"`
		DripAnchorDate      *time.Time                               `json:"drip_anchor_date,omitempty"`
		Translations        map[string]*pkgmodels.ProductTranslation `json:"translations,omitempty"`
		Modules             []struct {
			Slug         string                                  `json:"slug"`
			Title        string                                  `json:"title"`
			Order        int                                     `json:"order"`
			QuizSlug     string                                  `json:"quiz_slug"`
			Translations map[string]*pkgmodels.ModuleTranslation `json:"translations,omitempty"`
			Lessons      []struct {
				Slug                 string                                  `json:"slug"`
				Title                string                                  `json:"title"`
				Order                int                                     `json:"order"`
				VideoURL             string                                  `json:"video_url"`
				MediaPublicId        string                                  `json:"media_public_id"`
				Duration             string                                  `json:"duration"`
				DurationSec          int64                                   `json:"duration_sec"`
				ContentHTML          string                                  `json:"content_html"`
				ContentGenStatus     string                                  `json:"content_gen_status"`
				ContentGenConfig     map[string]interface{}                  `json:"content_gen_config"`
				IsFree               bool                                    `json:"is_free"`
				IsDraft              bool                                    `json:"is_draft"`
				DripDays             int                                     `json:"drip_days"`
				DripHours            int                                     `json:"drip_hours"`
				DripMinutes          int                                     `json:"drip_minutes"`
				LiveStartsAt         *time.Time                              `json:"live_starts_at,omitempty"`
				LiveEndsAt           *time.Time                              `json:"live_ends_at,omitempty"`
				BadgeRules           []*pkgmodels.MediaBadgeRule             `json:"badge_rules,omitempty"`
				Translations         map[string]*pkgmodels.LessonTranslation `json:"translations,omitempty"`
				VideoMode            string                                  `json:"video_mode"`
				VideoStubScript      string                                  `json:"video_stub_script"`
				VideoStubDescription string                                  `json:"video_stub_description"`
				VideoUploadPending   bool                                    `json:"video_upload_pending"`
				ContentMarkdown      string                                  `json:"content_markdown"`
			} `json:"lessons"`
		} `json:"modules,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	update := bson.M{}
	if req.Title != nil {
		update["name"] = *req.Title
	}
	if req.Description != nil {
		update["description"] = *req.Description
	}
	if req.InstructorName != nil {
		update["instructor_name"] = *req.InstructorName
	}
	if req.Thumbnail != nil {
		update["thumbnail_url"] = *req.Thumbnail
	}
	if req.Status != nil {
		if !pkgmodels.ValidProductStatusTransition(product.Status, *req.Status) {
			c.JSON(http.StatusConflict, gin.H{"error": "invalid course status transition from " + product.Status + " to " + *req.Status})
			return
		}
		update["status"] = *req.Status
	}
	if req.CertificateEnabled != nil {
		update["certificate_enabled"] = *req.CertificateEnabled
	}
	if req.CertificateTemplate != nil {
		update["certificate_template"] = *req.CertificateTemplate
	}
	if req.SequentialGating != nil {
		update["sequential_gating"] = *req.SequentialGating
	}
	if req.RequireQuizPass != nil {
		update["require_quiz_pass"] = *req.RequireQuizPass
	}
	if req.DripAnchor != nil {
		update["drip_anchor"] = *req.DripAnchor
	}
	if req.DripAnchorDate != nil {
		update["drip_anchor_date"] = req.DripAnchorDate
	}
	if req.Translations != nil {
		update["translations"] = req.Translations
	}

	var builtModules []*pkgmodels.CourseModule
	if req.Modules != nil {
		totalLessons := 0
		for _, m := range req.Modules {
			mod := &pkgmodels.CourseModule{
				Slug:         m.Slug,
				Title:        m.Title,
				Order:        m.Order,
				QuizSlug:     m.QuizSlug,
				Translations: m.Translations,
			}
			for _, l := range m.Lessons {
				lesson := &pkgmodels.CourseLesson{
					Slug:                 l.Slug,
					Title:                l.Title,
					Order:                l.Order,
					VideoURL:             l.VideoURL,
					MediaPublicId:        l.MediaPublicId,
					Duration:             l.Duration,
					DurationSec:          l.DurationSec,
					ContentHTML:          l.ContentHTML,
					ContentGenStatus:     l.ContentGenStatus,
					IsFree:               l.IsFree,
					IsDraft:              l.IsDraft,
					DripDays:             l.DripDays,
					DripHours:            l.DripHours,
					DripMinutes:          l.DripMinutes,
					LiveStartsAt:         l.LiveStartsAt,
					LiveEndsAt:           l.LiveEndsAt,
					BadgeRules:           l.BadgeRules,
					Translations:         l.Translations,
					VideoMode:            pkgmodels.VideoMode(l.VideoMode),
					VideoStubScript:      l.VideoStubScript,
					VideoStubDescription: l.VideoStubDescription,
					VideoUploadPending:   l.VideoUploadPending,
					ContentMarkdown:      l.ContentMarkdown,
				}
				if l.ContentGenConfig != nil {
					lesson.ContentGenConfig = &pkgmodels.GenConfig{}
					if v, ok := l.ContentGenConfig["instruction"].(string); ok {
						lesson.ContentGenConfig.Instruction = v
					}
					if v, ok := l.ContentGenConfig["theme"].(string); ok {
						lesson.ContentGenConfig.Theme = v
					}
				}
				mod.Lessons = append(mod.Lessons, lesson)
				totalLessons++
			}
			builtModules = append(builtModules, mod)
		}
		update["course_modules"] = builtModules
		update["total_lessons"] = totalLessons
	}

	if req.Status != nil && *req.Status == pkgmodels.ProductStatusActive {
		titleToCheck := product.Name
		if req.Title != nil {
			titleToCheck = *req.Title
		}
		modulesToCheck := product.CourseModules
		if req.Modules != nil {
			modulesToCheck = builtModules
		}
		if verrs := validatePublishCourse(titleToCheck, modulesToCheck); len(verrs) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "publish validation failed",
				"details": verrs,
			})
			return
		}
	}

	now := time.Now()
	update["timestamps.updated_at"] = now
	err = db.GetCollection(pkgmodels.ProductCollection).Update(
		bson.M{"_id": product.Id},
		bson.M{"$set": update},
	)
	if err != nil {
		log.Printf("[LMS] Error updating course: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update course"})
		return
	}

	handleGetCourse(c)
}

func handleDeleteCourse(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	product, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	now := time.Now()
	db.GetCollection(pkgmodels.ProductCollection).Update(
		bson.M{"_id": product.Id},
		bson.M{"$set": bson.M{"timestamps.deleted_at": now}},
	)

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ---------- Enrollment Handlers ----------

func handleListEnrollments(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 100 {
		limit = 100
	}
	status := c.DefaultQuery("status", "")

	var productID *bson.ObjectId
	if ppid := c.DefaultQuery("product_public_id", ""); ppid != "" {
		product, err := queries.GetCourseProductByPublicId(tenantID, ppid)
		if err == nil {
			productID = &product.Id
		}
	}

	var contactID *bson.ObjectId
	if cid := c.DefaultQuery("contact_id", ""); cid != "" && bson.IsObjectIdHex(cid) {
		oid := bson.ObjectIdHex(cid)
		contactID = &oid
	}

	var enrollments []*pkgmodels.CourseEnrollment
	var err error

	if contactID != nil {
		enrollments, err = queries.ListCourseEnrollmentsByContact(tenantID, *contactID, status, skip, limit)
	} else {
		enrollments, err = queries.ListCourseEnrollments(tenantID, productID, status, skip, limit)
	}

	if err != nil {
		log.Printf("[LMS] Error listing enrollments: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list enrollments"})
		return
	}

	type enrollmentItem struct {
		PublicId        string `json:"public_id"`
		ContactID       string `json:"contact_id"`
		ProductPublicId string `json:"product_public_id"`
		CourseTitle     string `json:"course_title"`
		Status          string `json:"status"`
		OverallPercent  int    `json:"overall_percent"`
		EnrolledAt      string `json:"enrolled_at"`
		CompletedAt     string `json:"completed_at,omitempty"`
	}

	items := make([]enrollmentItem, 0, len(enrollments))
	for _, e := range enrollments {
		item := enrollmentItem{
			PublicId:        e.PublicId,
			ContactID:       e.ContactID.Hex(),
			ProductPublicId: e.ProductPublicId,
			Status:          e.Status,
			OverallPercent:  e.OverallPercent,
			EnrolledAt:      e.EnrolledAt.Format(time.RFC3339),
		}
		if e.CompletedAt != nil {
			item.CompletedAt = e.CompletedAt.Format(time.RFC3339)
		}
		if product, err := queries.GetCourseProductByPublicId(tenantID, e.ProductPublicId); err == nil {
			item.CourseTitle = product.Name
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"enrollments": items, "total": len(items)})
}

func handleCreateEnrollment(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		ContactID       string `json:"contact_id" binding:"required"`
		ProductPublicId string `json:"product_public_id" binding:"required"`
		EnrollmentBadge string `json:"enrollment_badge"`
		ExpiresAt       string `json:"expires_at,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !bson.IsObjectIdHex(req.ContactID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contact_id"})
		return
	}
	contactID := bson.ObjectIdHex(req.ContactID)

	product, err := queries.GetCourseProductByPublicId(tenantID, req.ProductPublicId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "course not found"})
		return
	}

	enrollment := &pkgmodels.CourseEnrollment{
		Id:              bson.NewObjectId(),
		PublicId:        utils.GeneratePublicId(),
		TenantID:        tenantID,
		ContactID:       contactID,
		ProductID:       product.Id,
		ProductPublicId: product.PublicId,
		EnrollmentBadge: req.EnrollmentBadge,
		Status:          "active",
		OverallPercent:  0,
		EnrolledAt:      time.Now(),
	}

	if req.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpiresAt); err == nil {
			enrollment.ExpiresAt = &t
		}
	}

	enrollment, err = queries.CreateCourseEnrollment(enrollment)
	if err != nil {
		log.Printf("[LMS] Error creating enrollment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create enrollment"})
		return
	}

	queries.IncrementEnrollmentCount(tenantID, product.Id)

	c.JSON(http.StatusCreated, gin.H{
		"public_id":         enrollment.PublicId,
		"contact_id":        enrollment.ContactID.Hex(),
		"product_public_id": enrollment.ProductPublicId,
		"status":            enrollment.Status,
		"overall_percent":   enrollment.OverallPercent,
		"enrolled_at":       enrollment.EnrolledAt.Format(time.RFC3339),
	})
}

func handleGetEnrollment(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	enrollmentId := c.Param("enrollmentId")
	enrollment, err := queries.GetCourseEnrollmentByPublicId(tenantID, enrollmentId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "enrollment not found"})
		return
	}

	type progressResp struct {
		LessonSlug      string `json:"lesson_slug"`
		ModuleSlug      string `json:"module_slug"`
		WatchPercent    int    `json:"watch_percent"`
		LastPositionSec int    `json:"last_position_sec"`
		Completed       bool   `json:"completed"`
		CompletedAt     string `json:"completed_at,omitempty"`
		QuizPassed      *bool  `json:"quiz_passed,omitempty"`
	}

	progress := make([]progressResp, 0)
	for _, p := range enrollment.Progress {
		pr := progressResp{
			LessonSlug:      p.LessonSlug,
			ModuleSlug:      p.ModuleSlug,
			WatchPercent:    p.WatchPercent,
			LastPositionSec: p.LastPositionSec,
			Completed:       p.Completed,
			QuizPassed:      p.QuizPassed,
		}
		if p.CompletedAt != nil {
			pr.CompletedAt = p.CompletedAt.Format(time.RFC3339)
		}
		progress = append(progress, pr)
	}

	courseTitle := ""
	if product, err := queries.GetCourseProductByPublicId(tenantID, enrollment.ProductPublicId); err == nil {
		courseTitle = product.Name
	}

	resp := gin.H{
		"public_id":         enrollment.PublicId,
		"contact_id":        enrollment.ContactID.Hex(),
		"product_public_id": enrollment.ProductPublicId,
		"course_title":      courseTitle,
		"status":            enrollment.Status,
		"overall_percent":   enrollment.OverallPercent,
		"progress":          progress,
		"enrolled_at":       enrollment.EnrolledAt.Format(time.RFC3339),
	}
	if enrollment.CompletedAt != nil {
		resp["completed_at"] = enrollment.CompletedAt.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, resp)
}

func handleUpdateLessonProgress(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	enrollmentId := c.Param("enrollmentId")
	enrollment, err := queries.GetCourseEnrollmentByPublicId(tenantID, enrollmentId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "enrollment not found"})
		return
	}

	if enrollment.Status != "active" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enrollment is not active"})
		return
	}

	var req struct {
		LessonSlug      string `json:"lesson_slug" binding:"required"`
		ModuleSlug      string `json:"module_slug" binding:"required"`
		WatchPercent    int    `json:"watch_percent"`
		LastPositionSec int    `json:"last_position_sec"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	product, err := queries.GetCourseProductByPublicId(tenantID, enrollment.ProductPublicId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "course not found"})
		return
	}

	validLesson := false
	moduleHasQuiz := false
	for _, mod := range product.CourseModules {
		if mod.Slug == req.ModuleSlug {
			if mod.QuizSlug != "" {
				moduleHasQuiz = true
			}
			for _, lesson := range mod.Lessons {
				if lesson.Slug == req.LessonSlug {
					validLesson = true
					break
				}
			}
			break
		}
	}

	if !validLesson {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid lesson_slug or module_slug"})
		return
	}

	progress := &pkgmodels.LessonProgress{
		LessonSlug:      req.LessonSlug,
		ModuleSlug:      req.ModuleSlug,
		WatchPercent:    req.WatchPercent,
		LastPositionSec: req.LastPositionSec,
	}

	if req.WatchPercent >= 90 {
		if !moduleHasQuiz {
			now := time.Now()
			progress.Completed = true
			progress.CompletedAt = &now
		} else {
			for _, p := range enrollment.Progress {
				if p.LessonSlug == req.LessonSlug && p.ModuleSlug == req.ModuleSlug {
					if p.QuizPassed != nil && *p.QuizPassed {
						now := time.Now()
						progress.Completed = true
						progress.CompletedAt = &now
					}
					break
				}
			}
		}
	}

	enrollment, err = queries.UpdateLessonProgress(tenantID, enrollmentId, progress)
	if err != nil {
		log.Printf("[LMS] Error updating progress: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update progress"})
		return
	}

	overallPercent, err := queries.RecalculateOverallPercent(tenantID, enrollmentId, product)
	if err != nil {
		log.Printf("[LMS] Error recalculating percent: %v", err)
	}

	// Four-part completion cascade when overall_percent == 100
	if overallPercent == 100 && enrollment.Status == "active" {
		now := time.Now()

		queries.UpdateCourseEnrollment(tenantID, enrollmentId, bson.M{
			"status":       "completed",
			"completed_at": now,
		})

		completion := &pkgmodels.LessonCompletion{
			Id:           bson.NewObjectId(),
			TenantID:     tenantID,
			ContactID:    enrollment.ContactID,
			ProductID:    enrollment.ProductID,
			EnrollmentID: enrollment.Id,
			ModuleSlug:   req.ModuleSlug,
			LessonSlug:   req.LessonSlug,
			WatchPercent: req.WatchPercent,
			CompletedAt:  now,
		}
		queries.CreateLessonCompletion(completion)

		queries.IncrementCompletionCount(tenantID, enrollment.ProductID)

		// Use certificate template if configured, otherwise default
		certTemplate := "default"
		if tmpl, tErr := queries.GetCertificateTemplateByProduct(tenantID, enrollment.ProductID); tErr == nil && tmpl.Enabled {
			certTemplate = tmpl.TemplateName
		}

		cert := &pkgmodels.Certificate{
			Id:              bson.NewObjectId(),
			PublicId:        utils.GeneratePublicId(),
			TenantID:        tenantID,
			ContactID:       enrollment.ContactID,
			ProductID:       enrollment.ProductID,
			ProductPublicId: enrollment.ProductPublicId,
			EnrollmentID:    enrollment.Id,
			CourseTitle:     product.Name,
			CompletedAt:     now,
			Template:        certTemplate,
			GenStatus:       "pending",
		}
		queries.CreateCertificate(cert)

		log.Printf("[LMS] Course completed: enrollment=%s, contact=%s, course=%s",
			enrollment.PublicId, enrollment.ContactID.Hex(), product.Name)
	}

	handleGetEnrollment(c)
}

func handleRevokeEnrollment(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	enrollmentId := c.Param("enrollmentId")
	enrollment, err := queries.RevokeCourseEnrollment(tenantID, enrollmentId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke enrollment"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"public_id": enrollment.PublicId,
		"status":    enrollment.Status,
	})
}

// ---------- Quiz Handlers ----------

func handleListQuizzes(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	productPublicId := c.DefaultQuery("product_id", "")
	if productPublicId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "product_id query parameter required"})
		return
	}

	product, err := queries.GetCourseProductByPublicId(tenantID, productPublicId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	quizzes, err := queries.ListLMSQuizzesByProduct(tenantID, product.Id)
	if err != nil {
		log.Printf("[LMS] Error listing quizzes: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list quizzes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"quizzes": quizzes})
}

func handleGetQuiz(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	quizId := c.Param("quizId")
	quiz, err := queries.GetLMSQuizByPublicId(tenantID, quizId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "quiz not found"})
		return
	}

	c.JSON(http.StatusOK, quiz)
}

func handleSubmitQuizAttempt(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	quizId := c.Param("quizId")
	quiz, err := queries.GetLMSQuizByPublicId(tenantID, quizId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "quiz not found"})
		return
	}

	var req struct {
		ContactID    string `json:"contact_id" binding:"required"`
		EnrollmentID string `json:"enrollment_id" binding:"required"`
		Answers      []struct {
			QuestionSlug string `json:"question_slug" binding:"required"`
			AnswerIndex  *int   `json:"answer_index,omitempty"`
			AnswerText   string `json:"answer_text,omitempty"`
		} `json:"answers" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !bson.IsObjectIdHex(req.ContactID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contact_id"})
		return
	}
	contactID := bson.ObjectIdHex(req.ContactID)

	enrollment, err := queries.GetCourseEnrollmentByPublicId(tenantID, req.EnrollmentID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enrollment not found"})
		return
	}
	if enrollment.Status != "active" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enrollment is not active"})
		return
	}

	attemptCount, _ := queries.CountQuizAttempts(tenantID, quiz.Id, contactID)
	if quiz.MaxAttempts > 0 && attemptCount >= quiz.MaxAttempts {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max attempts reached"})
		return
	}

	correctCount := 0
	totalQuestions := len(quiz.Questions)
	var attemptAnswers []*pkgmodels.QuizAttemptAnswer

	for _, ans := range req.Answers {
		aa := &pkgmodels.QuizAttemptAnswer{
			QuestionSlug: ans.QuestionSlug,
		}
		if ans.AnswerIndex != nil {
			aa.AnswerIndex = *ans.AnswerIndex
		}
		aa.AnswerText = ans.AnswerText

		for _, q := range quiz.Questions {
			if q.Slug == ans.QuestionSlug {
				switch q.Type {
				case "multiple_choice":
					if ans.AnswerIndex != nil && *ans.AnswerIndex == q.CorrectAnswer {
						aa.IsCorrect = true
						correctCount++
					}
				case "short_answer":
					if ans.AnswerText == q.CorrectText {
						aa.IsCorrect = true
						correctCount++
					}
				}
				break
			}
		}
		attemptAnswers = append(attemptAnswers, aa)
	}

	score := 0
	if totalQuestions > 0 {
		score = (correctCount * 100) / totalQuestions
	}
	passed := score >= quiz.PassThreshold

	attempt := &pkgmodels.QuizAttempt{
		Id:            bson.NewObjectId(),
		TenantID:      tenantID,
		ContactID:     contactID,
		QuizID:        quiz.Id,
		EnrollmentID:  enrollment.Id,
		Answers:       attemptAnswers,
		Score:         score,
		Passed:        passed,
		AttemptNumber: attemptCount + 1,
		SubmittedAt:   time.Now(),
	}

	queries.CreateQuizAttempt(attempt)

	if passed {
		for _, p := range enrollment.Progress {
			if p.ModuleSlug == quiz.ModuleSlug {
				trueVal := true
				progressUpdate := &pkgmodels.LessonProgress{
					LessonSlug:      p.LessonSlug,
					ModuleSlug:      p.ModuleSlug,
					WatchPercent:    p.WatchPercent,
					LastPositionSec: p.LastPositionSec,
					Completed:       p.Completed,
					CompletedAt:     p.CompletedAt,
					QuizPassed:      &trueVal,
				}
				if p.WatchPercent >= 90 && !p.Completed {
					now := time.Now()
					progressUpdate.Completed = true
					progressUpdate.CompletedAt = &now
				}
				queries.UpdateLessonProgress(tenantID, enrollment.PublicId, progressUpdate)
			}
		}
	}

	type answerResult struct {
		QuestionSlug string `json:"question_slug"`
		IsCorrect    bool   `json:"is_correct"`
	}
	results := make([]answerResult, 0, len(attemptAnswers))
	for _, a := range attemptAnswers {
		results = append(results, answerResult{
			QuestionSlug: a.QuestionSlug,
			IsCorrect:    a.IsCorrect,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"score":          score,
		"passed":         passed,
		"attempt_number": attempt.AttemptNumber,
		"results":        results,
	})
}

func handleListQuizAttempts(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	quizId := c.Param("quizId")
	quiz, err := queries.GetLMSQuizByPublicId(tenantID, quizId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "quiz not found"})
		return
	}

	contactIdStr := c.DefaultQuery("contact_id", "")
	if contactIdStr == "" || !bson.IsObjectIdHex(contactIdStr) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contact_id query parameter required"})
		return
	}
	contactID := bson.ObjectIdHex(contactIdStr)

	attempts, err := queries.ListQuizAttempts(tenantID, quiz.Id, contactID)
	if err != nil {
		log.Printf("[LMS] Error listing quiz attempts: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list attempts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"attempts": attempts})
}

// ---------- Certificate Handlers ----------

func handleListLMSCertificates(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 100 {
		limit = 100
	}

	var contactID *bson.ObjectId
	if cid := c.DefaultQuery("contact_id", ""); cid != "" && bson.IsObjectIdHex(cid) {
		oid := bson.ObjectIdHex(cid)
		contactID = &oid
	}

	certs, err := queries.ListCertificates(tenantID, contactID, skip, limit)
	if err != nil {
		log.Printf("[LMS] Error listing certificates: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list certificates"})
		return
	}

	type certItem struct {
		PublicId        string `json:"public_id"`
		ContactName     string `json:"contact_name"`
		CourseTitle     string `json:"course_title"`
		ProductPublicId string `json:"product_public_id"`
		GenStatus       string `json:"gen_status"`
		AssetURL        string `json:"asset_url,omitempty"`
		CompletedAt     string `json:"completed_at"`
		IssuedAt        string `json:"issued_at,omitempty"`
	}

	items := make([]certItem, 0, len(certs))
	for _, cert := range certs {
		item := certItem{
			PublicId:        cert.PublicId,
			ContactName:     cert.ContactName,
			CourseTitle:     cert.CourseTitle,
			ProductPublicId: cert.ProductPublicId,
			GenStatus:       cert.GenStatus,
			AssetURL:        cert.AssetURL,
			CompletedAt:     cert.CompletedAt.Format(time.RFC3339),
		}
		if cert.IssuedAt != nil {
			item.IssuedAt = cert.IssuedAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"certificates": items, "total": len(items)})
}

func handleGetLMSCertificate(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	certId := c.Param("certId")
	cert, err := queries.GetCertificateByPublicId(tenantID, certId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "certificate not found"})
		return
	}

	c.JSON(http.StatusOK, cert)
}

func handleRegenerateCertificate(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	certId := c.Param("certId")
	_, err := queries.UpdateCertificate(tenantID, certId, bson.M{
		"gen_status":    "pending",
		"asset_id":      nil,
		"asset_url":     "",
		"gen_error_msg": "",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to regenerate certificate"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "regeneration queued"})
}

// ---------- Generation Handlers ----------

func handleGenerateOutline(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")

	// Validate the course exists
	_, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	var req struct {
		Prompt       string   `json:"prompt"`
		Audience     string   `json:"audience"`
		Outcome      string   `json:"outcome"`
		Tone         string   `json:"tone"`
		ModuleCount  int      `json:"module_count"`
		Quizzes      bool     `json:"quizzes_enabled"`
		Certificate  bool     `json:"cert_enabled"`
		DefaultMedia string   `json:"default_media"`
		ExtraContext string   `json:"extra_context"`
		ReferenceIds []string `json:"reference_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	estimatedInput := int64(len(req.Prompt) + len(req.Audience) + len(req.Outcome) + len(req.Tone) + len(req.ExtraContext))
	op, opCtx, cancel, admitted := admitLMSAI(c, tenantID, "lms.outline", estimatedInput, 4096)
	if !admitted {
		return
	}
	defer cancel()

	// Delegate to shared GenerationService
	genSvc := services.NewGenerationService()
	job, outline, err := genSvc.GenerateOutlineContext(
		opCtx,
		tenantID,
		courseId,
		req.Prompt,
		req.Audience,
		req.Outcome,
		req.Tone,
		req.ModuleCount,
		req.Quizzes,
		req.Certificate,
		req.DefaultMedia,
		req.ExtraContext,
		req.ReferenceIds,
	)
	settleLMSAI(op, err)
	if err != nil {
		log.Printf("[LMS] Error generating outline: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate outline"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"job":     job,
		"outline": outline,
	})
}

func handleGenerateFullCourse(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	var req struct {
		JobId          string `json:"job_id"`
		OutlineJSON    string `json:"outline_json"`
		DefaultMedia   string `json:"default_media"`
		QuizzesEnabled *bool  `json:"quizzes_enabled"`
		CertEnabled    *bool  `json:"cert_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Derive final config values — start with request values
	finalOutlineJSON := req.OutlineJSON
	finalDefaultMedia := req.DefaultMedia
	finalQuizzesEnabled := false
	finalCertEnabled := false
	if req.QuizzesEnabled != nil {
		finalQuizzesEnabled = *req.QuizzesEnabled
	}
	if req.CertEnabled != nil {
		finalCertEnabled = *req.CertEnabled
	}

	// If a job ID was provided, always fetch the existing job to preserve config
	if req.JobId != "" {
		existingJob, err := queries.GetGenerationJobByPublicId(tenantID, req.JobId)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "generation job not found"})
			return
		}
		// Use existing outline if request didn't provide one
		if finalOutlineJSON == "" {
			finalOutlineJSON = existingJob.OutlineJSON
		}
		// Preserve existing config when request didn't explicitly set values
		if finalDefaultMedia == "" {
			finalDefaultMedia = existingJob.DefaultMedia
		}
		if req.QuizzesEnabled == nil {
			finalQuizzesEnabled = existingJob.QuizzesEnabled
		}
		if req.CertEnabled == nil {
			finalCertEnabled = existingJob.CertEnabled
		}
	}

	if finalOutlineJSON == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "outline_json is required (either directly or via job_id)"})
		return
	}

	// Parse the outline
	var outline services.CourseOutline
	if err := json.Unmarshal([]byte(finalOutlineJSON), &outline); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid outline JSON: " + err.Error()})
		return
	}
	lessonCount := 0
	for _, module := range outline.Modules {
		lessonCount += len(module.Lessons)
	}
	estimatedOutput := int64(lessonCount*1500 + len(outline.Modules)*1000)
	if estimatedOutput < 4096 {
		estimatedOutput = 4096
	}
	op, opCtx, cancel, admitted := admitLMSAI(c, tenantID, "lms.course.materialize", int64(len(finalOutlineJSON)), estimatedOutput)
	if !admitted {
		return
	}
	defer cancel()

	// Delegate to shared GenerationService
	genSvc := services.NewGenerationService()
	job, err := genSvc.MaterializeCourseContext(
		opCtx,
		tenantID,
		courseId,
		req.JobId,
		outline,
		finalDefaultMedia,
		finalQuizzesEnabled,
		finalCertEnabled,
	)
	settleLMSAI(op, err)
	if err != nil {
		log.Printf("[LMS] Error materializing course: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate full course: " + err.Error()})
		return
	}

	// Fetch the updated course to return alongside the job
	updatedProduct, _ := queries.GetCourseProductByPublicId(tenantID, courseId)

	c.JSON(http.StatusOK, gin.H{
		"job":    job,
		"course": updatedProduct,
	})
}

func handleEditCoursePrompt(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")

	// Validate the course exists
	_, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	var req struct {
		Prompt     string `json:"prompt" binding:"required"`
		TargetType string `json:"target_type"`
		TargetId   string `json:"target_id"`
		Scope      string `json:"scope"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Delegate to shared PatchService
	patchSvc := services.NewPatchService()
	patch, err := patchSvc.CreateEditPatch(
		tenantID,
		courseId,
		req.Prompt,
		req.TargetType,
		req.TargetId,
		req.Scope,
	)
	if err != nil {
		log.Printf("[LMS] Error creating edit patch: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create edit patch"})
		return
	}

	c.JSON(http.StatusCreated, patch)
}

func handleListGenerationJobs(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	jobs, err := queries.ListGenerationJobs(tenantID, courseId, skip, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list generation jobs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

func handleGetGenerationJob(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	jobId := c.Param("jobId")
	job, err := queries.GetGenerationJobByPublicId(tenantID, jobId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	c.JSON(http.StatusOK, job)
}

// ---------- Patch Handlers ----------

func handleListPatches(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	status := c.DefaultQuery("status", "")
	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	patches, err := queries.ListContentPatches(tenantID, courseId, status, skip, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list patches"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"patches": patches})
}

func handleGetPatch(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	patchId := c.Param("patchId")
	patch, err := queries.GetContentPatchByPublicId(tenantID, patchId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "patch not found"})
		return
	}

	c.JSON(http.StatusOK, patch)
}

func handleApprovePatch(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	patchId := c.Param("patchId")

	// Delegate to shared PatchService
	patchSvc := services.NewPatchService()
	updated, err := patchSvc.ApplyPatch(tenantID, patchId)
	if err != nil {
		log.Printf("[LMS] Error applying patch: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, updated)
}

func handleRejectPatch(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	patchId := c.Param("patchId")
	updated, err := queries.UpdateContentPatch(tenantID, patchId, bson.M{
		"status": "rejected",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reject patch"})
		return
	}

	c.JSON(http.StatusOK, updated)
}

// ---------- Reference Handlers ----------

func handleUploadReference(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	product, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	var req struct {
		FileName string `json:"file_name" binding:"required"`
		FileType string `json:"file_type" binding:"required"`
		Content  string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Delegate to shared ReferenceService
	refSvc := services.NewReferenceService()
	created, err := refSvc.IngestText(tenantID, product.Id, req.FileName, req.FileType, req.Content)
	if err != nil {
		log.Printf("[LMS] Error ingesting reference: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create reference"})
		return
	}

	c.JSON(http.StatusCreated, created)
}

func handleListReferences(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	product, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	refs, err := queries.ListSourceReferences(tenantID, product.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list references"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"references": refs})
}

func handleDeleteReference(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	refId := c.Param("refId")
	err := queries.DeleteSourceReference(tenantID, refId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete reference"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Reference deleted"})
}

// ---------- Certificate Template Handlers ----------

func handleGetCertificateTemplate(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	product, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	tmpl, err := queries.GetCertificateTemplateByProduct(tenantID, product.Id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"enabled":       false,
			"template_name": "default",
			"title":         product.Name + " Certificate",
		})
		return
	}

	c.JSON(http.StatusOK, tmpl)
}

func handleUpdateCertificateTemplate(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	courseId := c.Param("courseId")
	product, err := queries.GetCourseProductByPublicId(tenantID, courseId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	var req struct {
		Enabled      *bool  `json:"enabled"`
		TemplateName string `json:"template_name"`
		Title        string `json:"title"`
		LogoURL      string `json:"logo_url"`
		AccentColor  string `json:"accent_color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	existing, err := queries.GetCertificateTemplateByProduct(tenantID, product.Id)
	if err != nil {
		tmpl := pkgmodels.NewCertificateTemplate(tenantID, product.Id, "", product.PublicId, product.Name+" Certificate")
		if req.Enabled != nil {
			tmpl.Enabled = *req.Enabled
		}
		if req.TemplateName != "" {
			tmpl.TemplateName = req.TemplateName
		}
		if req.Title != "" {
			tmpl.Title = req.Title
		}
		tmpl.LogoURL = req.LogoURL
		tmpl.AccentColor = req.AccentColor

		created, err := queries.CreateCertificateTemplate(tmpl)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create certificate template"})
			return
		}
		c.JSON(http.StatusCreated, created)
		return
	}

	update := bson.M{}
	if req.Enabled != nil {
		update["enabled"] = *req.Enabled
	}
	if req.TemplateName != "" {
		update["template_name"] = req.TemplateName
	}
	if req.Title != "" {
		update["title"] = req.Title
	}
	if req.LogoURL != "" {
		update["logo_url"] = req.LogoURL
	}
	if req.AccentColor != "" {
		update["accent_color"] = req.AccentColor
	}

	updated, err := queries.UpdateCertificateTemplate(tenantID, existing.PublicId, update)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update certificate template"})
		return
	}

	c.JSON(http.StatusOK, updated)
}

// ---------- Quiz CRUD Handlers ----------

func handleCreateQuiz(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		ProductPublicId string `json:"product_public_id" binding:"required"`
		ModuleSlug      string `json:"module_slug" binding:"required"`
		Slug            string `json:"slug" binding:"required"`
		Title           string `json:"title" binding:"required"`
		PassThreshold   int    `json:"pass_threshold"`
		MaxAttempts     int    `json:"max_attempts"`
		Questions       []struct {
			Slug          string   `json:"slug"`
			Type          string   `json:"type"`
			Title         string   `json:"title"`
			Options       []string `json:"options"`
			CorrectAnswer int      `json:"correct_answer"`
			CorrectText   string   `json:"correct_text"`
			Order         int      `json:"order"`
		} `json:"questions"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	product, err := queries.GetCourseProductByPublicId(tenantID, req.ProductPublicId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	quiz := pkgmodels.NewLMSQuiz()
	quiz.TenantID = tenantID
	quiz.ProductID = product.Id
	quiz.ModuleSlug = req.ModuleSlug
	quiz.Slug = req.Slug
	quiz.Title = req.Title
	quiz.PassThreshold = req.PassThreshold
	quiz.MaxAttempts = req.MaxAttempts

	if quiz.PassThreshold == 0 {
		quiz.PassThreshold = 70
	}
	if quiz.MaxAttempts == 0 {
		quiz.MaxAttempts = 3
	}

	for _, q := range req.Questions {
		question := &pkgmodels.LMSQuizQuestion{
			Slug:          q.Slug,
			Type:          q.Type,
			Title:         q.Title,
			Options:       q.Options,
			CorrectAnswer: q.CorrectAnswer,
			CorrectText:   q.CorrectText,
			Order:         q.Order,
		}
		quiz.Questions = append(quiz.Questions, question)
	}

	quiz.SetCreated()
	created, err := queries.CreateLMSQuiz(quiz)
	if err != nil {
		log.Printf("[LMS] Error creating quiz: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create quiz"})
		return
	}

	c.JSON(http.StatusCreated, created)
}

func handleUpdateQuiz(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	quizId := c.Param("quizId")
	quiz, err := queries.GetLMSQuizByPublicId(tenantID, quizId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "quiz not found"})
		return
	}

	var req struct {
		Title         *string `json:"title,omitempty"`
		PassThreshold *int    `json:"pass_threshold,omitempty"`
		MaxAttempts   *int    `json:"max_attempts,omitempty"`
		Questions     []struct {
			Slug          string   `json:"slug"`
			Type          string   `json:"type"`
			Title         string   `json:"title"`
			Options       []string `json:"options"`
			CorrectAnswer int      `json:"correct_answer"`
			CorrectText   string   `json:"correct_text"`
			Order         int      `json:"order"`
		} `json:"questions,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	update := bson.M{}
	if req.Title != nil {
		update["title"] = *req.Title
	}
	if req.PassThreshold != nil {
		update["pass_threshold"] = *req.PassThreshold
	}
	if req.MaxAttempts != nil {
		update["max_attempts"] = *req.MaxAttempts
	}
	if req.Questions != nil {
		var questions []*pkgmodels.LMSQuizQuestion
		for _, q := range req.Questions {
			questions = append(questions, &pkgmodels.LMSQuizQuestion{
				Slug:          q.Slug,
				Type:          q.Type,
				Title:         q.Title,
				Options:       q.Options,
				CorrectAnswer: q.CorrectAnswer,
				CorrectText:   q.CorrectText,
				Order:         q.Order,
			})
		}
		update["questions"] = questions
	}

	update["timestamps.updated_at"] = time.Now()
	err = db.GetCollection(pkgmodels.LMSQuizCollection).Update(
		bson.M{"_id": quiz.Id},
		bson.M{"$set": update},
	)
	if err != nil {
		log.Printf("[LMS] Error updating quiz: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update quiz"})
		return
	}

	updatedQuiz, _ := queries.GetLMSQuizByPublicId(tenantID, quizId)
	c.JSON(http.StatusOK, updatedQuiz)
}

func handleDeleteQuiz(c *gin.Context) {
	tenantID := auth.GetTenantObjectID(c)
	if tenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	quizId := c.Param("quizId")
	quiz, err := queries.GetLMSQuizByPublicId(tenantID, quizId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "quiz not found"})
		return
	}

	now := time.Now()
	db.GetCollection(pkgmodels.LMSQuizCollection).Update(
		bson.M{"_id": quiz.Id},
		bson.M{"$set": bson.M{"timestamps.deleted_at": now}},
	)

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
