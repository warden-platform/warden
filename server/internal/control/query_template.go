package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gitee.com/warden-platform/warden/server/internal/connector"
	"gitee.com/warden-platform/warden/server/internal/domain"
)

// resolveQueryTemplate turns an Agent's templateId/templateVersion/bindings into
// the full dispatch parameters for database.query_template.execute. The Agent
// names a template; the control plane resolves it to the immutable published
// canonical AST, merges the runtime bindings, and injects both the AST and the
// derived per-alias masking map. This runs after action-parameter validation
// (the schema only admits templateId/templateVersion/bindings) and before
// parameter hashing, so the injected AST is signed, hashed and audited like any
// other dispatch input and the Connector never resolves or masks from Agent
// input.
func (h *Handler) resolveQueryTemplate(ctx context.Context, res *domain.Resource, params map[string]any) error {
	if h.queryTemplates == nil {
		return fmt.Errorf("查询模板存储不可用")
	}
	templateID, err := requiredTemplateString(params, "templateId")
	if err != nil {
		return err
	}
	templateVersion, err := requiredTemplateVersion(params, "templateVersion")
	if err != nil {
		return err
	}
	if res.IntegrationID == "" {
		return fmt.Errorf("资源未关联数据库集成，无法解析查询模板")
	}
	tmpl, err := h.queryTemplates.GetPublished(ctx, res.IntegrationID, templateID, templateVersion)
	if err != nil {
		return err
	}
	if tmpl == nil {
		return fmt.Errorf("查询模板 ID/名称 %q 版本 %d 不存在或未发布", templateID, templateVersion)
	}
	ast := tmpl.CanonicalAST
	if ast == nil {
		return fmt.Errorf("查询模板 %q 缺少 canonical_ast", templateID)
	}

	// The execution anchors on a database_table resource; ensure it is one of
	// the template's own tables so policy scope and the tables actually queried
	// cannot diverge (an Agent must not execute a template against a resource
	// outside the template's blast radius).
	resSchema, resTable, ok := parseTableResourceID(res.ExternalID)
	if !ok {
		return fmt.Errorf("执行资源不是有效的 database_table 资源")
	}
	if !templateReferencesTable(ast, resSchema, resTable) {
		return fmt.Errorf("执行资源 %s.%s 不属于查询模板 %q 的表范围", resSchema, resTable, templateID)
	}

	// bindings is the only runtime input; identifiers, table/JOIN structure,
	// SELECT fields, aggregates, operators and sort are fixed by the published
	// template and cannot be substituted here.
	if bindings, ok := params["bindings"].(map[string]any); ok && bindings != nil {
		ast["bindings"] = bindings
	} else {
		ast["bindings"] = map[string]any{}
	}
	params["ast"] = ast

	masking, err := h.buildMasking(ctx, res.IntegrationID, ast)
	if err != nil {
		return err
	}
	if len(masking) > 0 {
		params["masking"] = masking
	}
	return nil
}

