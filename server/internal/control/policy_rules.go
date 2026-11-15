package control

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
)

var policySelectorPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,150}$`)

type policyRuleInput struct {
	Name          string                `json:"name"`
	ProjectID     string                `json:"project_id"`
	ResourceType  string                `json:"resource_type"`
	ActionName    string                `json:"action_name"`
	Environment   string                `json:"environment"`
	RiskLevel     string                `json:"risk_level"`
	PrincipalKind string                `json:"principal_kind"`
	Decision      domain.DecisionResult `json:"decision"`
	Priority      int                   `json:"priority"`
	Enabled       *bool                 `json:"enabled"`
	Description   string                `json:"description"`
}

func (input policyRuleInput) rule() domain.PolicyMatrixRule {
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return domain.PolicyMatrixRule{
		Name: strings.TrimSpace(input.Name), ProjectID: strings.TrimSpace(input.ProjectID),
		ResourceType: strings.TrimSpace(input.ResourceType), ActionName: strings.TrimSpace(input.ActionName),
		Environment: strings.TrimSpace(input.Environment), RiskLevel: strings.TrimSpace(input.RiskLevel),
		PrincipalKind: strings.TrimSpace(input.PrincipalKind), Decision: input.Decision,
		Priority: input.Priority, Enabled: enabled, Description: strings.TrimSpace(input.Description),
	}
}

func (h *Handler) validatePolicyRule(r *domain.PolicyMatrixRule) string {
	if r.Name == "" || len(r.Name) > 120 {
		return "规则名称为必填且不能超过 120 个字符"
	}
	if r.ResourceType == "" {
		r.ResourceType = "*"
	}
	if r.ActionName == "" {
		r.ActionName = "*"
	}
	if r.Environment == "" {
		r.Environment = "*"
	}
	if r.ResourceType != "*" && !policySelectorPattern.MatchString(r.ResourceType) {
		return "资源类型格式无效"
	}
	if r.ActionName != "*" && !policySelectorPattern.MatchString(r.ActionName) {
		return "动作名称格式无效"
	}
	if r.Environment != "*" && r.Environment != "development" && r.Environment != "testing" && r.Environment != "production" {
		return "环境必须是所有、测试或生产"
	}
	if r.RiskLevel != "" && r.RiskLevel != "low" && r.RiskLevel != "medium" && r.RiskLevel != "high" && r.RiskLevel != "critical" {
		return "风险等级无效"
	}
	if r.PrincipalKind != "" && r.PrincipalKind != "user" && r.PrincipalKind != "agent_type" && r.PrincipalKind != "agent_instance" && r.PrincipalKind != "workload" {
		return "执行身份类型无效"
	}
	if r.Decision != domain.DecisionAllow && r.Decision != domain.DecisionRequireApproval && r.Decision != domain.DecisionDeny {
		return "规则决策无效"
	}
	if r.Priority < 0 || r.Priority > 10000 {
		return "优先级必须在 0 到 10000 之间"
	}
	if len(r.Description) > 500 {
		return "说明不能超过 500 个字符"
	}
	return ""
}

func (h *Handler) validatePolicyRuleReferences(ctx *http.Request, rule *domain.PolicyMatrixRule) string {
	if rule.ProjectID != "" {
		project, err := h.projects.GetByID(ctx.Context(), rule.ProjectID)
		if err != nil || project == nil {
			return "所选项目不存在"
		}
	}
	if rule.ActionName != "*" {
		action, err := h.actions.GetByName(ctx.Context(), rule.ActionName)
		if err != nil || action == nil {
			return "所选动作不存在"
		}
		if rule.ResourceType != "*" && action.ResourceType != rule.ResourceType &&
			!(action.ResourceType == "kubernetes_resource" && genericKubernetesActionApplies(rule.ResourceType, action.Name)) {
			return "动作与资源类型不匹配"
		}
	}
	return ""
}

func (h *Handler) ListPolicyRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.policyRules.List(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询策略规则失败")
		return
	}
	enrichPolicyRuleConflicts(rules)
	jsonResp(w, http.StatusOK, rules)
}

func (h *Handler) CreatePolicyRule(w http.ResponseWriter, r *http.Request) {
	var input policyRuleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	rule := input.rule()
	if message := h.validatePolicyRule(&rule); message != "" {
		jsonError(w, http.StatusBadRequest, "INVALID_POLICY_RULE", message)
		return
	}
	if message := h.validatePolicyRuleReferences(r, &rule); message != "" {
		jsonError(w, http.StatusBadRequest, "INVALID_POLICY_RULE", message)
		return
	}
	if err := h.policyRules.Create(r.Context(), &rule); err != nil {
		jsonError(w, http.StatusInternalServerError, "POLICY_RULE_CREATE_FAILED", "创建策略规则失败")
		return
	}
	_ = h.writeAudit(r.Context(), "policy.rule.created", nil, nil, "", "")
	jsonResp(w, http.StatusCreated, rule)
}

func (h *Handler) UpdatePolicyRule(w http.ResponseWriter, r *http.Request) {
	var input policyRuleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	rule := input.rule()
	rule.ID = r.PathValue("id")
	if message := h.validatePolicyRule(&rule); message != "" {
		jsonError(w, http.StatusBadRequest, "INVALID_POLICY_RULE", message)
		return
	}
	if message := h.validatePolicyRuleReferences(r, &rule); message != "" {
		jsonError(w, http.StatusBadRequest, "INVALID_POLICY_RULE", message)
		return
	}
	if err := h.policyRules.Update(r.Context(), &rule); err != nil {
		if err == pgx.ErrNoRows {
			jsonError(w, http.StatusNotFound, "POLICY_RULE_NOT_FOUND", "策略规则不存在")
		} else {
			jsonError(w, http.StatusInternalServerError, "POLICY_RULE_UPDATE_FAILED", "更新策略规则失败")
		}
		return
	}
	_ = h.writeAudit(r.Context(), "policy.rule.updated", nil, nil, "", "")
	jsonResp(w, http.StatusOK, rule)
}

func (h *Handler) DeletePolicyRule(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.policyRules.Delete(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "POLICY_RULE_DELETE_FAILED", "删除策略规则失败")
		return
	}
	if !deleted {
		jsonError(w, http.StatusNotFound, "POLICY_RULE_NOT_FOUND", "策略规则不存在")
		return
	}
	_ = h.writeAudit(r.Context(), "policy.rule.deleted", nil, nil, "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CopyPolicyRule(w http.ResponseWriter, r *http.Request) {
	rule, err := h.policyRules.GetByID(r.Context(), r.PathValue("id"))
	if err != nil || rule == nil {
		jsonError(w, http.StatusNotFound, "POLICY_RULE_NOT_FOUND", "策略规则不存在")
		return
	}
	rule.ID = ""
	rule.Name = truncatePolicyRuleName(rule.Name + "（副本）")
	rule.Enabled = false
	rule.HitCount = 0
	rule.LastHitAt = nil
	rule.LegacyApprovalRuleID = ""
	if err := h.policyRules.Create(r.Context(), rule); err != nil {
		jsonError(w, http.StatusInternalServerError, "POLICY_RULE_COPY_FAILED", "复制策略规则失败")
		return
	}
	_ = h.writeAudit(r.Context(), "policy.rule.created", nil, nil, "", "")
	jsonResp(w, http.StatusCreated, rule)
}

func truncatePolicyRuleName(name string) string {
	runes := []rune(name)
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return name
}

func (h *Handler) SimulatePolicyRule(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProjectID     string                `json:"project_id"`
		ResourceType  string                `json:"resource_type"`
		ActionName    string                `json:"action_name"`
		Environment   string                `json:"environment"`
		RiskLevel     string                `json:"risk_level"`
		PrincipalKind string                `json:"principal_kind"`
		BaseDecision  domain.DecisionResult `json:"base_decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "模拟请求格式不正确")
		return
	}
	if input.BaseDecision == "" {
		input.BaseDecision = domain.DecisionRequireApproval
	}
	if input.BaseDecision != domain.DecisionAllow && input.BaseDecision != domain.DecisionRequireApproval && input.BaseDecision != domain.DecisionDeny {
		jsonError(w, http.StatusBadRequest, "INVALID_BASE_DECISION", "Rego 初始决策无效")
		return
	}
	rule, err := h.policyRules.Match(r.Context(), domain.PolicyMatchInput{
		ProjectID: input.ProjectID, ResourceType: input.ResourceType, ActionName: input.ActionName,
		Environment: input.Environment, RiskLevel: input.RiskLevel, PrincipalKind: input.PrincipalKind,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "POLICY_SIMULATION_FAILED", "策略模拟失败")
		return
	}
	effective := combinePolicyDecision(input.BaseDecision, rule)
	_ = h.writeAudit(r.Context(), "policy.rule.simulated", nil, nil, "", "")
	jsonResp(w, http.StatusOK, map[string]any{
		"base_decision": input.BaseDecision, "effective_decision": effective,
		"matched_rule": rule, "rego_deny_protected": input.BaseDecision == domain.DecisionDeny,
	})
}

