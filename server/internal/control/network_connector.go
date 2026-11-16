package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

// CreateNetworkConnector provisions the Connector identity before the
// network-side process starts. The task signing key is returned exactly once
// over the authenticated admin channel; it is never persisted in plaintext or
// included in audit records. The process can then self-register using the
// returned key, just like a paired Kubernetes/Linux Connector.
func (h *Handler) CreateNetworkConnector(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		databaseUnavailable(w)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Environment string `json:"environment"`
		ProjectID   string `json:"projectId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_NETWORK_CONNECTOR", "请求体无效")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Environment = strings.TrimSpace(req.Environment)
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if req.Name == "" || len(req.Name) > 255 || strings.ContainsAny(req.Name, "\"'`\\/\r\n") || !validEnvironment(req.Environment, false) {
		jsonError(w, http.StatusBadRequest, "INVALID_NETWORK_CONNECTOR", "名称和环境无效；环境必须是 development、testing 或 production")
		return
	}
	if req.ProjectID != "" {
		project, err := h.projects.GetByID(r.Context(), req.ProjectID)
		if err != nil || project == nil {
			jsonError(w, http.StatusBadRequest, "PROJECT_NOT_FOUND", "所选项目不存在")
			return
		}
	}
	taskKey, err := randomToken(32)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "NETWORK_CONNECTOR_GENERATION_FAILED", "生成 Connector 凭据失败")
		return
	}
	encryptedKey, err := h.credentialCipher.Encrypt([]byte(taskKey))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "NETWORK_CONNECTOR_GENERATION_FAILED", "加密 Connector 凭据失败")
		return
	}
	config := map[string]any{"connectorName": req.Name, "environment": req.Environment, "kind": "h3c_network"}
	tenantID := domain.TenantIDFromContext(r.Context())
	if tenantID == "" {
		jsonError(w, http.StatusUnauthorized, "TENANT_CONTEXT_REQUIRED", "缺少租户上下文")
		return
	}
	var connectorID string
	err = h.pool.QueryRow(r.Context(), `INSERT INTO connectors
		(name, connector_type, environment, version, capabilities, config, status, task_key_ciphertext, project_id, tenant_id)
		VALUES ($1,'network',$2,'0.2.0',$3,$4,'DRAFT',$5,nullif($6,'')::uuid,$7)
		ON CONFLICT (name,tenant_id) DO UPDATE SET connector_type='network', environment=EXCLUDED.environment,
			config=EXCLUDED.config, task_key_ciphertext=EXCLUDED.task_key_ciphertext, status='DRAFT', archived_at=NULL,
			project_id=COALESCE(EXCLUDED.project_id, connectors.project_id), updated_at=now()
		RETURNING id`, req.Name, req.Environment,
		[]string{domain.ChangeSafetyCapability,
			"network.device.get@v1", "network.device.audit@v1", "network.device.health@v1",
			"network.interface.list@v1", "network.vlan.list@v1", "network.topology.lldp@v1",
			"network.link_aggregation.status@v1", "network.stp.status@v1", "network.port.health@v1",
			"network.mac.lookup@v1", "network.arp.lookup@v1",
			"network.interface.description.preview@v1", "network.interface.description.update@v1",
			"network.interface.access_vlan.preview@v1", "network.interface.access_vlan.update@v1",
			"network.interface.trunk_vlan.preview@v1", "network.interface.trunk_vlan.add@v1", "network.interface.trunk_vlan.remove@v1"}, config, encryptedKey, req.ProjectID, tenantID).Scan(&connectorID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "创建 Network Connector 失败")
		return
	}
	_ = h.writeAudit(r.Context(), "network_connector.provisioned", nil, nil, "", "")
	jsonResp(w, http.StatusCreated, map[string]any{
		"id": connectorID, "name": req.Name, "connectorType": "network", "status": "DRAFT",
		"taskSigningKey":    taskKey,
		"credentialWarning": "该 taskSigningKey 只返回本次；请写入 Network Connector 的受保护文件后立即从终端输出和历史记录中清除。",
	})
}
