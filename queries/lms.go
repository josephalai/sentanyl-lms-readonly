package queries

import (
	"fmt"
	"log"
	"time"

	"gopkg.in/mgo.v2/bson"

	
	"github.com/josephalai/sentanyl/pkg/db"
	pkgmodels "github.com/josephalai/sentanyl/pkg/models"
)

// ---------- CourseEnrollment CRUD ----------

func CreateCourseEnrollment(enrollment *pkgmodels.CourseEnrollment) (*pkgmodels.CourseEnrollment, error) {
	enrollment.SetCreated()
	err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Insert(enrollment)
	if err != nil {
		log.Println("CreateCourseEnrollment error:", err)
		return nil, err
	}
	return enrollment, nil
}

func GetCourseEnrollmentByPublicId(tenantID bson.ObjectId, publicId string) (*pkgmodels.CourseEnrollment, error) {
	result := pkgmodels.CourseEnrollment{}
	query := bson.M{
		"tenant_id":             tenantID,
		"public_id":             publicId,
		"timestamps.deleted_at": nil,
	}
	err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Find(query).One(&result)
	if err != nil {
		log.Println("GetCourseEnrollmentByPublicId error:", err)
		return nil, err
	}
	return &result, nil
}

func GetCourseEnrollmentByContactAndProduct(tenantID, contactID, productID bson.ObjectId) (*pkgmodels.CourseEnrollment, error) {
	result := pkgmodels.CourseEnrollment{}
	query := bson.M{
		"tenant_id":             tenantID,
		"contact_id":            contactID,
		"product_id":            productID,
		"timestamps.deleted_at": nil,
	}
	err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Find(query).One(&result)
	if err != nil {
		log.Println("GetCourseEnrollmentByContactAndProduct error:", err)
		return nil, err
	}
	return &result, nil
}

