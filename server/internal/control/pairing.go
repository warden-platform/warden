package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func randomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func randomPairingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	value := make([]byte, 8)
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range value {
		value[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(value[:4]) + "-" + string(value[4:]), nil
}

func hashPairingCode(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToUpper(value))))
	return hex.EncodeToString(sum[:])
}

func hashBootstrapToken(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

// hashLegacyBootstrapToken matches the pre-v0.3.53 behavior. Older Operators
// still hold the original mixed-case token while the database contains this
// upper-cased hash. Authentication accepts it once and immediately migrates
// the row to the case-sensitive hash.
func hashLegacyBootstrapToken(value string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(value))))
	return hex.EncodeToString(sum[:])
}

func bearerToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

const maxPendingPairings = 1000

func (h *Handler) reservePairingCapacity(r *http.Request) error {
	if _, err := h.pool.Exec(r.Context(), `UPDATE cluster_pairings SET status='EXPIRED',updated_at=now()
		WHERE status='PENDING' AND expires_at<=now()`); err != nil {
		return err
	}
	var count int
	if err := h.pool.QueryRow(r.Context(), `SELECT count(*) FROM cluster_pairings
		WHERE status='PENDING' AND expires_at>now()`).Scan(&count); err != nil {
		return err
	}
	if count >= maxPendingPairings {
		return fmt.Errorf("too many pending pairings")
	}
	return nil
}

func (h *Handler) CreateOperatorPairing(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	if !allowLoginAttempt(requestClientAddress(r), "operator-pairing", time.Now()) {
		w.Header().Set("Retry-After", "300")
		jsonError(w, http.StatusTooManyRequests, "PAIRING_RATE_LIMITED", "配对请求过于频繁，请稍后重试")
		return
	}
	if err := h.reservePairingCapacity(r); err != nil {
		jsonError(w, http.StatusServiceUnavailable, "PAIRING_CAPACITY_EXCEEDED", "当前待处理配对过多，请稍后重试")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		OperatorID           string   `json:"operatorId"`
		ClusterUID           string   `json:"clusterUid"`
		ClusterVersion       string   `json:"clusterVersion"`
		SuggestedAudience    string   `json:"suggestedAudience"`
		AvailableNamespaces  []string `json:"availableNamespaces"`
		SuggestedImagePrefix string   `json:"suggestedImagePrefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OperatorID == "" || req.ClusterUID == "" ||
		len(req.OperatorID) > 255 || len(req.ClusterUID) > 255 || len(req.AvailableNamespaces) > 500 {
		jsonError(w, http.StatusBadRequest, "INVALID_PAIRING_REQUEST", "operatorId 和 clusterUid 为必填")
		return
	}
	code, err := randomPairingCode()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PAIRING_GENERATION_FAILED", "生成配对码失败")
		return
	}
	bootstrapToken, err := randomToken(32)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PAIRING_GENERATION_FAILED", "生成配对凭证失败")
		return
	}
	taskKey, err := randomToken(32)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PAIRING_GENERATION_FAILED", "生成 Connector 凭证失败")
		return
	}
	encryptedKey, err := h.credentialCipher.Encrypt([]byte(taskKey))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "PAIRING_GENERATION_FAILED", "加密 Connector 凭证失败")
		return
	}
	expiresAt := time.Now().Add(10 * time.Minute)
	var pairingID string
	err = h.pool.QueryRow(r.Context(), `INSERT INTO cluster_pairings
		(code_hash, bootstrap_token_hash, operator_id, cluster_uid, cluster_version, suggested_audience,
		 available_namespaces, suggested_image_prefix, task_key_ciphertext, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		hashPairingCode(code), hashBootstrapToken(bootstrapToken), req.OperatorID, req.ClusterUID,
		req.ClusterVersion, req.SuggestedAudience, req.AvailableNamespaces, req.SuggestedImagePrefix,
		encryptedKey, expiresAt).Scan(&pairingID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存配对请求失败")
		return
	}
	jsonResp(w, http.StatusCreated, map[string]any{
		"pairingId": pairingID, "code": code, "bootstrapToken": bootstrapToken, "expiresAt": expiresAt,
	})
}

func (h *Handler) GetClusterPairingOptions(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		jsonError(w, http.StatusBadRequest, "INVALID_PAIRING_CODE", "请输入配对码")
		return
	}
	var namespaces []string
	var expiresAt time.Time
	err := h.pool.QueryRow(r.Context(), `SELECT available_namespaces, expires_at FROM cluster_pairings
		WHERE code_hash=$1 AND status='PENDING' AND expires_at>now()`, hashPairingCode(req.Code)).Scan(&namespaces, &expiresAt)
	if err != nil {
		jsonError(w, http.StatusNotFound, "PAIRING_NOT_FOUND", "配对码无效或已过期")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"availableNamespaces": namespaces, "expiresAt": expiresAt})
}

