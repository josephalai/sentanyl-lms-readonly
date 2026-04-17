package services

import (
	"log"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/queries"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// CourseRevisionService manages audit trail and revision events.
type CourseRevisionService struct{}

// NewCourseRevisionService creates a CourseRevisionService.
func NewCourseRevisionService() *CourseRevisionService {
	return &CourseRevisionService{}
}

// RecordRevision creates a course revision event for auditing.
func (s *CourseRevisionService) RecordRevision(
	tenantID bson.ObjectId,
	productPublicId string,
	revisionType string,
	summary string,
	patchPublicId string,
	snapshotBefore interface{},
	snapshotAfter interface{},
) (*pkgmodels.CourseRevisionEvent, error) {
	event := pkgmodels.NewCourseRevisionEvent(tenantID, productPublicId, revisionType, summary)
	event.PatchPublicId = patchPublicId
	event.SnapshotBefore = snapshotBefore
	event.SnapshotAfter = snapshotAfter

	created, err := queries.CreateCourseRevisionEvent(event)
	if err != nil {
		log.Printf("[LMS] Failed to record revision event: %v", err)
		return nil, err
	}
	return created, nil
}

// ListRevisions lists revision events for a course.
func (s *CourseRevisionService) ListRevisions(
	tenantID bson.ObjectId,
	productPublicId string,
	skip, limit int,
) ([]*pkgmodels.CourseRevisionEvent, error) {
	return queries.ListCourseRevisionEvents(tenantID, productPublicId, skip, limit)
}
