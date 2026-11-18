package control

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/domain"
)

func configuredRegistrationAudience() (string, error) {
	explicit := strings.TrimSpace(os.Getenv("WARDEN_REGISTRATION_AUDIENCE"))
	publicControlURL := strings.TrimSpace(os.Getenv("WARDEN_PUBLIC_CONTROL_URL"))
	if explicit == "" && publicControlURL == "" {
		port := strings.TrimSpace(os.Getenv("WARDEN_PORT"))
		if port == "" {
			port = "8080"
		}
		return normalizeRegistrationAudience("http://localhost:" + port)
	}
	value := explicit
	if value == "" {
		value = publicControlURL
	}
	normalized, err := normalizeRegistrationAudience(value)
	if err != nil {
		return "", err
	}
	if explicit != "" && publicControlURL != "" {
		publicAudience, publicErr := normalizeRegistrationAudience(publicControlURL)
		if publicErr != nil || publicAudience != normalized {
			return "", errors.New("WARDEN_REGISTRATION_AUDIENCE must match WARDEN_PUBLIC_CONTROL_URL")
		}
	}
	return normalized, nil
}

func normalizeRegistrationAudience(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("registration audience must be an absolute HTTP(S) Warden API endpoint")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/api/v1"
	}
	if path != "/api/v1" {
		return "", errors.New("registration audience path must be /api/v1")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + path, nil
}

type authorizationStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *authorizationStatusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *authorizationStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (w *authorizationStatusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// authorizationAuditMiddleware records authenticated authorization failures.
// Invalid bearer tokens remain anonymous 401s; a recognized credential that
// lacks scope is attributable to its Principal and immutable credential ID.
func (h *Handler) authorizationAuditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &authorizationStatusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		if wrapped.status != http.StatusForbidden || h.audit == nil {
			return
		}
		identity := identityFromContext(r.Context())
		if identity == nil || identity.Principal == nil || identity.CredentialID == "" {
			return
		}
		metadata := map[string]any{
			"method": r.Method, "path": r.URL.Path, "scopes": identity.Scopes, "identity_kind": identity.Kind,
		}
		for key, value := range identity.AuditMetadata {
			metadata[key] = value
		}
		event := &domain.AuditEvent{Action: "authorization.denied", ActorID: identity.Principal.ID,
			ActorType: string(identity.Principal.Kind), CredentialID: identity.CredentialID, TargetID: r.URL.Path,
			Summary: "Authorization denied: " + r.Method + " " + r.URL.Path, Metadata: metadata}
		if err := h.audit.Create(r.Context(), event); err != nil {
			log.Printf("write authorization denied audit: %v", err)
		}
	})
}

func (h *Handler) writeRegistrationAuthenticationDenied(ctx context.Context, key *domain.PrincipalAPIKey, principal *domain.Principal, reason string) {
	if h.audit == nil || key == nil || principal == nil {
		return
	}
	event := &domain.AuditEvent{Action: "authorization.denied", ActorID: principal.ID, ActorType: string(principal.Kind),
		CredentialID: key.ID, TargetID: key.Audience, Summary: "Registration credential rejected",
		Metadata: map[string]any{"reason": reason, "credential_purpose": key.Purpose, "credential_audience": key.Audience,
			"expected_audience": h.registrationAudience}}
	if err := h.audit.Create(ctx, event); err != nil {
		log.Printf("write registration authentication denied audit: %v", err)
	}
}
