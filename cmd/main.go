package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/josephalai/sentanyl/lms-service/routes"
	"github.com/josephalai/sentanyl/pkg/audit"
	"github.com/josephalai/sentanyl/pkg/auth"
	"github.com/josephalai/sentanyl/pkg/config"
	"github.com/josephalai/sentanyl/pkg/db"
	httputil "github.com/josephalai/sentanyl/pkg/http"
)

func main() {
	log.Println("lms-service: starting up")

	// Load config from .env if present.
	if _, err := os.Stat(".env"); err == nil {
		configVals := config.LoadConfigFile(config.ConfigFile)
		config.MapConfigValues(configVals)
	}

	// Determine port (default 8083 for lms-service).
	port := envOrDefault("LMS_SERVICE_PORT", "8083")

	// Connect to MongoDB.
	db.MongoHost = envOrDefault("MONGO_HOST", "localhost")
	db.MongoPort = envOrDefault("MONGO_PORT", "27017")
	db.MongoDB = envOrDefault("MONGO_DB", "sentanyl")
	db.MongoDefaultCollectionName = "products"
	db.UsingLocalMongo = true
	db.InitMongoConnection()
	routes.EnsureEnrollmentIndexes()

	// Set up Gin router.
	r := gin.Default()
	audit.Init("lms-service")
	r.Use(httputil.CORSMiddleware())
	r.Use(audit.Middleware())

	r.GET("/health", httputil.HealthHandler("lms-service"))

	// Protected LMS routes (require JWT).
	// Routes register under /api/lms/* matching the Caddy gateway prefix.
	lmsAPI := r.Group("/api")
	lmsAPI.Use(auth.RequireTenantAuth(), auth.RequirePlatformSubscription())
	routes.RegisterLMSRoutes(lmsAPI)

	// Internal routes: service-to-service only, authenticated by a signed
	// service token (API-001 / DEL-006). Network position is not identity.
	internal := r.Group("/internal")
	internal.Use(auth.RequireServiceAuth())
	routes.RegisterInternalRoutes(internal)

	log.Printf("lms-service: listening on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("lms-service: failed to start: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
