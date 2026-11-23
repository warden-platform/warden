package control

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

type gzipResponseWriter struct {
	http.ResponseWriter
	gzipWriter  *gzip.Writer
	wroteHeader bool
	compressed  bool
}

func (w *gzipResponseWriter) Flush() {
	if !w.compressed {
		if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	contentType := w.Header().Get("Content-Type")
	if status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified && strings.Contains(contentType, "application/json") {
		w.compressed = true
		w.gzipWriter = gzip.NewWriter(w.ResponseWriter)
	}
	if !w.compressed {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.compressed {
		return w.gzipWriter.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// GzipMiddleware compresses JSON API responses. Resource catalogs can contain
// thousands of metadata rows; compression avoids transferring multi-megabyte
// uncompressed payloads while preserving the existing JSON contract.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		wrapped := &gzipResponseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		if wrapped.gzipWriter != nil {
			_ = wrapped.gzipWriter.Close()
		}
	})
}

func acceptsGzip(header string) bool {
	for _, value := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(value), ";")
		if strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
			return len(parts) == 1 || strings.TrimSpace(parts[1]) != "q=0"
		}
	}
	return false
}

type AuthenticatedIdentity struct {
	Principal *domain.Principal
	// TenantID is always derived from the authenticated Principal or Connector
	// credential. Callers must never accept a tenant from an agent request body.
	TenantID                  string
	IsPlatformAdmin           bool
	CredentialID              string
	SessionID                 string
	Scopes                    []string
	Kind                      string
	AuditMetadata             map[string]any
	DelegationTokenID         string
	DelegationProjectID       string
	DelegationExternalContext map[string]any
	OnBehalfOfPrincipalID     string
	AccountablePrincipalID    string
}

func (i *AuthenticatedIdentity) HasScope(scope string) bool {
	if i == nil {
		return false
	}
	for _, candidate := range i.Scopes {
		if candidate == "warden:admin" || candidate == scope {
			return true
		}
	}
	return false
}

func (i *AuthenticatedIdentity) IsTenantAdministrator() bool {
	return i != nil && (i.IsPlatformAdmin || i.HasScope("warden:tenant:admin"))
}

type identityContextKey struct{}

func identityFromContext(ctx context.Context) *AuthenticatedIdentity {
	identity, _ := ctx.Value(identityContextKey{}).(*AuthenticatedIdentity)
	return identity
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		// 请求关联 ID：优先透传客户端 X-Request-ID，否则生成；写入响应头与日志，
		// 便于多租户并发下按请求串联日志与故障定位。
		requestID := r.Header.Get(requestIDHeader)
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set(requestIDHeader, requestID)

		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			// handler 未显式写状态码时 net/http 默认 200
			status = http.StatusOK
		}
		log.Printf("method=%s path=%s status=%d duration_ms=%d request_id=%s",
			r.Method, r.URL.Path, status, time.Since(start).Milliseconds(), requestID)
	})
}

const requestIDHeader = "X-Request-ID"

// statusRecorder 捕获写入的状态码（默认 200），供日志记录；首个状态码一旦
// 写出即冻结，避免多次 WriteHeader 覆盖。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Flush 透传内层 Flusher，保留 SSE 等流式端点（ai_assistant 等）的可用性。
func (r *statusRecorder) Flush() {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedOrigin, err := configuredCORSOrigin()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "INVALID_CORS_CONFIGURATION", "服务端请求来源配置无效")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func configuredCORSOrigin() (string, error) {
	origin := strings.TrimSpace(os.Getenv("WARDEN_CORS_ORIGIN"))
	if origin == "" {
		origin = "http://localhost:4173"
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("WARDEN_CORS_ORIGIN must be a single absolute HTTP(S) origin")
	}
	return strings.TrimRight(origin, "/"), nil
}

type AuthenticateFunc func(context.Context, string, string) (*AuthenticatedIdentity, error)

const protectedRequestMaxBody = 2 << 20

func IdentityAuthMiddleware(authenticate AuthenticateFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, protectedRequestMaxBody)
		}
		const prefix = "Bearer "
		authHeader := r.Header.Get("Authorization")
		token := ""
		cookieAuth := false
		if strings.HasPrefix(authHeader, prefix) {
			token = strings.TrimSpace(authHeader[len(prefix):])
		} else if cookie, err := r.Cookie("warden_session"); err == nil {
			token = cookie.Value
			cookieAuth = true
		}
		if token == "" {
			jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "无效或缺失的 API Key")
			return
		}
		if cookieAuth && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			allowedOrigin, originErr := configuredCORSOrigin()
			if originErr != nil || r.Header.Get("Origin") != allowedOrigin {
				jsonError(w, http.StatusForbidden, "CSRF_REJECTED", "请求来源无效")
				return
			}
		}
		identity, err := authenticate(r.Context(), token, requestClientAddress(r))
		if err != nil {
			log.Printf("API Key authentication failed: %v", err)
		}
		if err != nil || identity == nil || identity.Principal == nil {
			jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "无效或缺失的 API Key")
			return
		}
		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		if identity.TenantID != "" {
			ctx = WithTenantContext(ctx, identity.TenantID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return RequireAnyScope([]string{scope}, next)
}

func RequireAnyScope(scopes []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity := identityFromContext(r.Context())
		for _, scope := range scopes {
			if identity.HasScope(scope) {
				next(w, r)
				return
			}
		}
		jsonResp(w, http.StatusForbidden, map[string]any{
			"error": "INSUFFICIENT_SCOPE", "message": "API Key 无权执行此操作", "requiredScopes": scopes,
		})
	}
}

// RequireTenantAdmin allows a platform administrator or an administrator of
// the authenticated tenant. The handler/repository layer still enforces the
// tenant boundary on every object access.
func RequireTenantAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity := identityFromContext(r.Context())
		if identity.IsTenantAdministrator() {
			next(w, r)
			return
		}
		jsonResp(w, http.StatusForbidden, map[string]any{
			"error": "INSUFFICIENT_SCOPE", "message": "需要租户管理员权限", "requiredScopes": []string{"warden:tenant:admin"},
		})
	}
}

// APIKeyAuthMiddleware remains as a small compatibility helper for callers
// that only need to protect a single endpoint with a static secret.
func APIKeyAuthMiddleware(expectedKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, prefix) || subtle.ConstantTimeCompare([]byte(authHeader[len(prefix):]), []byte(expectedKey)) != 1 {
			jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "无效或缺失的 API Key")
			return
		}
		next.ServeHTTP(w, r)
	})
}
