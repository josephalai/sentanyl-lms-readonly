package routes

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/pkg/db"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// RegisterInternalRoutes registers internal-only routes (no auth).
func RegisterInternalRoutes(internal *gin.RouterGroup) {
	internal.POST("/hydrate-lms", HandleInternalHydrateCourse)
	internal.POST("/enroll", HandleInternalEnroll)
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