func combinePolicyDecision(base domain.DecisionResult, rule *domain.PolicyMatrixRule) domain.DecisionResult {
	if base == domain.DecisionDeny || rule == nil {
		return base
	}
	switch rule.Decision {
	case domain.DecisionDeny:
		return domain.DecisionDeny
	case domain.DecisionRequireApproval:
		return domain.DecisionRequireApproval
	case domain.DecisionAllow:
		return domain.DecisionAllow
	default:
		return base
	}
}

func enrichPolicyRuleConflicts(rules []domain.PolicyMatrixRule) {
	for i := range rules {
		rules[i].ConflictIDs = []string{}
		if !rules[i].Enabled {
			continue
		}
		for j := range rules {
			if i == j || !rules[j].Enabled || !policyRulesOverlap(rules[i], rules[j]) {
				continue
			}
			if rules[i].Priority == rules[j].Priority {
				rules[i].ConflictIDs = append(rules[i].ConflictIDs, rules[j].ID)
			}
			if rules[j].Priority > rules[i].Priority && policyRuleCovers(rules[j], rules[i]) {
				rules[i].ShadowedBy = rules[j].ID
			}
		}
	}
}

func policyRulesOverlap(a, b domain.PolicyMatrixRule) bool {
	return optionalOverlap(a.ProjectID, b.ProjectID) && wildcardOverlap(a.ResourceType, b.ResourceType) &&
		wildcardOverlap(a.ActionName, b.ActionName) && wildcardOverlap(a.Environment, b.Environment) &&
		optionalOverlap(a.RiskLevel, b.RiskLevel) && optionalOverlap(a.PrincipalKind, b.PrincipalKind)
}

func policyRuleCovers(high, low domain.PolicyMatrixRule) bool {
	return optionalCovers(high.ProjectID, low.ProjectID) && wildcardCovers(high.ResourceType, low.ResourceType) &&
		wildcardCovers(high.ActionName, low.ActionName) && wildcardCovers(high.Environment, low.Environment) &&
		optionalCovers(high.RiskLevel, low.RiskLevel) && optionalCovers(high.PrincipalKind, low.PrincipalKind)
}

func wildcardOverlap(a, b string) bool     { return a == "*" || b == "*" || a == b }
func optionalOverlap(a, b string) bool     { return a == "" || b == "" || a == b }
func wildcardCovers(high, low string) bool { return high == "*" || high == low }
func optionalCovers(high, low string) bool { return high == "" || high == low }