func (h *Handler) GetOperatorPairing(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var status string
	var connectorID *string
	var encryptedKey []byte
	var config map[string]any
	var expiresAt time.Time
	pairingID := r.PathValue("id")
	token := bearerToken(r)
	currentHash := hashBootstrapToken(token)
	legacyHash := hashLegacyBootstrapToken(token)
	err := h.pool.QueryRow(r.Context(), `SELECT status, connector_id, task_key_ciphertext, config, expires_at
		FROM cluster_pairings WHERE id=$1 AND bootstrap_token_hash IN ($2,$3)`,
		pairingID, currentHash, legacyHash).Scan(&status, &connectorID, &encryptedKey, &config, &expiresAt)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "INVALID_PAIRING_TOKEN", "配对凭证无效")
		return
	}
	if currentHash != legacyHash {
		_, _ = h.pool.Exec(r.Context(), `UPDATE cluster_pairings SET bootstrap_token_hash=$2,updated_at=now()
			WHERE id=$1 AND bootstrap_token_hash=$3`, pairingID, currentHash, legacyHash)
	}
	if status == "PENDING" && time.Now().After(expiresAt) {
		status = "EXPIRED"
		_, _ = h.pool.Exec(r.Context(), `UPDATE cluster_pairings SET status='EXPIRED', updated_at=now() WHERE id=$1`, r.PathValue("id"))
	}
	response := map[string]any{"status": status, "expiresAt": expiresAt}
	if (status == "CONFIRMED" || status == "READY" || status == "DELETE_REQUESTED") && connectorID != nil {
		taskKey, err := h.credentialCipher.Decrypt(encryptedKey)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "CREDENTIAL_DECRYPT_FAILED", "读取 Connector 凭证失败")
			return
		}
		response["connectorId"] = *connectorID
		response["taskSigningKey"] = base64.RawStdEncoding.EncodeToString(taskKey)
		// The Operator uses this live allowlist to grant the connector access only
		// to credentials referenced by its own enabled integrations. Keeping the
		// list out of the persisted pairing config means integration changes are
		// picked up by the existing reconcile poll without a new pairing.
		rows, queryErr := h.pool.Query(r.Context(), `SELECT DISTINCT credential_ref FROM integrations
			WHERE connector_id=$1 AND status<>'DISABLED' ORDER BY credential_ref`, *connectorID)
		if queryErr != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取 Connector 凭据范围失败")
			return
		}
		credentialRefs := make([]string, 0)
		for rows.Next() {
			var ref string
			if scanErr := rows.Scan(&ref); scanErr != nil {
				rows.Close()
				jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取 Connector 凭据范围失败")
				return
			}
			credentialRefs = append(credentialRefs, ref)
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "读取 Connector 凭据范围失败")
			return
		}
		sort.Strings(credentialRefs)
		config["credentialRefs"] = credentialRefs
		response["config"] = config
	}
	jsonResp(w, http.StatusOK, response)
}

