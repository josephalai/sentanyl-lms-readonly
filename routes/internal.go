package routes

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/pkg/db"
	"github.com/josephalai/sentanyl/pkg/i18n"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// EnsureEnrollmentIndexes enforces the DEL-007 idempotency invariant: one
// course enrollment per purchased line item, races settled at insert.
func EnsureEnrollmentIndexes() {
	if err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).EnsureIndex(mgo.Index{
		Key:        []string{"purchase_item_id"},
		Unique:     true,
		Sparse:     true,
		Background: true,
	}); err != nil {
		log.Printf("lms: course enrollment purchase-item index: %v", err)
	}
}

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
	// OfferID/PurchaseItemID carry the commercial provenance (DEL-007/008):
	// the purchase item is the idempotency key so repurchases enroll again.
	OfferID        string `json:"offer_id,omitempty"`
	PurchaseItemID string `json:"purchase_item_id,omitempty"`
	Source         string `json:"source,omitempty"`
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

	// Confirm the product exists and fetch its public id + type. Provisioning
	// branches on ProductType so a single offer can mix course, coaching,
	// service, and digital_download products without per-product webhook
	// dispatch in marketing-service.
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

	switch product.ProductType {
	case pkgmodels.ProductTypeService:
		enrollServiceProduct(c, tenantID, contactID, &product)
	case pkgmodels.ProductTypeCoaching:
		enrollCoachingProduct(c, tenantID, contactID, &product)
	case pkgmodels.ProductTypeDigitalDownload:
		// Entitlement is granted via badge mapping on the offer; no per-product
		// enrollment row is required to unlock downloads.
		c.JSON(http.StatusOK, gin.H{"status": "ok", "skipped": "digital_download"})
	default:
		var offerID, purchaseItemID bson.ObjectId
		if bson.IsObjectIdHex(req.OfferID) {
			offerID = bson.ObjectIdHex(req.OfferID)
		}
		if bson.IsObjectIdHex(req.PurchaseItemID) {
			purchaseItemID = bson.ObjectIdHex(req.PurchaseItemID)
		}
		enrollCourseProduct(c, tenantID, contactID, &product, req.EnrollmentBadge, offerID, purchaseItemID, req.Source)
	}
}

// enrollCoachingProduct provisions a CoachingEnrollment so a buyer of a
// coaching offer immediately sees the program in their library and can book
// sessions. SessionsTotal is derived from the program's SessionTemplates so a
// 3-session program yields a 3-session enrollment. Idempotent on
// (tenant_id, contact_id, product_id).
func enrollCoachingProduct(c *gin.Context, tenantID, contactID bson.ObjectId, product *pkgmodels.Product) {
	filter := bson.M{
		"tenant_id":  tenantID,
		"contact_id": contactID,
		"product_id": product.Id,
	}
	var existing pkgmodels.CoachingEnrollment
	if err := db.GetCollection(pkgmodels.CoachingEnrollmentCollection).Find(filter).One(&existing); err == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": existing.Id.Hex(), "created": false})
		return
	}
	sessionsTotal := 0
	if product.Coaching != nil {
		sessionsTotal = len(product.Coaching.SessionTemplates)
	}
	enrollment := pkgmodels.NewCoachingEnrollment(tenantID, contactID, product.Id, product.PublicId, sessionsTotal)
	if err := db.GetCollection(pkgmodels.CoachingEnrollmentCollection).Insert(enrollment); err != nil {
		log.Printf("[LMS] Coaching enroll error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enroll"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": enrollment.Id.Hex(), "created": true, "sessions_total": sessionsTotal})
}

// enrollCourseProduct upserts a CourseEnrollment for course-shaped products.
//
// DEL-007: when the caller supplies a purchase_item_id, idempotency is keyed
// on it — a webhook retry reuses the enrollment while a genuine repurchase
// (new item) enrolls again. Legacy callers without one keep the old
// (tenant, contact, product) collapse. DEL-008: the offer/item/source
// provenance is stored on the enrollment row.
func enrollCourseProduct(c *gin.Context, tenantID, contactID bson.ObjectId, product *pkgmodels.Product, enrollmentBadge string, offerID, purchaseItemID bson.ObjectId, source string) {
	col := db.GetCollection(pkgmodels.CourseEnrollmentCollection)
	var filter bson.M
	if purchaseItemID.Valid() {
		filter = bson.M{"purchase_item_id": purchaseItemID}
	} else {
		filter = bson.M{
			"tenant_id":  tenantID,
			"contact_id": contactID,
			"product_id": product.Id,
		}
	}
	var existing pkgmodels.CourseEnrollment
	if err := col.Find(filter).One(&existing); err == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": existing.Id.Hex(), "created": false})
		return
	}
	enrollment := pkgmodels.NewCourseEnrollment(tenantID, contactID, product.Id, product.PublicId, enrollmentBadge)
	enrollment.OfferID = offerID
	enrollment.PurchaseItemID = purchaseItemID
	enrollment.Source = source
	if enrollment.Source == "" && purchaseItemID.Valid() {
		enrollment.Source = "purchase"
	}
	if err := col.Insert(enrollment); err != nil {
		if mgo.IsDup(err) {
			// Concurrent replay of the same purchase item — treat as reuse.
			if ferr := col.Find(filter).One(&existing); ferr == nil {
				c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": existing.Id.Hex(), "created": false})
				return
			}
		}
		log.Printf("[LMS] Internal enroll error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enroll"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": enrollment.Id.Hex(), "created": true})
}

// enrollServiceProduct provisions the ServiceEnrollment + N pending
// ServiceInstance rows that the tenant fulfills asynchronously. Each instance
// mirrors one ServiceInstanceTemplate on the product and starts in pending
// status. Idempotent on (tenant_id, contact_id, product_id).
func enrollServiceProduct(c *gin.Context, tenantID, contactID bson.ObjectId, product *pkgmodels.Product) {
	cfg := product.Service
	if cfg == nil {
		cfg = &pkgmodels.ServiceConfig{}
	}
	templates := cfg.InstanceTemplates

	filter := bson.M{
		"tenant_id":  tenantID,
		"contact_id": contactID,
		"product_id": product.Id,
	}
	var existing pkgmodels.ServiceEnrollment
	if err := db.GetCollection(pkgmodels.ServiceEnrollmentCollection).Find(filter).One(&existing); err == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": existing.Id.Hex(), "created": false})
		return
	}

	enrollment := pkgmodels.NewServiceEnrollment(tenantID, contactID, product.Id, product.PublicId, len(templates))
	if err := db.GetCollection(pkgmodels.ServiceEnrollmentCollection).Insert(enrollment); err != nil {
		log.Printf("[LMS] Service enroll error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enroll"})
		return
	}

	for i, t := range templates {
		title := t.Title
		if title == "" {
			title = fmt.Sprintf("Session %d", i+1)
		}
		instance := pkgmodels.NewServiceInstance(tenantID, product.Id, enrollment.Id, contactID, t.Id, t.Order, title)
		if err := db.GetCollection(pkgmodels.ServiceInstanceCollection).Insert(instance); err != nil {
			log.Printf("[LMS] Service instance insert error: %v", err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "enrollment_id": enrollment.Id.Hex(), "created": true, "instances": len(templates)})
}
