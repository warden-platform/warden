package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) CreateLinuxPairing(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	if !allowLoginAttempt(requestClientAddress(r), "linux-pairing", time.Now()) {
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
		MachineID  string         `json:"machineId"`
		Hostname   string         `json:"hostname"`
		Services   []string       `json:"services"`
		Containers []string       `json:"containers"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.MachineID) == "" || strings.TrimSpace(req.Hostname) == "" ||
		len(req.MachineID) > 255 || len(req.Hostname) > 255 || len(req.Services) > 1000 || len(req.Containers) > 1000 {
		jsonError(w, http.StatusBadRequest, "INVALID_PAIRING_REQUEST", "machineId 和 hostname 为必填")
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
		(code_hash, bootstrap_token_hash, operator_id, cluster_uid, connector_type,
		 available_services, available_containers, machine_metadata, task_key_ciphertext, expires_at)
		VALUES ($1,$2,$3,$3,'linux',$4,$5,$6,$7,$8) RETURNING id`, hashPairingCode(code),
		hashBootstrapToken(bootstrapToken), req.MachineID, req.Services, req.Containers, req.Metadata, encryptedKey, expiresAt).Scan(&pairingID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存 Linux 配对请求失败")
		return
	}
	jsonResp(w, http.StatusCreated, map[string]any{
		"pairingId": pairingID, "code": code, "bootstrapToken": bootstrapToken, "expiresAt": expiresAt,
	})
}

func (h *Handler) GetLinuxPairingOptions(w http.ResponseWriter, r *http.Request) {
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
	var services []string
	var containers []string
	var metadata map[string]any
	var expiresAt time.Time
	err := h.pool.QueryRow(r.Context(), `SELECT available_services, available_containers, machine_metadata, expires_at FROM cluster_pairings
		WHERE code_hash=$1 AND connector_type='linux' AND status='PENDING' AND expires_at>now()`,
		hashPairingCode(req.Code)).Scan(&services, &containers, &metadata, &expiresAt)
	if err != nil {
		jsonError(w, http.StatusNotFound, "PAIRING_NOT_FOUND", "配对码无效或已过期")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"availableServices": services, "availableContainers": containers, "machine": metadata, "expiresAt": expiresAt})
}

func (h *Handler) ConfirmLinuxPairing(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	tenantID, err := TenantIDFromContext(r.Context())
	if err != nil {
		jsonError(w, http.StatusForbidden, "TENANT_REQUIRED", "缺少租户上下文")
		return
	}
	if err := h.enforceConnectorQuota(r.Context()); err != nil {
		jsonError(w, http.StatusTooManyRequests, "TENANT_QUOTA_EXCEEDED", "已达到该租户的 Connector 配额")
		return
	}
	var req struct {
		Code              string   `json:"code"`
		Name              string   `json:"name"`
		Environment       string   `json:"environment"`
		ProjectID         string   `json:"projectId"`
		AllowedServices   []string `json:"allowedServices"`
		AllowedContainers []string `json:"allowedContainers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" ||
		!validEnvironment(req.Environment, false) {
		jsonError(w, http.StatusBadRequest, "INVALID_PAIRING_CONFIG", "主机名和环境为必填")
		return
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if req.ProjectID != "" {
		if project, err := h.projects.GetByID(r.Context(), req.ProjectID); err != nil || project == nil {
			jsonError(w, http.StatusBadRequest, "PROJECT_NOT_FOUND", "所选项目不存在")
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
	var available []string
	var availableContainers []string
	var metadata map[string]any
	err = tx.QueryRow(r.Context(), `SELECT id, task_key_ciphertext, available_services, available_containers, machine_metadata FROM cluster_pairings
		WHERE code_hash=$1 AND connector_type='linux' AND status='PENDING' AND expires_at>now() FOR UPDATE`,
		hashPairingCode(req.Code)).Scan(&pairingID, &encryptedKey, &available, &availableContainers, &metadata)
	if err != nil {
		jsonError(w, http.StatusNotFound, "PAIRING_NOT_FOUND", "配对码无效或已过期")
		return
	}
	allowedSet := make(map[string]struct{}, len(available))
	for _, service := range available {
		allowedSet[service] = struct{}{}
	}
	for _, service := range req.AllowedServices {
		if _, ok := allowedSet[service]; !ok {
			jsonError(w, http.StatusBadRequest, "SERVICE_NOT_AVAILABLE", "目标主机不存在服务: "+service)
			return
		}
	}
	containerSet := make(map[string]struct{}, len(availableContainers))
	for _, container := range availableContainers {
		containerSet[container] = struct{}{}
	}
	for _, container := range req.AllowedContainers {
		if _, ok := containerSet[container]; !ok {
			jsonError(w, http.StatusBadRequest, "CONTAINER_NOT_AVAILABLE", "目标主机不存在容器: "+container)
			return
		}
	}
	config := map[string]any{"allowedServices": req.AllowedServices, "connectorName": req.Name,
		"allowedContainers": req.AllowedContainers, "environment": req.Environment, "machine": metadata}
	var connectorID string
	err = tx.QueryRow(r.Context(), `INSERT INTO connectors
		(name, connector_type, environment, version, capabilities, config, status, task_key_ciphertext, target_host, project_id, tenant_id)
		VALUES ($1,'linux',$2,'0.1.0','[]'::jsonb,$3,'DRAFT',$4,$5,nullif($6,'')::uuid,$7)
		ON CONFLICT (name,tenant_id) DO UPDATE SET connector_type='linux', environment=EXCLUDED.environment,
		config=EXCLUDED.config, task_key_ciphertext=EXCLUDED.task_key_ciphertext, status='DRAFT', archived_at=NULL,
		project_id=COALESCE(EXCLUDED.project_id, connectors.project_id), updated_at=now()
		RETURNING id`, req.Name, req.Environment, config, encryptedKey, metadata["hostname"], req.ProjectID, tenantID).Scan(&connectorID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE cluster_pairings SET status='CONFIRMED', connector_id=$2,
			config=$3, confirmed_at=now(), updated_at=now() WHERE id=$1`, pairingID, connectorID, config)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "确认 Linux 配对失败")
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{"pairingId": pairingID, "connectorId": connectorID, "status": "CONFIRMED"})
}

