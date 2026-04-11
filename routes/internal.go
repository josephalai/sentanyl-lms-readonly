package routes

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	
	"github.com/josephalai/sentanyl/pkg/db"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// RegisterInternalRoutes registers internal-only routes (no auth).
func RegisterInternalRoutes(internal *gin.RouterGroup) {
	internal.POST("/hydrate-lms", HandleInternalHydrateCourse)
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