func ListCourseEnrollments(tenantID bson.ObjectId, productID *bson.ObjectId, status string, skip, limit int) ([]*pkgmodels.CourseEnrollment, error) {
	result := []*pkgmodels.CourseEnrollment{}
	query := bson.M{
		"tenant_id":             tenantID,
		"timestamps.deleted_at": nil,
	}
	if productID != nil {
		query["product_id"] = *productID
	}
	if status != "" {
		query["status"] = status
	}
	q := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Find(query).Sort("-enrolled_at")
	if skip > 0 {
		q = q.Skip(skip)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.All(&result)
	if err != nil {
		log.Println("ListCourseEnrollments error:", err)
		return nil, err
	}
	return result, nil
}

func ListCourseEnrollmentsByContact(tenantID, contactID bson.ObjectId, status string, skip, limit int) ([]*pkgmodels.CourseEnrollment, error) {
	result := []*pkgmodels.CourseEnrollment{}
	query := bson.M{
		"tenant_id":             tenantID,
		"contact_id":            contactID,
		"timestamps.deleted_at": nil,
	}
	if status != "" {
		query["status"] = status
	}
	q := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Find(query).Sort("-enrolled_at")
	if skip > 0 {
		q = q.Skip(skip)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.All(&result)
	if err != nil {
		log.Println("ListCourseEnrollmentsByContact error:", err)
		return nil, err
	}
	return result, nil
}

func UpdateCourseEnrollment(tenantID bson.ObjectId, publicId string, update bson.M) (*pkgmodels.CourseEnrollment, error) {
	query := bson.M{
		"tenant_id":             tenantID,
		"public_id":             publicId,
		"timestamps.deleted_at": nil,
	}
	update["timestamps.updated_at"] = time.Now()
	err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Update(query, bson.M{"$set": update})
	if err != nil {
		log.Println("UpdateCourseEnrollment error:", err)
		return nil, err
	}
	return GetCourseEnrollmentByPublicId(tenantID, publicId)
}

func RevokeCourseEnrollment(tenantID bson.ObjectId, publicId string) (*pkgmodels.CourseEnrollment, error) {
	now := time.Now()
	return UpdateCourseEnrollment(tenantID, publicId, bson.M{
		"status":     "revoked",
		"revoked_at": now,
	})
}

func UpdateLessonProgress(tenantID bson.ObjectId, enrollmentPublicId string, progress *pkgmodels.LessonProgress) (*pkgmodels.CourseEnrollment, error) {
	enrollment, err := GetCourseEnrollmentByPublicId(tenantID, enrollmentPublicId)
	if err != nil {
		return nil, err
	}

	found := false
	for i, p := range enrollment.Progress {
		if p.LessonSlug == progress.LessonSlug && p.ModuleSlug == progress.ModuleSlug {
			updateFields := bson.M{
				fmt.Sprintf("progress.%d.watch_percent", i):     progress.WatchPercent,
				fmt.Sprintf("progress.%d.last_position_sec", i): progress.LastPositionSec,
				"timestamps.updated_at":                         time.Now(),
			}
			if progress.Completed {
				updateFields[fmt.Sprintf("progress.%d.completed", i)] = true
				if progress.CompletedAt != nil {
					updateFields[fmt.Sprintf("progress.%d.completed_at", i)] = progress.CompletedAt
				}
			}
			if progress.QuizPassed != nil {
				updateFields[fmt.Sprintf("progress.%d.quiz_passed", i)] = *progress.QuizPassed
			}
			err = db.GetCollection(pkgmodels.CourseEnrollmentCollection).Update(
				bson.M{"_id": enrollment.Id},
				bson.M{"$set": updateFields},
			)
			if err != nil {
				log.Println("UpdateLessonProgress update error:", err)
			}
			found = true
			break
		}
	}

	if !found {
		err = db.GetCollection(pkgmodels.CourseEnrollmentCollection).Update(
			bson.M{"_id": enrollment.Id},
			bson.M{
				"$push": bson.M{"progress": progress},
				"$set":  bson.M{"timestamps.updated_at": time.Now()},
			},
		)
		if err != nil {
			log.Println("UpdateLessonProgress push error:", err)
		}
	}

	if err != nil {
		return nil, err
	}
	return GetCourseEnrollmentByPublicId(tenantID, enrollmentPublicId)
}

func RecalculateOverallPercent(tenantID bson.ObjectId, enrollmentPublicId string, product *pkgmodels.Product) (int, error) {
	enrollment, err := GetCourseEnrollmentByPublicId(tenantID, enrollmentPublicId)
	if err != nil {
		return 0, err
	}

	totalRequired := 0
	completed := 0
	for _, mod := range product.CourseModules {
		hasQuiz := mod.QuizSlug != ""
		for _, lesson := range mod.Lessons {
			if lesson.IsDraft {
				continue
			}
			totalRequired++
			for _, p := range enrollment.Progress {
				if p.LessonSlug == lesson.Slug && p.ModuleSlug == mod.Slug {
					if p.Completed {
						if hasQuiz {
							if p.QuizPassed != nil && *p.QuizPassed {
								completed++
							}
						} else {
							completed++
						}
					}
					break
				}
			}
		}
	}

	percent := 0
	if totalRequired > 0 {
		percent = (completed * 100) / totalRequired
	}

	now := time.Now()
	err = db.GetCollection(pkgmodels.CourseEnrollmentCollection).Update(
		bson.M{"_id": enrollment.Id},
		bson.M{"$set": bson.M{
			"overall_percent":       percent,
			"timestamps.updated_at": now,
		}},
	)
	if err != nil {
		log.Println("RecalculateOverallPercent error:", err)
		return 0, err
	}
	return percent, nil
}

func CountCourseEnrollments(tenantID, productID bson.ObjectId, status string) (int, error) {
	query := bson.M{
		"tenant_id":             tenantID,
		"product_id":            productID,
		"timestamps.deleted_at": nil,
	}
	if status != "" {
		query["status"] = status
	}
	n, err := db.GetCollection(pkgmodels.CourseEnrollmentCollection).Find(query).Count()
	if err != nil {
		log.Println("CountCourseEnrollments error:", err)
	}
	return n, err
}

// ---------- LessonCompletion ----------

func CreateLessonCompletion(completion *pkgmodels.LessonCompletion) (*pkgmodels.LessonCompletion, error) {
	err := db.GetCollection(pkgmodels.LessonCompletionCollection).Insert(completion)
	if err != nil {
		log.Println("CreateLessonCompletion error:", err)
		return nil, err
	}
	return completion, nil
}

func ListLessonCompletions(tenantID, enrollmentID bson.ObjectId) ([]*pkgmodels.LessonCompletion, error) {
	result := []*pkgmodels.LessonCompletion{}
	err := db.GetCollection(pkgmodels.LessonCompletionCollection).Find(bson.M{
		"tenant_id":     tenantID,
		"enrollment_id": enrollmentID,
	}).Sort("-completed_at").All(&result)
	if err != nil {
		log.Println("ListLessonCompletions error:", err)
		return nil, err
	}
	return result, nil
}

func CountLessonCompletionsByEnrollment(tenantID, enrollmentID bson.ObjectId) (int, error) {
	n, err := db.GetCollection(pkgmodels.LessonCompletionCollection).Find(bson.M{
		"tenant_id":     tenantID,
		"enrollment_id": enrollmentID,
	}).Count()
	if err != nil {
		log.Println("CountLessonCompletionsByEnrollment error:", err)
	}
	return n, err
}

// ---------- Certificate CRUD ----------

func CreateCertificate(cert *pkgmodels.Certificate) (*pkgmodels.Certificate, error) {
	cert.SetCreated()
	err := db.GetCollection(pkgmodels.CertificateCollection).Insert(cert)
	if err != nil {
		log.Println("CreateCertificate error:", err)
		return nil, err
	}
	return cert, nil
}

func GetCertificateByPublicId(tenantID bson.ObjectId, publicId string) (*pkgmodels.Certificate, error) {
	result := pkgmodels.Certificate{}
	query := bson.M{
		"tenant_id":             tenantID,
		"public_id":             publicId,
		"timestamps.deleted_at": nil,
	}
	err := db.GetCollection(pkgmodels.CertificateCollection).Find(query).One(&result)
	if err != nil {
		log.Println("GetCertificateByPublicId error:", err)
		return nil, err
	}
	return &result, nil
}

func GetCertificateByEnrollment(tenantID, enrollmentID bson.ObjectId) (*pkgmodels.Certificate, error) {
	result := pkgmodels.Certificate{}
	query := bson.M{
		"tenant_id":             tenantID,
		"enrollment_id":         enrollmentID,
		"timestamps.deleted_at": nil,
	}
	err := db.GetCollection(pkgmodels.CertificateCollection).Find(query).One(&result)
	if err != nil {
		log.Println("GetCertificateByEnrollment error:", err)
		return nil, err
	}
	return &result, nil
}

func ListCertificates(tenantID bson.ObjectId, contactID *bson.ObjectId, skip, limit int) ([]*pkgmodels.Certificate, error) {
	result := []*pkgmodels.Certificate{}
	query := bson.M{
		"tenant_id":             tenantID,
		"timestamps.deleted_at": nil,
	}
	if contactID != nil {
		query["contact_id"] = *contactID
	}
	q := db.GetCollection(pkgmodels.CertificateCollection).Find(query).Sort("-completed_at")
	if skip > 0 {
		q = q.Skip(skip)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.All(&result)
	if err != nil {
		log.Println("ListCertificates error:", err)
		return nil, err
	}
	return result, nil
}

func UpdateCertificate(tenantID bson.ObjectId, publicId string, update bson.M) (*pkgmodels.Certificate, error) {
	query := bson.M{
		"tenant_id":             tenantID,
		"public_id":             publicId,
		"timestamps.deleted_at": nil,
	}
	update["timestamps.updated_at"] = time.Now()
	err := db.GetCollection(pkgmodels.CertificateCollection).Update(query, bson.M{"$set": update})
	if err != nil {
		log.Println("UpdateCertificate error:", err)
		return nil, err
	}
	return GetCertificateByPublicId(tenantID, publicId)
}

func ListPendingCertificates(tenantID bson.ObjectId) ([]*pkgmodels.Certificate, error) {
	result := []*pkgmodels.Certificate{}
	err := db.GetCollection(pkgmodels.CertificateCollection).Find(bson.M{
		"tenant_id":             tenantID,
		"gen_status":            "pending",
		"timestamps.deleted_at": nil,
	}).Limit(5).All(&result)
	if err != nil {
		log.Println("ListPendingCertificates error:", err)
		return nil, err
	}
	return result, nil
}

// ---------- LMS Quiz CRUD ----------

func CreateLMSQuiz(quiz *pkgmodels.LMSQuiz) (*pkgmodels.LMSQuiz, error) {
	quiz.SetCreated()
	err := db.GetCollection(pkgmodels.LMSQuizCollection).Insert(quiz)
	if err != nil {
		log.Println("CreateLMSQuiz error:", err)
		return nil, err
	}
	return quiz, nil
}

func GetLMSQuizByPublicId(tenantID bson.ObjectId, publicId string) (*pkgmodels.LMSQuiz, error) {
	result := pkgmodels.LMSQuiz{}
	query := bson.M{
		"tenant_id":             tenantID,
		"public_id":             publicId,
		"timestamps.deleted_at": nil,
	}
	err := db.GetCollection(pkgmodels.LMSQuizCollection).Find(query).One(&result)
	if err != nil {
		log.Println("GetLMSQuizByPublicId error:", err)
		return nil, err
	}
	return &result, nil
}

func GetLMSQuizByProductAndModule(tenantID, productID bson.ObjectId, moduleSlug string) (*pkgmodels.LMSQuiz, error) {
	result := pkgmodels.LMSQuiz{}
	query := bson.M{
		"tenant_id":             tenantID,
		"product_id":            productID,
		"module_slug":           moduleSlug,
		"timestamps.deleted_at": nil,
	}
	err := db.GetCollection(pkgmodels.LMSQuizCollection).Find(query).One(&result)
	if err != nil {
		log.Println("GetLMSQuizByProductAndModule error:", err)
		return nil, err
	}
	return &result, nil
}

func ListLMSQuizzesByProduct(tenantID, productID bson.ObjectId) ([]*pkgmodels.LMSQuiz, error) {
	result := []*pkgmodels.LMSQuiz{}
	err := db.GetCollection(pkgmodels.LMSQuizCollection).Find(bson.M{
		"tenant_id":             tenantID,
		"product_id":            productID,
		"timestamps.deleted_at": nil,
	}).All(&result)
	if err != nil {
		log.Println("ListLMSQuizzesByProduct error:", err)
		return nil, err
	}
	return result, nil
}

func UpdateLMSQuiz(tenantID bson.ObjectId, publicId string, update bson.M) (*pkgmodels.LMSQuiz, error) {
	query := bson.M{
		"tenant_id":             tenantID,
		"public_id":             publicId,
		"timestamps.deleted_at": nil,
	}
	update["timestamps.updated_at"] = time.Now()
	err := db.GetCollection(pkgmodels.LMSQuizCollection).Update(query, bson.M{"$set": update})
	if err != nil {
		log.Println("UpdateLMSQuiz error:", err)
		return nil, err
	}
	return GetLMSQuizByPublicId(tenantID, publicId)
}

func DeleteLMSQuiz(tenantID bson.ObjectId, publicId string) (*pkgmodels.LMSQuiz, error) {
	quiz, err := GetLMSQuizByPublicId(tenantID, publicId)
	if err != nil {
		return nil, err
	}
	quiz.SetDeleted()
	err = db.GetCollection(pkgmodels.LMSQuizCollection).Update(
		bson.M{"_id": quiz.Id},
		bson.M{"$set": bson.M{"timestamps.deleted_at": quiz.DeletedAt}},
	)
	if err != nil {
		log.Println("DeleteLMSQuiz error:", err)
		return nil, err
	}
	return quiz, nil
}

// ---------- QuizAttempt ----------

func CreateQuizAttempt(attempt *pkgmodels.QuizAttempt) (*pkgmodels.QuizAttempt, error) {
	err := db.GetCollection(pkgmodels.QuizAttemptCollection).Insert(attempt)
	if err != nil {
		log.Println("CreateQuizAttempt error:", err)
		return nil, err
	}
	return attempt, nil
}

func ListQuizAttempts(tenantID, quizID, contactID bson.ObjectId) ([]*pkgmodels.QuizAttempt, error) {
	result := []*pkgmodels.QuizAttempt{}
	err := db.GetCollection(pkgmodels.QuizAttemptCollection).Find(bson.M{
		"tenant_id":  tenantID,
		"quiz_id":    quizID,
		"contact_id": contactID,
	}).Sort("-submitted_at").All(&result)
	if err != nil {
		log.Println("ListQuizAttempts error:", err)
		return nil, err
	}
	return result, nil
}

func CountQuizAttempts(tenantID, quizID, contactID bson.ObjectId) (int, error) {
	n, err := db.GetCollection(pkgmodels.QuizAttemptCollection).Find(bson.M{
		"tenant_id":  tenantID,
		"quiz_id":    quizID,
		"contact_id": contactID,
	}).Count()
	if err != nil {
		log.Println("CountQuizAttempts error:", err)
	}
	return n, err
}

func GetBestQuizAttempt(tenantID, quizID, contactID bson.ObjectId) (*pkgmodels.QuizAttempt, error) {
	result := pkgmodels.QuizAttempt{}
	err := db.GetCollection(pkgmodels.QuizAttemptCollection).Find(bson.M{
		"tenant_id":  tenantID,
		"quiz_id":    quizID,
		"contact_id": contactID,
	}).Sort("-score").One(&result)
	if err != nil {
		log.Println("GetBestQuizAttempt error:", err)
		return nil, err
	}
	return &result, nil
}

// ---------- Course Product Helpers ----------

func ListCourseProducts(tenantID bson.ObjectId, status string, skip, limit int) ([]*pkgmodels.Product, error) {
	result := []*pkgmodels.Product{}
	query := bson.M{
		"tenant_id":             tenantID,
		"product_type":          "course",
		"timestamps.deleted_at": nil,
	}
	if status != "" {
		query["status"] = status
	}
	q := db.GetCollection(pkgmodels.ProductCollection).Find(query).Sort("-timestamps.created_at")
	if skip > 0 {
		q = q.Skip(skip)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.All(&result)
	if err != nil {
		log.Println("ListCourseProducts error:", err)
		return nil, err
	}
	return result, nil
}

func CountCourseProducts(tenantID bson.ObjectId, status string) (int, error) {
	query := bson.M{
		"tenant_id":             tenantID,
		"product_type":          "course",
		"timestamps.deleted_at": nil,
	}
	if status != "" {
		query["status"] = status
	}
	n, err := db.GetCollection(pkgmodels.ProductCollection).Find(query).Count()
	if err != nil {
		log.Println("CountCourseProducts error:", err)
	}
	return n, err
}

func GetCourseProductByPublicId(tenantID bson.ObjectId, publicId string) (*pkgmodels.Product, error) {
	result := pkgmodels.Product{}
	query := bson.M{
		"tenant_id":             tenantID,
		"public_id":             publicId,
		"product_type":          "course",
		"timestamps.deleted_at": nil,
	}
	err := db.GetCollection(pkgmodels.ProductCollection).Find(query).One(&result)
	if err != nil {
		log.Println("GetCourseProductByPublicId error:", err)
		return nil, err
	}
	return &result, nil
}

func IncrementEnrollmentCount(tenantID, productID bson.ObjectId) error {
	err := db.GetCollection(pkgmodels.ProductCollection).Update(
		bson.M{"_id": productID, "tenant_id": tenantID},
		bson.M{"$inc": bson.M{"enrollment_count": 1}},
	)
	if err != nil {
		log.Println("IncrementEnrollmentCount error:", err)
	}
	return err
}

func IncrementCompletionCount(tenantID, productID bson.ObjectId) error {
	err := db.GetCollection(pkgmodels.ProductCollection).Update(
		bson.M{"_id": productID, "tenant_id": tenantID},
		bson.M{"$inc": bson.M{"completion_count": 1}},
	)
	if err != nil {
		log.Println("IncrementCompletionCount error:", err)
	}
	return err
}

func InsertProduct(product pkgmodels.Product) error {
	err := db.GetCollection(pkgmodels.ProductCollection).Insert(product)
	if err != nil {
		log.Println("InsertProduct error:", err)
	}
	return err
}

func ListAllPendingCertificates() ([]*pkgmodels.Certificate, error) {
	result := []*pkgmodels.Certificate{}
	err := db.GetCollection(pkgmodels.CertificateCollection).Find(bson.M{
		"gen_status":            "pending",
		"timestamps.deleted_at": nil,
	}).Limit(5).All(&result)
	if err != nil {
		log.Println("ListAllPendingCertificates error:", err)
		return nil, err
	}
	return result, nil
}

func UpdateProductField(productID bson.ObjectId, field string, value interface{}) error {
	err := db.GetCollection(pkgmodels.ProductCollection).UpdateId(productID, bson.M{
		"$set": bson.M{
			field:                   value,
			"timestamps.updated_at": time.Now(),
		},
	})
	if err != nil {
		log.Println("UpdateProductField error:", err)
	}
	return err
}

func UpdateLessonContentField(productID bson.ObjectId, moduleIdx, lessonIdx int, fields bson.M) error {
	setFields := bson.M{"timestamps.updated_at": time.Now()}
	for k, v := range fields {
		path := fmt.Sprintf("course_modules.%d.lessons.%d.%s", moduleIdx, lessonIdx, k)
		setFields[path] = v
	}
	err := db.GetCollection(pkgmodels.ProductCollection).UpdateId(productID, bson.M{
		"$set": setFields,
	})
	if err != nil {
		log.Println("UpdateLessonContentField error:", err)
	}
	return err
}
