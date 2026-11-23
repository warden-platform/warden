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

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
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
		Name               string   `json:"name"`
		Enabled            bool     `json:"enabled"`
		Events             []string `json:"events"`
		OpenConversationID string   `json:"open_conversation_id"`
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
	body.OpenConversationID = strings.TrimSpace(body.OpenConversationID)
	if len(body.OpenConversationID) > 512 || strings.ContainsAny(body.OpenConversationID, "\r\n") {
		jsonError(w, http.StatusBadRequest, "INVALID_CHANNEL", "审批群 ID 格式不正确")
		return
	}
	tenantID := domain.TenantIDFromContext(r.Context())
	if body.OpenConversationID != "" && tenantID != "platform" && h.dingTalkCard != nil && body.OpenConversationID == strings.TrimSpace(h.dingTalkCard.openConversationID) {
		jsonError(w, http.StatusConflict, "DINGTALK_GROUP_CONFLICT", "该群是平台管理域的部署级审批群，业务组织请绑定自己的审批群")
		return
	}
	if body.Enabled && h.dingTalk == nil && h.dingTalkCard == nil {
		jsonError(w, http.StatusConflict, "DINGTALK_NOT_CONFIGURED", "请先通过 Secret 文件配置钉钉 Webhook 或互动卡片")
		return
	}
	channel := &domain.NotificationChannel{Type: dingTalkChannelType, Name: body.Name, Enabled: body.Enabled, Events: events, OpenConversationID: body.OpenConversationID}
	if err := h.notificationChannels.Upsert(r.Context(), channel); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "notification_channels_type_conversation_key" {
			jsonError(w, http.StatusConflict, "DINGTALK_GROUP_BOUND", "该审批群已被其他组织绑定，一个群只能归属一个组织")
			return
		}
		jsonError(w, http.StatusInternalServerError, "DB_ERROR", "保存钉钉通知渠道失败")
		return
	}
	identity := identityFromContext(r.Context())
	h.writeNotificationAudit(r.Context(), "notification.updated", channel.ID, "钉钉审批通知配置已更新", map[string]any{
		"enabled": channel.Enabled, "events": channel.Events, "open_conversation_id": channel.OpenConversationID,
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
	identity := identityFromContext(r.Context())
	tenantID := domain.TenantIDFromContext(r.Context())
	conversationID := h.dingTalkConversationForTenant(ctx, tenantID)
	cardAvailable := h.dingTalkCard != nil && conversationID != ""
	webhookAvailable := h.dingTalk != nil && tenantID == "platform"
	if !cardAvailable && !webhookAvailable {
		jsonError(w, http.StatusConflict, "DINGTALK_GROUP_NOT_BOUND", "本组织尚未绑定审批群，请先在渠道设置中填写本组织的审批群 ID")
		return
	}
	notification := approvalNotification{
		Event: "notification.test", Status: "TEST", RequestID: fmt.Sprintf("channel-test-%s-%d", tenantID, time.Now().UnixNano()),
		PrincipalName: identity.Principal.Name,
		Reason:        "Warden 钉钉审批通知渠道连通性测试",
	}
	var err error
	if cardAvailable {
		err = h.dingTalkCard.send(ctx, notification, conversationID)
	} else {
		err = h.dingTalk.send(ctx, notification)
	}
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

// resolveDingTalkTenant maps a DingTalk group to its owning tenant. A group
// bound in notification_channels wins; the deployment-level
// WARDEN_DINGTALK_OPEN_CONVERSATION_ID secret is the platform tenant's
// fallback (backward compatibility with single-tenant deployments).
// Returns "" when the group is bound to no tenant (fail-closed upstream).
func (h *Handler) resolveDingTalkTenant(ctx context.Context, conversationID string) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" || h.notificationChannels == nil {
		return "", nil
	}
	channel, err := h.notificationChannels.GetByConversation(ctx, dingTalkChannelType, conversationID)
	if err != nil {
		return "", err
	}
	if channel != nil {
		return channel.TenantID, nil
	}
	if h.dingTalkCard != nil && conversationID == strings.TrimSpace(h.dingTalkCard.openConversationID) {
		return "platform", nil
	}
	return "", nil
}

// dingTalkConversationForTenant returns the approval group bound to a tenant.
// Business tenants must bind their own group; only the platform tenant may
// fall back to the deployment-level secret. "" means unbound.
func (h *Handler) dingTalkConversationForTenant(ctx context.Context, tenantID string) string {
	if h.notificationChannels != nil {
		if channel, err := h.notificationChannels.Get(domain.WithTenantID(ctx, tenantID), dingTalkChannelType); err == nil && channel != nil && strings.TrimSpace(channel.OpenConversationID) != "" {
			return strings.TrimSpace(channel.OpenConversationID)
		}
	}
	if tenantID == "platform" && h.dingTalkCard != nil {
		return strings.TrimSpace(h.dingTalkCard.openConversationID)
	}
	return ""
}

func notificationChannelResponse(channel *domain.NotificationChannel, webhookConfigured, coolAppConfigured, cardConfigured bool) map[string]any {
	return map[string]any{
		"id": channel.ID, "type": channel.Type, "name": channel.Name,
		"enabled": channel.Enabled, "events": channel.Events, "configured": webhookConfigured || cardConfigured,
		"webhook_configured": webhookConfigured, "coolapp_configured": coolAppConfigured,
		"card_configured": cardConfigured, "stream_configured": cardConfigured,
		"open_conversation_id": channel.OpenConversationID,
		"credential_source":    "secret_file", "created_at": channel.CreatedAt, "updated_at": channel.UpdatedAt,
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
	// 非阻塞限流：信号量满则丢弃本次通知（卡片状态仍由后续生命周期事件的
	// reconcile 兜底），避免审批批次（批量过期/卡片风暴）派生无界 goroutine。
	if h.notifySem != nil {
		select {
		case h.notifySem <- struct{}{}:
		default:
			log.Printf("DingTalk approval notification dropped (concurrency limit): event=%s request=%s", event, request.ID)
			return
		}
	}
	go func(tenantID string) {
		if h.notifySem != nil {
			defer func() { <-h.notifySem }()
		}
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
	tenantID := domain.TenantIDFromContext(ctx)
	conversationID := strings.TrimSpace(channel.OpenConversationID)
	if conversationID == "" && tenantID == "platform" && h.dingTalkCard != nil {
		conversationID = strings.TrimSpace(h.dingTalkCard.openConversationID)
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
		if conversationID == "" {
			// Tenant isolation: never deliver a tenant's approval content into
			// another tenant's (or the deployment default) group.
			log.Printf("skip DingTalk card without a bound tenant group: tenant=%s event=%s request=%s", tenantID, event, request.ID)
		} else {
			var cardErr error
			if event == "access_request.created" {
				cardErr = h.dingTalkCard.send(ctx, notification, conversationID)
			} else {
				cardErr = h.dingTalkCard.update(ctx, notification)
			}
			if cardErr != nil {
				deliveryErrors = append(deliveryErrors, cardErr)
			}
		}
	}
	// The legacy webhook points at one deployment-level group, so it may only
	// carry platform-tenant content; business tenants use their own card group.
	if eventEnabled && h.dingTalk != nil && tenantID == "platform" {
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