// buildMasking derives the per-alias output-policy map from the template's
// SELECT columns and the integration's column classifications (§13.2). Output
// aliases whose source column is classified receive allow/mask/hash/deny;
// unclassified columns are left for the Connector's fail-closed default.
func (h *Handler) buildMasking(ctx context.Context, integrationID string, ast map[string]any) (map[string]any, error) {
	if h.queryTemplates == nil {
		return nil, nil
	}
	classifications, err := h.queryTemplates.ListColumnClassifications(ctx, integrationID)
	if err != nil {
		return nil, err
	}
	if len(classifications) == 0 {
		return nil, nil
	}
	policyByColumn := make(map[string]string, len(classifications))
	for _, c := range classifications {
		policyByColumn[c.SchemaName+"\x00"+c.TableName+"\x00"+c.ColumnName] = c.OutputPolicy
	}

	// Resolve each output alias to its (schema, table, column) source.
	aliasToResource := map[string]string{}
	for _, raw := range astSlice(ast["sources"]) {
		if m, ok := raw.(map[string]any); ok {
			aliasToResource[astString(m["alias"])] = astString(m["resourceId"])
		}
	}
	for _, raw := range astSlice(ast["joins"]) {
		if m, ok := raw.(map[string]any); ok {
			if src, ok := m["source"].(map[string]any); ok {
				aliasToResource[astString(src["alias"])] = astString(src["resourceId"])
			}
		}
	}

	masking := map[string]any{}
	for _, raw := range astSlice(ast["select"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		field, _ := item["field"].(map[string]any)
		if field == nil {
			continue // COUNT(*) has no source column
		}
		alias := astString(item["alias"])
		resourceID := aliasToResource[astString(field["source"])]
		schema, table, ok := parseTableResourceID(resourceID)
		if !ok {
			continue
		}
		if policy, found := policyByColumn[schema+"\x00"+table+"\x00"+astString(field["column"])]; found {
			masking[alias] = policy
		}
	}
	return masking, nil
}

func astSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func astString(v any) string {
	s, _ := v.(string)
	return s
}

func parseTableResourceID(id string) (schema, table string, ok bool) {
	parts := strings.Split(id, "/")
	if len(parts) != 3 || parts[0] != "table" {
		return "", "", false
	}
	schema, err := url.PathUnescape(parts[1])
	if err != nil || schema == "" {
		return "", "", false
	}
	table, err = url.PathUnescape(parts[2])
	if err != nil || table == "" {
		return "", "", false
	}
	return schema, table, true
}

// templateReferencesTable reports whether the template's canonical AST reads the
// given (schema, table) as a base source or a joined table.
func templateReferencesTable(ast map[string]any, schema, table string) bool {
	for _, raw := range astSlice(ast["sources"]) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if s, tbl, ok := parseTableResourceID(astString(m["resourceId"])); ok && s == schema && tbl == table {
			return true
		}
	}
	for _, raw := range astSlice(ast["joins"]) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		src, ok := m["source"].(map[string]any)
		if !ok {
			continue
		}
		if s, tbl, ok := parseTableResourceID(astString(src["resourceId"])); ok && s == schema && tbl == table {
			return true
		}
	}
	return false
}

func requiredTemplateString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("缺少参数 %q", key)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("参数 %q 必须为非空字符串", key)
	}
	return s, nil
}

func requiredTemplateVersion(params map[string]any, key string) (int, error) {
	v, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("缺少参数 %q", key)
	}
	switch n := v.(type) {
	case float64:
		if n < 1 || n != math.Trunc(n) {
			return 0, fmt.Errorf("参数 %q 必须为正整数", key)
		}
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil || i < 1 {
			return 0, fmt.Errorf("参数 %q 必须为正整数", key)
		}
		return int(i), nil
	case int:
		if n < 1 {
			return 0, fmt.Errorf("参数 %q 必须为正整数", key)
		}
		return n, nil
	case int64:
		if n < 1 {
			return 0, fmt.Errorf("参数 %q 必须为正整数", key)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("参数 %q 必须为正整数", key)
	}
}

// --- SQL import (admin) ---

type importQueryTemplateRequest struct {
	IntegrationID    string `json:"integrationId"`
	SchemaResourceID string `json:"schemaResourceId"`
	Name             string `json:"name"`
	SQL              string `json:"sql"`
	Owner            string `json:"owner"`
}

