package control

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLoggingMiddlewareCapturesStatusAndRequestID 验证日志中间件捕获状态码并
// 注入 X-Request-ID（无输入时生成、有输入时透传）。
func TestLoggingMiddlewareCapturesStatusAndRequestID(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	mw := LoggingMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
	resp := httptest.NewRecorder()
	mw.ServeHTTP(resp, req)

	if resp.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", resp.Code)
	}
	if resp.Header().Get(requestIDHeader) == "" {
		t.Error("未注入 X-Request-ID")
	}
}

func TestLoggingMiddlewarePreservesRequestID(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mw := LoggingMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
	req.Header.Set(requestIDHeader, "test-request-123")
	resp := httptest.NewRecorder()
	mw.ServeHTTP(resp, req)

	if got := resp.Header().Get(requestIDHeader); got != "test-request-123" {
		t.Fatalf("X-Request-ID = %q, want test-request-123", got)
	}
}

// TestLoggingMiddlewarePreservesFlusher 验证 statusRecorder 透传 http.Flusher，
// 流式端点（SSE 等）不被日志中间件打断。
func TestLoggingMiddlewarePreservesFlusher(t *testing.T) {
	flushed := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("handler 未收到 http.Flusher")
			return
		}
		f.Flush()
		flushed = true
	})
	mw := LoggingMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
	resp := httptest.NewRecorder()
	mw.ServeHTTP(resp, req)

	if !flushed {
		t.Error("Flush 未生效")
	}
}