func (h *Handler) DeleteConnector(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开始移除事务失败")
		return
	}
	defer tx.Rollback(r.Context())
	connectorID := r.PathValue("id")
	var connectorType string
	err = tx.QueryRow(r.Context(), `UPDATE connectors SET status='DISABLED', updated_at=now() WHERE id=$1 RETURNING connector_type`, connectorID).Scan(&connectorType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			jsonError(w, http.StatusNotFound, "CONNECTOR_NOT_FOUND", "Connector 不存在")
			return
		}
		jsonError(w, http.StatusNotFound, "CONNECTOR_NOT_FOUND", "Connector 不存在")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE resources SET status='DISABLED',
		metadata=metadata || jsonb_build_object('connectorDeleted', true), updated_at=now()
		WHERE connector_id=$1`, connectorID)
	if err == nil {
		// Integrations must follow the connector lifecycle: leaving them ACTIVE
		// after the connector is removed produces phantom "running" entries.
		_, err = tx.Exec(r.Context(), `UPDATE integrations SET status='DISABLED', updated_at=now() WHERE connector_id=$1`, connectorID)
	}
	if err == nil {
		if connectorType != "network" {
			var pairingResult pgconn.CommandTag
			pairingResult, err = tx.Exec(r.Context(), `UPDATE cluster_pairings SET status='DELETE_REQUESTED', updated_at=now()
				WHERE id=(SELECT id FROM cluster_pairings WHERE connector_id=$1 ORDER BY created_at DESC LIMIT 1)`, connectorID)
			if err == nil && pairingResult.RowsAffected() == 0 {
				jsonError(w, http.StatusConflict, "CONNECTOR_NOT_OPERATOR_MANAGED", "该 Connector 不是由 Operator 管理，无法自动清理目标集群")
				return
			}
		}
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "移除 Connector 失败")
		return
	}
	jsonResp(w, http.StatusAccepted, map[string]string{"id": connectorID, "status": "DISABLED"})
}

func (h *Handler) MarkOperatorPairingDeleted(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	token := bearerToken(r)
	currentHash := hashBootstrapToken(token)
	legacyHash := hashLegacyBootstrapToken(token)
	var connectorID string
	err := h.pool.QueryRow(r.Context(), `UPDATE cluster_pairings SET status='DELETED',bootstrap_token_hash=$2,updated_at=now()
		WHERE id=$1 AND bootstrap_token_hash IN ($2,$3) AND status IN ('DELETE_REQUESTED','DELETED')
		RETURNING connector_id`, r.PathValue("id"), currentHash, legacyHash).Scan(&connectorID)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "INVALID_PAIRING_TOKEN", "配对凭证无效")
		return
	}
	_, _ = h.pool.Exec(r.Context(), `UPDATE connectors SET status='DISABLED', updated_at=now() WHERE id=$1`, connectorID)
	jsonResp(w, http.StatusOK, map[string]string{"status": "DELETED"})
}

func (h *Handler) ArchiveConnector(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	connectorID := r.PathValue("id")
	result, err := h.pool.Exec(r.Context(), `UPDATE connectors SET archived_at=now(), updated_at=now()
		WHERE id=$1 AND status='DISABLED' AND (
			connector_type='network' OR EXISTS (
				SELECT 1 FROM cluster_pairings WHERE connector_id=$1 AND status='DELETED'
			)
		)`, connectorID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "归档 Connector 失败")
		return
	}
	if result.RowsAffected() == 0 {
		jsonError(w, http.StatusConflict, "CONNECTOR_NOT_READY_TO_ARCHIVE", "请等待目标集群动态资源清理完成后再确认卸载")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"id": connectorID, "status": "ARCHIVED"})
}

// ForceArchiveConnector is the explicit recovery path when the target-side
// Operator has already been removed and can no longer acknowledge deletion.
func (h *Handler) ForceArchiveConnector(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var body struct {
		ConfirmTargetRemoved bool `json:"confirmTargetRemoved"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || !body.ConfirmTargetRemoved {
		jsonError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "必须确认目标环境资源已手工清理")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开始强制归档事务失败")
		return
	}
	defer tx.Rollback(r.Context())
	connectorID := r.PathValue("id")
	pairing, err := tx.Exec(r.Context(), `UPDATE cluster_pairings SET status='DELETED',updated_at=now()
		WHERE id=(SELECT id FROM cluster_pairings WHERE connector_id=$1 ORDER BY created_at DESC LIMIT 1)
		AND connector_id=$1 AND status IN ('DELETE_REQUESTED','DELETED')`, connectorID)
	if err != nil || pairing.RowsAffected() != 1 {
		jsonError(w, http.StatusConflict, "CONNECTOR_NOT_FORCE_ARCHIVABLE", "执行通道不在等待清理状态")
		return
	}
	connector, err := tx.Exec(r.Context(), `UPDATE connectors SET status='DISABLED',archived_at=now(),updated_at=now()
		WHERE id=$1 AND archived_at IS NULL`, connectorID)
	if err != nil || connector.RowsAffected() != 1 {
		jsonError(w, http.StatusConflict, "CONNECTOR_NOT_FORCE_ARCHIVABLE", "执行通道不存在或已归档")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "强制归档执行通道失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"id": connectorID, "status": "ARCHIVED"})
}