// ImportQueryTemplate parses an administrator's MySQL SELECT into a draft
// governed query template (§6). The SQL is converted to the bounded Query AST,
// every referenced table is bound to a Warden database_table resource under the
// integration, and the result is stored as a DRAFT for human review and publish
// — raw SQL is never stored or executed.
func (h *Handler) ImportQueryTemplate(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}
	var req importQueryTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体解析失败")
		return
	}
	if req.IntegrationID == "" || req.SchemaResourceID == "" || req.Name == "" || req.SQL == "" {
		jsonError(w, http.StatusBadRequest, "MISSING_FIELDS", "integrationId、schemaResourceId、name、sql 为必填")
		return
	}
	ctx := r.Context()
	identity := identityFromContext(ctx)
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "缺少认证身份")
		return
	}
	if h.queryTemplates == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}

	imported, err := connector.ImportQuerySQL(req.SQL)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_SQL", domain.SanitizeErrorMessage(err.Error()))
		return
	}

	resourceIDs, err := bindImportedTables(req.SchemaResourceID, imported)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "UNBOUND_TABLE", domain.SanitizeErrorMessage(err.Error()))
		return
	}
	for _, resourceID := range resourceIDs {
		exists, err := h.queryTemplates.TableResourceExists(ctx, req.IntegrationID, resourceID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "DB_ERROR", "校验表资源失败")
			return
		}
		if !exists {
			jsonError(w, http.StatusBadRequest, "UNBOUND_TABLE", "表资源未在 Warden 中注册或未激活："+resourceID)
			return
		}
	}

	canonicalJSON, err := json.Marshal(imported.AST)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "ENCODE_AST_FAILED", "编码查询 AST 失败")
		return
	}
	var canonicalAST map[string]any
	if err := json.Unmarshal(canonicalJSON, &canonicalAST); err != nil {
		jsonError(w, http.StatusInternalServerError, "ENCODE_AST_FAILED", "编码查询 AST 失败")
		return
	}
	sum := sha256.Sum256(canonicalJSON)

	version, err := h.queryTemplates.NextVersion(ctx, req.IntegrationID, req.Name)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "计算模板版本失败")
		return
	}

	properties := make(map[string]any, len(imported.Parameters))
	for _, p := range imported.Parameters {
		properties[p] = map[string]any{"type": []string{"string", "number", "boolean", "array"}}
	}

	tmpl := &domain.QueryTemplate{
		IntegrationID:    req.IntegrationID,
		SchemaResourceID: req.SchemaResourceID,
		Name:             req.Name,
		Version:          version,
		Status:           "DRAFT",
		CanonicalAST:     canonicalAST,
		ParameterSchema: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"additionalProperties": false,
		},
		ResourceBindings: resourceIDs,
		MaxRows:          100,
		MaxResultBytes:   1048576,
		TimeoutSeconds:   30,
		CostPolicy:       map[string]any{},
		Sensitivity:      "INTERNAL",
		Owner:            req.Owner,
		CreatedBy:        identity.Principal.Name,
		TemplateHash:     hex.EncodeToString(sum[:]),
		TenantID:         identity.TenantID,
	}
	if err := h.queryTemplates.Create(ctx, tmpl); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存查询模板失败")
		return
	}
	jsonResp(w, http.StatusCreated, tmpl)
}

// bindImportedTables assigns each table reference a bound Warden resource id
// (table/<schema>/<table>). Schema-qualified tables keep their own schema; bare
// tables inherit the template's schema resource. It returns the deduplicated
// resource ids for existence verification and template resource_bindings.
func bindImportedTables(schemaResourceID string, imported *connector.ImportedQuery) ([]string, error) {
	schemaName, err := parseSchemaExternalID(schemaResourceID)
	if err != nil {
		return nil, err
	}
	byAlias := make(map[string]connector.ImportedTable, len(imported.Tables))
	for _, t := range imported.Tables {
		byAlias[t.Alias] = t
	}

	var resourceIDs []string
	seen := map[string]bool{}
	resolve := func(source *connector.QuerySource) error {
		t, ok := byAlias[source.Alias]
		if !ok {
			return fmt.Errorf("查询引用了未解析的表别名 %q", source.Alias)
		}
		schema := t.Schema
		if schema == "" {
			schema = schemaName
		}
		if schema == "" {
			return fmt.Errorf("表 %q 未限定 schema，且未提供 schemaResourceId", t.Table)
		}
		source.ResourceID = "table/" + schema + "/" + t.Table
		if !seen[source.ResourceID] {
			seen[source.ResourceID] = true
			resourceIDs = append(resourceIDs, source.ResourceID)
		}
		return nil
	}
	for i := range imported.AST.Sources {
		if err := resolve(&imported.AST.Sources[i]); err != nil {
			return nil, err
		}
	}
	for i := range imported.AST.Joins {
		if err := resolve(&imported.AST.Joins[i].Source); err != nil {
			return nil, err
		}
	}
	return resourceIDs, nil
}

