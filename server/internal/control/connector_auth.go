package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

const (
	connectorRequestMaxBody = 16 << 20
	connectorRequestMaxSkew = 2 * time.Minute
)

type connectorKeyResolver func(context.Context, *http.Request, []byte) (string, []byte, error)

type connectorReplayCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func (c *connectorReplayCache) use(connectorID, nonce string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]time.Time)
	}
	for key, expiresAt := range c.entries {
		if !expiresAt.After(now) {
			delete(c.entries, key)
		}
	}
	key := connectorID + ":" + nonce
	if _, exists := c.entries[key]; exists {
		return false
	}
	c.entries[key] = now.Add(connectorRequestMaxSkew)
	return true
}

func ConnectorAuthMiddleware(resolve connectorKeyResolver, replay *connectorReplayCache, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, connectorRequestMaxBody+1))
		if err != nil || len(body) > connectorRequestMaxBody {
			jsonError(w, http.StatusBadRequest, "INVALID_CONNECTOR_REQUEST", "Connector 请求体无效或过大")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		connectorID, key, err := resolve(r.Context(), r, body)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "CONNECTOR_UNAUTHORIZED", "Connector 身份认证失败")
			return
		}
		now := time.Now()
		nonce, err := domain.VerifyConnectorRequest(r, body, key, now, connectorRequestMaxSkew)
		if err != nil || !replay.use(connectorID, nonce, now) {
			jsonError(w, http.StatusUnauthorized, "CONNECTOR_UNAUTHORIZED", "Connector 请求签名无效、过期或已重放")
			return
		}
		next(w, r)
	}
}

func (h *Handler) resolveConnectorRequestKey(ctx context.Context, r *http.Request, body []byte) (string, []byte, error) {
	if h.pool == nil {
		return "", nil, errors.New("database unavailable")
	}
	connectorID := r.PathValue("id")
	var encryptedKey []byte
	if r.URL.Path == "/api/v1/connectors/register" {
		var request struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &request); err != nil || strings.TrimSpace(request.Name) == "" {
			return "", nil, errors.New("invalid connector registration")
		}
		rows, err := h.pool.Query(ctx, `SELECT c.id, c.tenant_id, c.task_key_ciphertext, c.archived_at IS NOT NULL FROM connectors c
			JOIN tenants t ON t.slug=c.tenant_id AND t.status='ACTIVE'
			WHERE c.name=$1`, strings.TrimSpace(request.Name))
		if err != nil {
			return "", nil, err
		}
		defer rows.Close()
		var resolvedTenant string
		var key []byte
		candidateCount := 0
		for rows.Next() {
			var candidateID, candidateTenant string
			var candidateEncryptedKey []byte
			var archived bool
			if err := rows.Scan(&candidateID, &candidateTenant, &candidateEncryptedKey, &archived); err != nil {
				return "", nil, err
			}
			candidateCount++
			if len(candidateEncryptedKey) == 0 {
				continue
			}
			candidateKey, decryptErr := h.credentialCipher.Decrypt(candidateEncryptedKey)
			if decryptErr != nil || len(candidateKey) < 32 {
				continue
			}
			if _, verifyErr := domain.VerifyConnectorRequest(r, body, candidateKey, time.Now(), connectorRequestMaxSkew); verifyErr != nil {
				continue
			}
			if connectorID != "" {
				return "", nil, errors.New("connector registration identity is ambiguous")
			}
			if archived {
				return "", nil, errors.New("archived connector cannot self-reactivate")
			}
			connectorID, resolvedTenant, key = candidateID, candidateTenant, candidateKey
		}
		if err := rows.Err(); err != nil {
			return "", nil, err
		}
		if connectorID == "" && candidateCount == 0 {
			if legacyConnectorKeyEnabled() {
				return "bootstrap:" + strings.TrimSpace(request.Name), h.taskSigningKey, nil
			}
			return "", nil, errors.New("connector must be paired before registration")
		}
		if connectorID == "" {
			return "", nil, errors.New("connector registration signature does not match a paired connector")
		}
		// Registration is an authenticated connector request, so bind the
		// request to the tenant that owns the paired connector before the
		// handler performs its upsert. The connector must never fall back to
		// the database column default (the legacy "default" tenant).
		*r = *r.WithContext(domain.WithTenantID(r.Context(), resolvedTenant))
		return connectorID, key, nil
	} else {
		if connectorID == "" && connectorRequestCarriesID(r.URL.Path) {
			var request struct {
				ConnectorID string `json:"connectorId"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				return "", nil, err
			}
			connectorID = request.ConnectorID
		}
		if connectorID == "" {
			return "", nil, errors.New("connector id is required")
		}
		err := h.pool.QueryRow(ctx, `SELECT c.task_key_ciphertext FROM connectors c
			JOIN tenants t ON t.slug=c.tenant_id AND t.status='ACTIVE'
			WHERE c.id=$1 AND c.archived_at IS NULL`, connectorID).Scan(&encryptedKey)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", nil, errors.New("connector not found")
			}
			return "", nil, err
		}
		// Bind all connector-runtime handlers to the tenant stored with the
		// authenticated connector. The connector cannot choose this value.
		var resolvedTenant string
		if err := h.pool.QueryRow(ctx, `SELECT tenant_id FROM connectors WHERE id=$1 AND archived_at IS NULL`, connectorID).Scan(&resolvedTenant); err == nil {
			*r = *r.WithContext(domain.WithTenantID(r.Context(), resolvedTenant))
		}
	}
	if len(encryptedKey) == 0 {
		if legacyConnectorKeyEnabled() {
			return connectorID, h.taskSigningKey, nil
		}
		return "", nil, errors.New("connector has no individual task key")
	}
	key, err := h.credentialCipher.Decrypt(encryptedKey)
	if err != nil {
		return "", nil, fmt.Errorf("decrypt connector key: %w", err)
	}
	if len(key) < 32 {
		return "", nil, errors.New("connector key is too short")
	}
	return connectorID, key, nil
}

func connectorRequestCarriesID(path string) bool {
	switch path {
	case "/api/v1/resources/sync", "/api/v2/resources/sync", "/api/v1/connectors/credentials/resolve":
		return true
	default:
		return false
	}
}

func legacyConnectorKeyEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("WARDEN_ENABLE_LEGACY_CONNECTOR_KEY")), "true")
}