func (h *Handler) UpdateConnectorNamespaces(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Namespaces []string `json:"namespaces"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Namespaces) == 0 {
		jsonError(w, http.StatusBadRequest, "INVALID_NAMESPACES", "至少选择一个 Namespace")
		return
	}
	wanted := make(map[string]struct{}, len(req.Namespaces))
	for _, namespace := range req.Namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" || namespace == "*" {
			jsonError(w, http.StatusBadRequest, "INVALID_NAMESPACES", "Namespace 不能为空或使用通配符")
			return
		}
		wanted[namespace] = struct{}{}
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开始更新事务失败")
		return
	}
	defer tx.Rollback(r.Context())
	var pairingID string
	var available []string
	err = tx.QueryRow(r.Context(), `SELECT id, available_namespaces FROM cluster_pairings
		WHERE connector_id=$1 AND status IN ('CONFIRMED','READY') ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, r.PathValue("id")).Scan(&pairingID, &available)
	if err != nil {
		jsonError(w, http.StatusConflict, "CONNECTOR_NOT_MANAGED", "Connector 当前不可由 Operator 更新")
		return
	}
	availableSet := make(map[string]struct{}, len(available))
	for _, namespace := range available {
		availableSet[namespace] = struct{}{}
	}
	for namespace := range wanted {
		if _, ok := availableSet[namespace]; !ok {
			jsonError(w, http.StatusBadRequest, "NAMESPACE_NOT_AVAILABLE", "选择了目标集群中不存在的 Namespace: "+namespace)
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE cluster_pairings SET config=jsonb_set(config,'{allowedNamespaces}',$2::jsonb), updated_at=now() WHERE id=$1`, pairingID, req.Namespaces)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE connectors SET config=jsonb_set(config,'{allowedNamespaces}',$2::jsonb), updated_at=now() WHERE id=$1`, r.PathValue("id"), req.Namespaces)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新 Namespace 失败")
		return
	}
	jsonResp(w, http.StatusAccepted, map[string]any{"id": r.PathValue("id"), "namespaces": req.Namespaces, "status": "RECONCILING"})
}