func parseSchemaExternalID(id string) (string, error) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] != "schema" || parts[1] == "" {
		return "", fmt.Errorf("无效的 database_schema 资源标识 %q", id)
	}
	schema, err := url.PathUnescape(parts[1])
	if err != nil || schema == "" || strings.Contains(schema, "/") {
		return "", fmt.Errorf("无效的 database_schema 资源标识 %q", id)
	}
	return schema, nil
}

// --- Template admin: list, get, publish review ---

func (h *Handler) ListQueryTemplates(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil || h.queryTemplates == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}
	integrationID := r.URL.Query().Get("integrationId")
	status := r.URL.Query().Get("status")
	limit, offset := listLimitOffset(r, 50)
	templates, err := h.queryTemplates.List(r.Context(), integrationID, status, limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询模板失败")
		return
	}
	if templates == nil {
		templates = []domain.QueryTemplate{}
	}
	jsonResp(w, http.StatusOK, templates)
}

func (h *Handler) GetQueryTemplate(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil || h.queryTemplates == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}
	tmpl, err := h.queryTemplates.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询模板失败")
		return
	}
	if tmpl == nil {
		jsonError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "查询模板不存在")
		return
	}
	jsonResp(w, http.StatusOK, tmpl)
}

// SubmitTemplatePublish creates a PENDING publish request for a DRAFT template.
// The template is not executable until the request is APPROVED.
func (h *Handler) SubmitTemplatePublish(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil || h.queryTemplates == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "缺少认证身份")
		return
	}
	requestedBy := identity.Principal.Name
	if requestedBy == "" {
		requestedBy = identity.Principal.ID
	}
	request, err := h.queryTemplates.CreatePublishRequest(r.Context(), r.PathValue("id"), requestedBy)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "PUBLISH_SUBMIT_FAILED", domain.SanitizeErrorMessage(err.Error()))
		return
	}
	jsonResp(w, http.StatusCreated, request)
}

func (h *Handler) ListTemplatePublishRequests(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil || h.queryTemplates == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}
	status := r.URL.Query().Get("status")
	limit, offset := listLimitOffset(r, 50)
	requests, err := h.queryTemplates.ListPublishRequests(r.Context(), status, limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询发布审批请求失败")
		return
	}
	if requests == nil {
		requests = []domain.TemplatePublishRequest{}
	}
	jsonResp(w, http.StatusOK, requests)
}

// ApproveTemplatePublish approves a pending publish request and transitions the
// template DRAFT → PUBLISHED atomically.
func (h *Handler) ApproveTemplatePublish(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil || h.queryTemplates == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}
	identity := identityFromContext(r.Context())
	if identity == nil || identity.Principal == nil {
		jsonError(w, http.StatusUnauthorized, "UNAUTHORIZED", "缺少认证身份")
		return
	}
	approvedBy := identity.Principal.Name
	if approvedBy == "" {
		approvedBy = identity.Principal.ID
	}
	err := h.queryTemplates.ApprovePublish(r.Context(), r.PathValue("requestId"), r.PathValue("id"), approvedBy)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "PUBLISH_APPROVE_FAILED", domain.SanitizeErrorMessage(err.Error()))
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"id": r.PathValue("requestId"), "status": "APPROVED"})
}

// RejectTemplatePublish rejects a pending publish request; the template stays
// DRAFT so it can be revised and resubmitted.
func (h *Handler) RejectTemplatePublish(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil || h.queryTemplates == nil {
		jsonError(w, http.StatusServiceUnavailable, "NO_DATABASE", "数据库未连接，请先启动 PostgreSQL")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	err := h.queryTemplates.RejectPublish(r.Context(), r.PathValue("requestId"), r.PathValue("id"), body.Reason)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "PUBLISH_REJECT_FAILED", domain.SanitizeErrorMessage(err.Error()))
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"id": r.PathValue("requestId"), "status": "REJECTED"})
}

func listLimitOffset(r *http.Request, defaultLimit int) (int, int) {
	limit, offset := defaultLimit, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if limit > 500 {
		limit = 500
	}
	return limit, offset
}
