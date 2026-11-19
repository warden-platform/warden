package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func validHostPlatform(platform string) bool { return platform == "windows" || platform == "macos" }

func (h *Handler) CreateHostPairing(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		Platform, MachineID, Hostname string
		Commands                      []string
		Metadata                      map[string]any `json:"metadata"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || !validHostPlatform(req.Platform) || strings.TrimSpace(req.MachineID) == "" || strings.TrimSpace(req.Hostname) == "" || len(req.Commands) > 256 {
		jsonError(w, http.StatusBadRequest, "INVALID_PAIRING_REQUEST", "platform、machineId 和 hostname 为必填")
		return
	}
	code, err := randomPairingCode()
	if err != nil {
		jsonError(w, 500, "PAIRING_GENERATION_FAILED", "生成配对码失败")
		return
	}
	bootstrap, err := randomToken(32)
	if err != nil {
		jsonError(w, 500, "PAIRING_GENERATION_FAILED", "生成配对凭证失败")
		return
	}
	key, err := randomToken(32)
	if err != nil {
		jsonError(w, 500, "PAIRING_GENERATION_FAILED", "生成 Connector 凭证失败")
		return
	}
	enc, err := h.credentialCipher.Encrypt([]byte(key))
	if err != nil {
		jsonError(w, 500, "PAIRING_GENERATION_FAILED", "加密 Connector 凭证失败")
		return
	}
	expires := time.Now().Add(10 * time.Minute)
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["hostname"] = req.Hostname
	metadata["machineId"] = req.MachineID
	metadata["platform"] = req.Platform
	var id string
	err = h.pool.QueryRow(r.Context(), `INSERT INTO cluster_pairings (code_hash, bootstrap_token_hash, operator_id, cluster_uid, connector_type, available_services, machine_metadata, task_key_ciphertext, expires_at) VALUES ($1,$2,$3,$3,$4,$5,$6,$7,$8) RETURNING id`, hashPairingCode(code), hashBootstrapToken(bootstrap), req.MachineID, req.Platform, req.Commands, metadata, enc, expires).Scan(&id)
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "保存主机配对请求失败")
		return
	}
	jsonResp(w, http.StatusCreated, map[string]any{"pairingId": id, "code": code, "bootstrapToken": bootstrap, "expiresAt": expires})
}

func (h *Handler) GetHostPairingOptions(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct{ Code, Platform string }
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Code) == "" {
		jsonError(w, 400, "INVALID_PAIRING_CODE", "请输入配对码")
		return
	}
	var commands []string
	var metadata map[string]any
	var expires time.Time
	var platform string
	err := h.pool.QueryRow(r.Context(), `SELECT available_services, machine_metadata, expires_at, connector_type FROM cluster_pairings WHERE code_hash=$1 AND connector_type IN ('windows','macos') AND status='PENDING' AND expires_at>now()`, hashPairingCode(req.Code)).Scan(&commands, &metadata, &expires, &platform)
	if err != nil || (req.Platform != "" && req.Platform != platform) {
		jsonError(w, 404, "PAIRING_NOT_FOUND", "配对码无效或已过期")
		return
	}
	jsonResp(w, 200, map[string]any{"availableCommands": commands, "machine": metadata, "platform": platform, "expiresAt": expires})
}

func (h *Handler) ConfirmHostPairing(w http.ResponseWriter, r *http.Request) {
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
		Code, Platform, Name, Environment, ProjectID string
		AllowedCommands                              []string `json:"allowedCommands"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || !validHostPlatform(req.Platform) || strings.TrimSpace(req.Name) == "" || !validEnvironment(req.Environment, false) {
		jsonError(w, 400, "INVALID_PAIRING_CONFIG", "主机名、平台和环境为必填")
		return
	}
	if req.ProjectID != "" {
		if p, err := h.projects.GetByID(r.Context(), req.ProjectID); err != nil || p == nil {
			jsonError(w, 400, "PROJECT_NOT_FOUND", "所选项目不存在")
			return
		}
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		jsonError(w, 500, "DB_ERROR", "开始配对事务失败")
		return
	}
	defer tx.Rollback(r.Context())
	var pairingID string
	var enc []byte
	var available []string
	var metadata map[string]any
	err = tx.QueryRow(r.Context(), `SELECT id, task_key_ciphertext, available_services, machine_metadata FROM cluster_pairings WHERE code_hash=$1 AND connector_type=$2 AND status='PENDING' AND expires_at>now() FOR UPDATE`, hashPairingCode(req.Code), req.Platform).Scan(&pairingID, &enc, &available, &metadata)
	if err != nil {
		jsonError(w, 404, "PAIRING_NOT_FOUND", "配对码无效或已过期")
		return
	}
	set := map[string]struct{}{}
	for _, c := range available {
		set[c] = struct{}{}
	}
	for _, c := range req.AllowedCommands {
		if _, ok := set[c]; !ok {
			jsonError(w, 400, "COMMAND_NOT_AVAILABLE", "目标主机未发现程序: "+c)
			return
		}
	}
	config := map[string]any{"allowedCommands": req.AllowedCommands, "connectorName": req.Name, "environment": req.Environment, "machine": metadata}
	var connectorID string
	err = tx.QueryRow(r.Context(), `INSERT INTO connectors (name, connector_type, environment, version, capabilities, config, status, task_key_ciphertext, target_host, project_id, tenant_id) VALUES ($1,$2,$3,'0.1.0','[]'::jsonb,$4,'DRAFT',$5,$6,nullif($7,'')::uuid,$8) ON CONFLICT (name,tenant_id) DO UPDATE SET connector_type=EXCLUDED.connector_type, environment=EXCLUDED.environment, config=EXCLUDED.config, task_key_ciphertext=EXCLUDED.task_key_ciphertext, status='DRAFT', archived_at=NULL, project_id=COALESCE(EXCLUDED.project_id, connectors.project_id), updated_at=now() RETURNING id`, req.Name, req.Platform, req.Environment, config, enc, metadata["hostname"], req.ProjectID, tenantID).Scan(&connectorID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE cluster_pairings SET status='CONFIRMED', connector_id=$2, config=$3, confirmed_at=now(), updated_at=now() WHERE id=$1`, pairingID, connectorID, config)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		jsonError(w, 500, "DB_ERROR", "确认主机配对失败")
		return
	}
	jsonResp(w, 200, map[string]any{"pairingId": pairingID, "connectorId": connectorID, "status": "CONFIRMED"})
}