func (h *Handler) ConfirmClusterPairing(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	tenantID, err := TenantIDFromContext(r.Context())
	if err != nil {
		jsonError(w, http.StatusForbidden, "TENANT_REQUIRED", "缺少租户上下文")
		return
	}
	var req struct {
		Code                 string   `json:"code"`
		Name                 string   `json:"name"`
		Environment          string   `json:"environment"`
		ProjectID            string   `json:"projectId"`
		AllowedNamespaces    []string `json:"allowedNamespaces"`
		AllowedImagePrefixes []string `json:"allowedImagePrefixes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	if req.Name == "" || len(req.AllowedNamespaces) == 0 || !validEnvironment(req.Environment, false) {
		jsonError(w, http.StatusBadRequest, "INVALID_PAIRING_CONFIG", "集群名、环境和至少一个 Namespace 为必填")
		return
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if req.ProjectID != "" {
		if project, err := h.projects.GetByID(r.Context(), req.ProjectID); err != nil || project == nil {
			jsonError(w, http.StatusBadRequest, "PROJECT_NOT_FOUND", "所选项目不存在")
			return
		}
	}
	for _, ns := range req.AllowedNamespaces {
		if strings.TrimSpace(ns) == "" || ns == "*" {
			jsonError(w, http.StatusBadRequest, "INVALID_PAIRING_CONFIG", "Namespace 不能为空或使用通配符")
			return
		}
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开始配对事务失败")
		return
	}
	defer tx.Rollback(r.Context())
	var pairingID string
	var encryptedKey []byte
	var suggestedAudience, suggestedImagePrefix string
	err = tx.QueryRow(r.Context(), `SELECT id, task_key_ciphertext, suggested_audience, suggested_image_prefix FROM cluster_pairings
		WHERE code_hash=$1 AND status='PENDING' AND expires_at>now() FOR UPDATE`, hashPairingCode(req.Code)).
		Scan(&pairingID, &encryptedKey, &suggestedAudience, &suggestedImagePrefix)
	if err != nil {
		jsonError(w, http.StatusNotFound, "PAIRING_NOT_FOUND", "配对码无效或已过期")
		return
	}
	imagePrefixes := req.AllowedImagePrefixes
	if len(imagePrefixes) == 0 && suggestedImagePrefix != "" {
		imagePrefixes = []string{suggestedImagePrefix}
	}
	config := map[string]any{
		"allowedNamespaces": req.AllowedNamespaces, "allowedImagePrefixes": imagePrefixes,
		"tokenAudience": suggestedAudience, "executionServiceAccount": "warden-executor",
		"connectorName": req.Name, "environment": req.Environment,
	}
	var connectorID string
	err = tx.QueryRow(r.Context(), `INSERT INTO connectors
		(name, connector_type, environment, version, capabilities, config, status, task_key_ciphertext, project_id, tenant_id)
		VALUES ($1,'kubernetes',$2,'0.2.0','[]'::jsonb,$3,'DRAFT',$4,nullif($5,'')::uuid,$6)
		ON CONFLICT (name,tenant_id) DO UPDATE SET environment=EXCLUDED.environment, config=EXCLUDED.config,
			task_key_ciphertext=EXCLUDED.task_key_ciphertext, status='DRAFT', archived_at=NULL,
			project_id=COALESCE(EXCLUDED.project_id, connectors.project_id), updated_at=now()
		RETURNING id`, req.Name, req.Environment, config, encryptedKey, req.ProjectID, tenantID).Scan(&connectorID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "创建 Connector 失败")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE cluster_pairings SET status='CONFIRMED', connector_id=$2,
		config=$3, confirmed_at=now(), updated_at=now() WHERE id=$1`, pairingID, connectorID, config)
	if err != nil || tx.Commit(r.Context()) != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "确认配对失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"pairingId": pairingID, "connectorId": connectorID, "status": "CONFIRMED"})
}

func (h *Handler) MarkOperatorPairingReady(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	token := bearerToken(r)
	currentHash := hashBootstrapToken(token)
	legacyHash := hashLegacyBootstrapToken(token)
	result, err := h.pool.Exec(r.Context(), `UPDATE cluster_pairings SET status='READY',bootstrap_token_hash=$2,ready_at=now(),updated_at=now()
		WHERE id=$1 AND bootstrap_token_hash IN ($2,$3) AND status IN ('CONFIRMED','READY')`,
		r.PathValue("id"), currentHash, legacyHash)
	if err != nil || result.RowsAffected() == 0 {
		jsonError(w, http.StatusUnauthorized, "INVALID_PAIRING_TOKEN", "配对凭证无效")
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "READY"})
}
