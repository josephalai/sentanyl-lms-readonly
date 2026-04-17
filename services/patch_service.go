package services

import (
	"fmt"
	"log"
	"time"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/queries"
	"github.com/josephalai/sentanyl/pkg/db"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// PatchService handles structured patch creation, validation, and application.
type PatchService struct{}

// NewPatchService creates a PatchService.
func NewPatchService() *PatchService {
	return &PatchService{}
}

// CreateEditPatch generates a structured patch from an edit prompt.
// In production, this would call an LLM to produce targeted patch operations.
// The current implementation creates a deterministic patch based on the prompt.
func (s *PatchService) CreateEditPatch(
	tenantID bson.ObjectId,
	productPublicId string,
	prompt string,
	targetType string,
	targetId string,
	scope string,
) (*pkgmodels.ContentPatch, error) {
	if targetType == "" {
		targetType = "course"
	}
	if targetId == "" {
		targetId = productPublicId
	}

	patch := pkgmodels.NewContentPatch(tenantID, "", productPublicId, targetType, targetId)
	patch.Prompt = prompt

	// In production, this would be LLM-generated structured operations.
	// For now, create a meaningful placeholder operation based on the target.
	patch.Operations = []pkgmodels.PatchOperation{
		{
			Op:    "replace",
			Path:  fmt.Sprintf("/%s/%s", targetType, targetId),
			Value: fmt.Sprintf("Updated content based on: %s", prompt),
		},
	}

	created, err := queries.CreateContentPatch(patch)
	if err != nil {
		return nil, fmt.Errorf("failed to create patch: %w", err)
	}
	return created, nil
}

// ApplyPatch applies a patch to the course tree and records a revision event.
func (s *PatchService) ApplyPatch(
	tenantID bson.ObjectId,
	patchPublicId string,
) (*pkgmodels.ContentPatch, error) {
	patch, err := queries.GetContentPatchByPublicId(tenantID, patchPublicId)
	if err != nil {
		return nil, fmt.Errorf("patch not found: %w", err)
	}

	if patch.Status != "pending" {
		return nil, fmt.Errorf("patch is not in pending state (current: %s)", patch.Status)
	}

	product, err := queries.GetCourseProductByPublicId(tenantID, patch.ProductPublicId)
	if err != nil {
		return nil, fmt.Errorf("course not found: %w", err)
	}

	snapshotBefore := product.CourseModules

	// Apply each operation
	for _, op := range patch.Operations {
		applyOperation(product, op)
	}

	// Persist updated tree
	updateDoc := bson.M{
		"course_modules":        product.CourseModules,
		"timestamps.updated_at": time.Now(),
	}
	err = db.GetCollection(pkgmodels.ProductCollection).Update(
		bson.M{"_id": product.Id},
		bson.M{"$set": updateDoc},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to apply patch to course: %w", err)
	}

	// Record revision event
	revEvent := pkgmodels.NewCourseRevisionEvent(tenantID, patch.ProductPublicId, "patch_apply",
		fmt.Sprintf("Applied patch %s (%s/%s)", patchPublicId, patch.TargetType, patch.TargetId))
	revEvent.PatchPublicId = patchPublicId
	revEvent.SnapshotBefore = snapshotBefore
	revEvent.SnapshotAfter = product.CourseModules
	queries.CreateCourseRevisionEvent(revEvent)

	// Mark patch as applied
	now := time.Now()
	updated, err := queries.UpdateContentPatch(tenantID, patchPublicId, bson.M{
		"status":     "applied",
		"applied_at": now,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update patch status: %w", err)
	}

	return updated, nil
}

// applyOperation applies a single patch operation to the product.
func applyOperation(product *pkgmodels.Product, op pkgmodels.PatchOperation) {
	switch op.Op {
	case "replace":
		switch op.Path {
		case "/title", "/name":
			if v, ok := op.Value.(string); ok {
				product.Name = v
			}
		case "/description":
			if v, ok := op.Value.(string); ok {
				product.Description = v
			}
		default:
			// Handle module/lesson-level patches
			for _, mod := range product.CourseModules {
				if op.Path == "/module/"+mod.Slug+"/title" {
					if v, ok := op.Value.(string); ok {
						mod.Title = v
					}
					return
				}
				for _, lesson := range mod.Lessons {
					prefix := "/module/" + mod.Slug + "/lesson/" + lesson.Slug
					switch op.Path {
					case prefix + "/title":
						if v, ok := op.Value.(string); ok {
							lesson.Title = v
						}
						return
					case prefix + "/content_markdown":
						if v, ok := op.Value.(string); ok {
							lesson.ContentMarkdown = v
						}
						return
					case prefix + "/content_html":
						if v, ok := op.Value.(string); ok {
							lesson.ContentHTML = v
						}
						return
					case prefix + "/video_mode":
						if v, ok := op.Value.(string); ok {
							lesson.VideoMode = pkgmodels.VideoMode(v)
						}
						return
					}
				}
			}
		}
	}
}

func init() {
	log.Println("[LMS] Patch service initialized")
}
