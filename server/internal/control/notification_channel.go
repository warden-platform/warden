package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"gitee.com/pam-platform/pam/server/internal/domain"
)

func (h *Handler) GetDingTalkChannel(w http.ResponseWriter, r *http.Request) {
	if h.notificationChannels == nil {
		databaseUnavailable(w)
		return
	}
	channel, err := h.notificationChannels.Get(r.Context(), dingTalkChannelType)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "查询钉钉通知渠道失败")
		return
	}
	if channel == nil {
		channel = defaultDingTalkChannel()
	}
	jsonResp(w, http.StatusOK, notificationChannelResponse(channel, h.dingTalk != nil, h.dingTalkCoolApp != nil, h.dingTalkCard != nil))
}

func (h *Handler) UpdateDingTalkChannel(w http.ResponseWriter, r *http.Request) {
	if h.notificationChannels == nil {
		databaseUnavailable(w)
		return
	}
	var body struct {
		Name    string   `json:"name"`
		Enabled bool     `json:"enabled"`
		Events  []string `json:"events"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "请求体格式不正确")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len([]rune(body.Name)) > 100 {
		jsonError(w, http.StatusBadRequest, "INVALID_CHANNEL", "渠道名称长度必须为 1-100 个字符")
		return
	}
	events, err := validateNotificationEvents(body.Events)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "INVALID_CHANNEL", err.Error())
		return
	}
	if body.Enabled && h.dingTalk == nil && h.dingTalkCard == nil {
		jsonError(w, http.StatusConflict, "DINGTALK_NOT_CONFIGURED", "请先通过 Secret 文件配置钉钉 Webhook 或互动卡片")
		return
	}
	channel := &domain.NotificationChannel{Type: dingTalkChannelType, Name: body.Name, Enabled: body.Enabled, Events: events}
	if err := h.notificationChannels.Upsert(r.Context(), channel); err != nil {
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存钉钉通知渠道失败")
		return
	}
	identity := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "notification.updated", channel.ID, "钉钉审批通知配置已更新", map[string]any{
		"enabled": channel.Enabled, "events": channel.Events,
	}, identity.Principal.ID, string(identity.Principal.Kind))
	jsonResp(w, http.StatusOK, notificationChannelResponse(channel, h.dingTalk != nil, h.dingTalkCoolApp != nil, h.dingTalkCard != nil))
}

func (h *Handler) TestDingTalkChannel(w http.ResponseWriter, r *http.Request) {
	if h.dingTalk == nil && h.dingTalkCard == nil {
		jsonError(w, http.StatusConflict, "DINGTALK_NOT_CONFIGURED", "钉钉 Webhook 或互动卡片尚未配置")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	notification := approvalNotification{
		Event: "notification.test", Status: "TEST", RequestID: fmt.Sprintf("channel-test-%d", time.Now().UnixNano()),
		PrincipalName: identityFromContext(r.Context()).Principal.Name,
		Reason:        "HighTower PAM 钉钉审批通知渠道连通性测试",
	}
	var err error
	if h.dingTalkCard != nil {
		err = h.dingTalkCard.send(ctx, notification)
	} else {
		err = h.dingTalk.send(ctx, notification)
	}
	identity := identityFromContext(r.Context())
	if err != nil {
		h.writeNotificationAudit(r.Context(), "notification.failed", "dingtalk", "钉钉通知渠道测试失败", map[string]any{
			"event": "notification.test", "error": safeNotificationError(err),
		}, identity.Principal.ID, string(identity.Principal.Kind))
		jsonError(w, http.StatusBadGateway, "DINGTALK_DELIVERY_FAILED", "钉钉渠道测试失败，请检查机器人安全设置和网络")
		return
	}
	h.writeNotificationAudit(r.Context(), "notification.tested", "dingtalk", "钉钉通知渠道测试成功", map[string]any{
		"event": "notification.test",
	}, identity.Principal.ID, string(identity.Principal.Kind))
	jsonResp(w, http.StatusOK, map[string]string{"status": "SUCCEEDED"})
}

func defaultDingTalkChannel() *domain.NotificationChannel {
	return &domain.NotificationChannel{Type: dingTalkChannelType, Name: "钉钉审批通知", Enabled: true, Events: append([]string(nil), allowedDingTalkEvents...), TenantID: "platform"}
}

func notificationChannelResponse(channel *domain.NotificationChannel, webhookConfigured, coolAppConfigured, cardConfigured bool) map[string]any {
	return map[string]any{
		"id": channel.ID, "type": channel.Type, "name": channel.Name,
		"enabled": channel.Enabled, "events": channel.Events, "configured": webhookConfigured || cardConfigured,
		"webhook_configured": webhookConfigured, "coolapp_configured": coolAppConfigured,
		"card_configured": cardConfigured, "stream_configured": cardConfigured,
		"credential_source": "secret_file", "created_at": channel.CreatedAt, "updated_at": channel.UpdatedAt,
	}
}

func validateNotificationEvents(input []string) ([]string, error) {
	allowed := make(map[string]struct{}, len(allowedDingTalkEvents))
	for _, event := range allowedDingTalkEvents {
		allowed[event] = struct{}{}
	}
	unique := make(map[string]struct{}, len(input))
	for _, event := range input {
		event = strings.TrimSpace(event)
		if _, ok := allowed[event]; !ok {
			return nil, fmt.Errorf("不支持的通知事件 %q", event)
		}
		unique[event] = struct{}{}
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("至少选择一个审批通知事件")
	}
	result := make([]string, 0, len(unique))
	for event := range unique {
		result = append(result, event)
	}
	sort.Strings(result)
	return result, nil
}

func (h *Handler) notifyApprovalAsync(parent context.Context, request domain.AccessRequest, event, approverName string) {
	if (h.dingTalk == nil && h.dingTalkCard == nil) || h.notificationChannels == nil {
		return
	}
	tenantID := domain.TenantIDFromContext(parent)
	if tenantID == "" {
		log.Printf("skip DingTalk approval notification without tenant context: event=%s request=%s", event, request.ID)
		return
	}
	go func(tenantID string) {
		background := domain.WithTenantID(context.Background(), tenantID)
		ctx, cancel := context.WithTimeout(background, 25*time.Second)
		defer cancel()
		if err := h.deliverApprovalNotification(ctx, request, event, approverName); err != nil {
			log.Printf("DingTalk approval notification failed: event=%s request=%s error=%s", event, request.ID, safeNotificationError(err))
		}
	}(tenantID)
}

func (h *Handler) deliverApprovalNotification(ctx context.Context, request domain.AccessRequest, event, approverName string) error {
	channel, err := h.notificationChannels.Get(ctx, dingTalkChannelType)
	if err != nil {
		return err
	}
	if channel == nil {
		channel = defaultDingTalkChannel()
	}
	notification := h.approvalNotificationForRequest(ctx, request, notificationStatus(event), event, approverName)
	eventEnabled := channel.Enabled && stringSliceContains(channel.Events, event)
	// Once an interactive card exists, lifecycle changes must always reconcile
	// its state. Notification preferences control new deliveries and webhook
	// messages, not whether an old approval card is allowed to stay actionable.
	cardEnabled := h.dingTalkCard != nil && (eventEnabled || event != "access_request.created")
	if !eventEnabled && !cardEnabled {
		return nil
	}
	var deliveryErrors []error
	if cardEnabled {
		var cardErr error
		if event == "access_request.created" {
			cardErr = h.dingTalkCard.send(ctx, notification)
		} else {
			cardErr = h.dingTalkCard.update(ctx, notification)
		}
		if cardErr != nil {
			deliveryErrors = append(deliveryErrors, cardErr)
		}
	}
	if eventEnabled && h.dingTalk != nil {
		if err := h.dingTalk.send(ctx, notification); err != nil {
			deliveryErrors = append(deliveryErrors, err)
		}
	}
	if len(deliveryErrors) > 0 {
		err := errors.Join(deliveryErrors...)
		h.writeNotificationAudit(ctx, "notification.failed", request.ID, "钉钉审批通知发送失败", map[string]any{
			"channel_id": channel.ID, "event": event, "error": safeNotificationError(err),
		}, "", "system")
		return err
	}
	h.writeNotificationAudit(ctx, "notification.sent", request.ID, "钉钉审批通知发送成功", map[string]any{
		"channel_id": channel.ID, "event": event, "interactive_card": cardEnabled,
	}, "", "system")
	return nil
}

func (h *Handler) approvalNotificationForRequest(ctx context.Context, request domain.AccessRequest, status, event, approverName string) approvalNotification {
	notification := approvalNotification{
		Event: event, RequestID: request.ID, ExecutionID: request.ExecutionID,
		Status: status, Reason: request.Reason,
		ExpiresAt: request.ExpiresAt, ApproverName: approverName,
	}
	if principal, err := h.principals.GetByID(ctx, request.PrincipalID); err == nil && principal != nil {
		notification.PrincipalName = principal.Name
		notification.PrincipalKind = string(principal.Kind)
	}
	if resource, err := h.resources.GetByID(ctx, request.ResourceID); err == nil && resource != nil {
		notification.ResourceName = resource.Name
		notification.ResourceType = resource.ResourceType
	}
	if actionName := actionNameFromID(ctx, h.pool, request.ActionID); actionName != "" {
		notification.ActionName = actionName
		if action, err := h.actions.GetByName(ctx, actionName); err == nil && action != nil {
			notification.RiskLevel = string(action.RiskLevel)
		}
	}
	return notification
}

func notificationStatus(event string) string {
	switch event {
	case "access_request.created":
		return "PENDING"
	case "access_request.approved":
		return "APPROVED"
	case "access_request.rejected":
		return "REJECTED"
	case "access_request.expired":
		return "EXPIRED"
	case "access_request.cancelled":
		return "CANCELLED"
	default:
		return strings.ToUpper(strings.TrimPrefix(event, "access_request."))
	}
}

func (h *Handler) writeNotificationAudit(ctx context.Context, action, targetID, summary string, metadata map[string]any, actorID, actorType string) {
	if h.audit == nil {
		return
	}
	event := &domain.AuditEvent{Action: action, ActorID: actorID, ActorType: actorType, TargetID: targetID, Summary: summary, Metadata: metadata}
	if identity := identityFromContext(ctx); identity != nil {
		event.CredentialID = identity.CredentialID
		if event.ActorID == "" && identity.Principal != nil {
			event.ActorID, event.ActorType = identity.Principal.ID, string(identity.Principal.Kind)
		}
		if event.Metadata == nil {
			event.Metadata = map[string]any{}
		}
		for key, value := range identity.AuditMetadata {
			event.Metadata[key] = value
		}
	}
	if err := h.audit.Create(ctx, event); err != nil {
		log.Printf("write notification audit failed: action=%s error=%v", action, err)
	}
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func safeNotificationError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}
