package routes

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/pkg/db"
	"github.com/josephalai/sentanyl/pkg/i18n"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// RegisterInternalRoutes registers internal-only routes (no auth).
func RegisterInternalRoutes(internal *gin.RouterGroup) {
	internal.POST("/hydrate-lms", HandleInternalHydrateCourse)
	internal.POST("/enroll", HandleInternalEnroll)
	internal.POST("/certificates", HandleInternalIssueCertificate)
}

// HandleInternalIssueCertificate idempotently creates a Certificate for a
// completed enrollment. Called by marketing-service when a contact's overall
// progress crosses 100%. Idempotent on enrollment_id — re-issuing returns the
// existing cert.
type internalIssueCertRequest struct {
	EnrollmentID string `json:"enrollment_id" binding:"required"`
	CompletedAt  string `json:"completed_at,omitempty"`
}

func HandleInternalIssueCertificate(c *gin.Context) {
	var req internalIssueCertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !bson.IsObjectIdHex(req.EnrollmentID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid enrollment_id"})
		return
	}
	enrollmentID := bson.ObjectIdHex(req.EnrollmentID)

	var existing pkgmodels.Certificate
	if err := db.GetCollection(pkgmodels.CertificateCollection).Find(bson.M{
		"enrollment_id": enrollmentID,
	}).One(&existing); err == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "certificate_id": existing.Id.Hex(), "public_id": existing.PublicId, "created": false})
		return
	}

	var enrollment pkgmodels.CourseEnrollment
	if err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).FindId(enrollmentID).One(&enrollment); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "enrollment not found"})
		return
	}
	var product pkgmodels.Product
	if err := db.GetCollection(pkgmodels.ProductCollection).FindId(enrollment.ProductID).One(&product); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "product not found"})
		return
	}
	var contact pkgmodels.User
	contactName := ""
	contactLocale := ""
	if err := db.GetCollection(pkgmodels.UserCollection).FindId(enrollment.ContactID).One(&contact); err == nil {
		contactName = strings.TrimSpace(strings.TrimSpace(contact.Name.First) + " " + strings.TrimSpace(contact.Name.Last))
		if contactName == "" {
			contactName = string(contact.Email)
		}
		contactLocale = i18n.Normalize(contact.PreferredLocale)
	}

	// Snapshot the localized course title at issue time. The hydrator runs
	// out of request context so it can't resolve translations later.
	courseTitle := product.Name
	if contactLocale != "" && len(product.Translations) > 0 {
		if t, ok := product.Translations[contactLocale]; ok && t != nil && t.Title != "" {
			courseTitle = t.Title
		} else if i := strings.Index(contactLocale, "-"); i > 0 {
			if t, ok := product.Translations[contactLocale[:i]]; ok && t != nil && t.Title != "" {
				courseTitle = t.Title
			}
		}
	}

	completedAt := time.Now()
	if req.CompletedAt != "" {
		if t, err := time.Parse(time.RFC3339, req.CompletedAt); err == nil {
			completedAt = t
		}
	} else if enrollment.CompletedAt != nil {
		completedAt = *enrollment.CompletedAt
	}

	cert := pkgmodels.NewCertificate(
		enrollment.TenantID,
		enrollment.ContactID,
		enrollment.ProductID,
		enrollment.Id,
		product.PublicId,
		contactName,
		courseTitle,
		"default",
		completedAt,
	)
	cert.Locale = contactLocale
	// "pending" puts the cert in the hydrator's render queue (core-service
	// processPendingCertificates). It will flip to "completed" once asset_url
	// is populated. issued_at marks when the cert was officially earned and
	// is set immediately so the library banner shows even before render.
	cert.GenStatus = "pending"
	now := time.Now()
	cert.IssuedAt = &now
	if err := db.GetCollection(pkgmodels.CertificateCollection).Insert(cert); err != nil {
		log.Printf("[LMS] Internal certificate error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue certificate"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "certificate_id": cert.Id.Hex(), "public_id": cert.PublicId, "created": true})
}

// HandleInternalHydrateCourse receives a hydrated course payload from the
// compiler and inserts it into the products collection.
func HandleInternalHydrateCourse(c *gin.Context) {
	var product pkgmodels.Product
	if err := c.ShouldBindJSON(&product); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := db.GetCollection(pkgmodels.ProductCollection).Insert(product)
	if err != nil {
		log.Printf("[LMS] Internal hydrate error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to insert course"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "public_id": product.PublicId})
}

// HandleInternalEnroll idempotently enrolls a contact into a course product.
// Called by the marketing-service Stripe webhook after a successful purchase.
type internalEnrollRequest struct {
	TenantID        string `json:"tenant_id" binding:"required"`
	ContactID       string `json:"contact_id" binding:"required"`
	ProductID       string `json:"product_id" binding:"required"`
	EnrollmentBadge string `json:"enrollment_badge,omitempty"`
}

func HandleInternalEnroll(c *gin.Context) {
	var req internalEnrollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !bson.IsObjectIdHex(req.TenantID) || !bson.IsObjectIdHex(req.ContactID) || !bson.IsObjectIdHex(req.ProductID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	tenantID := bson.ObjectIdHex(req.TenantID)
	contactID := bson.ObjectIdHex(req.ContactID)
	productID := bson.ObjectIdHex(req.ProductID)

	// Confirm the product exists and fetch its public id.
	var product pkgmodels.Product
	err := db.GetCollection(pkgmodels.ProductCollection).Find(bson.M{
		"_id":                   productID,
		"tenant_id":             tenantID,
		"timestamps.deleted_at": nil,
	}).One(&product)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "product not found"})
		return
	}

	// Upsert CourseEnrollment keyed on (tenant_id, contact_id, product_id).
	filter := bson.M{
		"tenant_id":  tenantID,
		"contact_id": contactID,
		"product_id": productID,
	}
	var existing pkgmodels.CourseEnrollment
	findErr := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Find(filter).One(&existing)
	if findErr == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": existing.Id.Hex(), "created": false})
		return
	}

	enrollment := pkgmodels.NewCourseEnrollment(tenantID, contactID, productID, product.PublicId, req.EnrollmentBadge)
	if err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Insert(enrollment); err != nil {
		log.Printf("[LMS] Internal enroll error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enroll"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": enrollment.Id.Hex(), "created": true})
}
