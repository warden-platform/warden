package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/ai"
	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fakeAIStore struct {
	conversation *domain.AIConversation
	messages     []domain.AIMessage
	run          *domain.AIRun
}

func (f *fakeAIStore) CreateConversation(_ context.Context, principalID, title string) (*domain.AIConversation, error) {
	f.conversation = &domain.AIConversation{ID: "conversation-1", PrincipalID: principalID, Title: title}
	return f.conversation, nil
}
func (f *fakeAIStore) GetConversation(_ context.Context, id, principalID string) (*domain.AIConversation, error) {
	if f.conversation == nil || f.conversation.ID != id || f.conversation.PrincipalID != principalID {
		return nil, nil
	}
	return f.conversation, nil
}
func (f *fakeAIStore) CreateRun(_ context.Context, conversationID string) (*domain.AIRun, error) {
	f.run = &domain.AIRun{ID: "run-1", ConversationID: conversationID, Status: "RUNNING"}
	return f.run, nil
}
func (f *fakeAIStore) FinishRun(_ context.Context, _, status, errorCode string) error {
	f.run.Status, f.run.ErrorCode = status, errorCode
	return nil
}
func (f *fakeAIStore) AddMessage(_ context.Context, conversationID, runID, role, content string) (*domain.AIMessage, error) {
	message := domain.AIMessage{ID: "message", ConversationID: conversationID, RunID: runID, Role: role, Content: content, CreatedAt: time.Now()}
	f.messages = append(f.messages, message)
	return &message, nil
}
func (f *fakeAIStore) ListMessages(_ context.Context, _, _ string, _ int) ([]domain.AIMessage, error) {
	return nil, nil
}
func (f *fakeAIStore) AddToolCall(context.Context, *domain.AIToolCall) error { return nil }

type fakeAIRunner struct{}

func (fakeAIRunner) Run(_ context.Context, _ []*schema.Message, _ string, emit func(ai.Event) error) error {
	if err := emit(ai.Event{Kind: "delta", Content: "只读回答"}); err != nil {
		return err
	}
	return nil
}

func TestAIToolsRejectMissingScopeBeforeRepositoryAccess(t *testing.T) {
	h := &Handler{}
	ctx := context.WithValue(context.Background(), identityContextKey{}, &AuthenticatedIdentity{
		Principal: &domain.Principal{ID: "principal"}, Scopes: []string{},
	})
	if _, err := h.queryAIResources(ctx, &aiResourceQuery{}); err == nil {
		t.Fatal("resource tool must reject a principal without resource scope")
	}
	if _, err := h.queryAIExecutions(ctx, &aiExecutionQuery{}); err == nil {
		t.Fatal("execution tool must reject a principal without execution scope")
	}
	if _, err := h.queryAIAuditEvents(ctx, &aiAuditQuery{}); err == nil {
		t.Fatal("audit tool must reject a principal without audit scope")
	}
}

func TestAIChatReturnsNotConfiguredWithoutCreatingARun(t *testing.T) {
	store := &fakeAIStore{}
	h := &Handler{aiConfig: &ai.Config{Enabled: true}, aiStore: store}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat", strings.NewReader(`{"message":"查询资源"}`))
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, &AuthenticatedIdentity{Principal: &domain.Principal{ID: "principal"}}))
	response := httptest.NewRecorder()
	h.AIChat(response, req)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "AI_NOT_CONFIGURED") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	if store.run != nil {
		t.Fatal("unconfigured model must not create a run")
	}
}

func TestAIChatStreamsAndPersistsAnswer(t *testing.T) {
	originalFactory := newAIStreamRunner
	newAIStreamRunner = func(context.Context, *ai.Config, []tool.BaseTool) (aiStreamRunner, error) {
		return fakeAIRunner{}, nil
	}
	t.Cleanup(func() { newAIStreamRunner = originalFactory })
	store := &fakeAIStore{}
	h := &Handler{aiConfig: &ai.Config{Enabled: true, Model: "model", APIKey: "key"}, aiStore: store}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat", strings.NewReader(`{"message":"查询资源"}`))
	req = req.WithContext(context.WithValue(req.Context(), identityContextKey{}, &AuthenticatedIdentity{Principal: &domain.Principal{ID: "principal"}}))
	response := httptest.NewRecorder()
	h.AIChat(response, req)
	body := response.Body.String()
	if !strings.Contains(body, "event: delta") || !strings.Contains(body, "只读回答") || !strings.Contains(body, "event: done") {
		t.Fatalf("unexpected SSE body: %s", body)
	}
	if store.run.Status != "COMPLETED" || len(store.messages) != 2 || store.messages[1].Content != "只读回答" {
		t.Fatalf("run or messages not persisted: %+v %+v", store.run, store.messages)
	}
}

func TestAIStreamingWritersForwardFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	authorizationWriter := &authorizationStatusWriter{ResponseWriter: recorder}
	authorizationWriter.Flush()
	if !recorder.Flushed {
		t.Fatal("authorization status writer must forward streaming flushes")
	}
	gzipWriter := &gzipResponseWriter{ResponseWriter: recorder}
	gzipWriter.Flush()
	if !recorder.Flushed {
		t.Fatal("gzip writer must forward streaming flushes")
	}
}
