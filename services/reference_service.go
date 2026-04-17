package services

import (
	"fmt"
	"log"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/queries"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
	"github.com/josephalai/sentanyl/pkg/utils"
)

// ReferenceService handles ingestion of txt, pdf, and pasted references.
type ReferenceService struct {
	chunkSize int
}

// NewReferenceService creates a ReferenceService with default chunk size.
func NewReferenceService() *ReferenceService {
	return &ReferenceService{chunkSize: 2000}
}

// IngestText creates a source reference from raw text content.
func (s *ReferenceService) IngestText(
	tenantID bson.ObjectId,
	productID bson.ObjectId,
	fileName string,
	fileType string,
	content string,
) (*pkgmodels.SourceReference, error) {
	ref := pkgmodels.NewSourceReference(tenantID, productID, "", fileName, fileType)
	ref.OriginalSize = int64(len(content))
	ref.ExtractedText = content

	// Chunk the text
	for i := 0; i < len(content); i += s.chunkSize {
		end := i + s.chunkSize
		if end > len(content) {
			end = len(content)
		}
		ref.Chunks = append(ref.Chunks, pkgmodels.TextChunk{
			Index: len(ref.Chunks),
			Text:  content[i:end],
			Start: i,
			End:   end,
		})
	}

	created, err := queries.CreateSourceReference(ref)
	if err != nil {
		return nil, fmt.Errorf("failed to create reference: %w", err)
	}
	return created, nil
}

// IngestPDF extracts text from a PDF and creates a source reference.
// In this implementation, the extracted text is expected to be pre-extracted
// and passed as content. Full PDF binary extraction would require a PDF library.
func (s *ReferenceService) IngestPDF(
	tenantID bson.ObjectId,
	productID bson.ObjectId,
	fileName string,
	content string,
) (*pkgmodels.SourceReference, error) {
	// For PDF: content should be pre-extracted text
	// In production, this would use a PDF extraction library
	return s.IngestText(tenantID, productID, fileName, "pdf", content)
}

// GetReferenceText retrieves concatenated reference text for a set of reference IDs.
func (s *ReferenceService) GetReferenceText(tenantID bson.ObjectId, referenceIds []string) (string, error) {
	var combined string
	for _, refId := range referenceIds {
		ref, err := queries.GetSourceReferenceByPublicId(tenantID, refId)
		if err != nil {
			log.Printf("[LMS] Reference %s not found: %v", refId, err)
			continue
		}
		combined += ref.ExtractedText + "\n\n"
	}
	return combined, nil
}

// GetSourceReferenceByPublicId is a helper that wraps the query.
func GetSourceReferenceByPublicId(tenantID bson.ObjectId, publicId string) (*pkgmodels.SourceReference, error) {
	return queries.GetSourceReferenceByPublicId(tenantID, publicId)
}

func init() {
	_ = utils.GeneratePublicId // ensure import is used
	log.Println("[LMS] Reference service initialized")
}
