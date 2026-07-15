package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.in/mgo.v2/bson"

	"github.com/josephalai/sentanyl/lms-service/llm"
)

type cancelAwareProvider struct{ started chan struct{} }

func (p *cancelAwareProvider) Name() string { return "cancel-aware" }
func (p *cancelAwareProvider) GenerateOutline(ctx context.Context, _ llm.OutlineRequest) (*llm.OutlineResponse, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *cancelAwareProvider) GenerateLessonContent(ctx context.Context, _ llm.LessonContentRequest) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (p *cancelAwareProvider) GenerateQuiz(ctx context.Context, _ llm.QuizRequest) (*llm.QuizResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestGenerateOutlineContextDoesNotFallbackAfterCancellation(t *testing.T) {
	provider := &cancelAwareProvider{started: make(chan struct{})}
	service := NewGenerationService()
	service.SetLLMProvider(provider)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := service.GenerateOutlineContext(ctx, bson.NewObjectId(), "course", "prompt", "", "", "", 1, false, false, "", "", nil)
		done <- err
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("generation did not stop after cancellation")
	}
}