func (h *Handler) UpdateConnectorServices(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Services   []string `json:"services"`
		Containers []string `json:"containers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_SERVICES", "服务范围格式无效")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "开始更新事务失败")
		return
	}
	defer tx.Rollback(r.Context())
	var pairingID string
	var available []string
	var availableContainers []string
	err = tx.QueryRow(r.Context(), `SELECT id, available_services, available_containers FROM cluster_pairings
		WHERE connector_id=$1 AND connector_type='linux' AND status IN ('CONFIRMED','READY')
		ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, r.PathValue("id")).Scan(&pairingID, &available, &availableContainers)
	if err != nil {
		jsonError(w, http.StatusConflict, "CONNECTOR_NOT_MANAGED", "Linux Connector 当前不可更新")
		return
	}
	availableSet := make(map[string]struct{}, len(available))
	for _, service := range available {
		availableSet[service] = struct{}{}
	}
	for _, service := range req.Services {
		if _, ok := availableSet[service]; !ok {
			jsonError(w, http.StatusBadRequest, "SERVICE_NOT_AVAILABLE", "目标主机不存在服务: "+service)
			return
		}
	}
	availableContainerSet := make(map[string]struct{}, len(availableContainers))
	for _, container := range availableContainers {
		availableContainerSet[container] = struct{}{}
	}
	for _, container := range req.Containers {
		if _, ok := availableContainerSet[container]; !ok {
			jsonError(w, http.StatusBadRequest, "CONTAINER_NOT_AVAILABLE", "目标主机不存在容器: "+container)
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE cluster_pairings SET config=jsonb_set(jsonb_set(config,'{allowedServices}',$2::jsonb),'{allowedContainers}',$3::jsonb), updated_at=now() WHERE id=$1`, pairingID, req.Services, req.Containers)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE connectors SET config=jsonb_set(jsonb_set(config,'{allowedServices}',$2::jsonb),'{allowedContainers}',$3::jsonb), updated_at=now() WHERE id=$1`, r.PathValue("id"), req.Services, req.Containers)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "更新服务范围失败")
		return
	}
	jsonResp(w, http.StatusAccepted, map[string]any{"id": r.PathValue("id"), "services": req.Services, "containers": req.Containers, "status": "RECONCILING"})
}
