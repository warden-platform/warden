package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"gitee.com/warden-platform/warden/server/internal/config"
	"gitee.com/warden-platform/warden/server/internal/domain"
	dingcard "github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
	streamclient "github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
)

const dingTalkCardTrackPrefix = "warden-approval:"

type dingTalkCardClient struct {
	app                *dingTalkCoolAppClient
	templateID         string
	openConversationID string
	robotCode          string
	http               *http.Client
	createEndpoint     string
	updateEndpoint     string
}

func loadDingTalkCardClient(app *dingTalkCoolAppClient) (*dingTalkCardClient, error) {
	templateID, err := config.Secret("WARDEN_DINGTALK_CARD_TEMPLATE_ID")
	if err != nil {
		return nil, err
	}
	conversationID, err := config.Secret("WARDEN_DINGTALK_OPEN_CONVERSATION_ID")
	if err != nil {
		return nil, err
	}
	robotCode, err := config.Secret("WARDEN_DINGTALK_ROBOT_CODE")
	if err != nil {
		return nil, err
	}
	if templateID == "" && conversationID == "" && robotCode == "" {
		return nil, nil
	}
	if app == nil {
		return nil, errors.New("WARDEN_DINGTALK_APP_KEY and WARDEN_DINGTALK_APP_SECRET are required for interactive cards")
	}
	if templateID == "" || conversationID == "" || robotCode == "" {
		return nil, errors.New("WARDEN_DINGTALK_CARD_TEMPLATE_ID, WARDEN_DINGTALK_OPEN_CONVERSATION_ID and WARDEN_DINGTALK_ROBOT_CODE must be configured together")
	}
	if len(templateID) > 256 || len(conversationID) > 512 || len(robotCode) > 256 || strings.ContainsAny(templateID, "\r\n") || strings.ContainsAny(conversationID, "\r\n") || strings.ContainsAny(robotCode, "\r\n") {
		return nil, errors.New("invalid DingTalk interactive card configuration")
	}
	return &dingTalkCardClient{
		app: app, templateID: templateID, openConversationID: conversationID, robotCode: robotCode,
		http:           &http.Client{Timeout: 8 * time.Second},
		createEndpoint: "https://api.dingtalk.com/v1.0/card/instances/createAndDeliver",
		updateEndpoint: "https://api.dingtalk.com/v1.0/card/instances",
	}, nil
}

func dingTalkCardTrackID(requestID string) string { return dingTalkCardTrackPrefix + requestID }

func (c *dingTalkCardClient) cardData(n approvalNotification) map[string]string {
	expiresAt := ""
	if n.ExpiresAt != nil {
		expiresAt = n.ExpiresAt.Local().Format("2006-01-02 15:04:05 MST")
	}
	statusText := map[string]string{"PENDING": "待审批", "APPROVED": "已通过", "REJECTED": "已拒绝", "EXPIRED": "已过期", "CANCELLED": "已取消", "TEST": "通道测试"}[n.Status]
	resource := sanitizeDingTalkText(strings.Trim(strings.Join([]string{n.ResourceType, n.ResourceName}, " / "), " /"))
	action := sanitizeDingTalkText(n.ActionName)
	title := "Warden 特权访问申请"
	if action != "" && resource != "" {
		title = action + " · " + resource
	} else if action != "" {
		title = action
	} else if resource != "" {
		title = resource
	}
	return map[string]string{
		"title": title, "status": n.Status, "requestId": n.RequestID,
		"statusText": statusText, "approvalStatus": n.Status,
		"applicant": sanitizeDingTalkText(approvalApplicant(n)), "resource": resource,
		"action": action, "riskLevel": sanitizeDingTalkText(n.RiskLevel),
		"reason": sanitizeDingTalkText(n.Reason), "expiresAt": expiresAt, "approver": sanitizeDingTalkText(n.ApproverName),
		// Compatibility aliases used by DingTalk's exported approval example.
		"type": action, "amount": resource, "createTime": time.Now().Local().Format("2006-01-02 15:04:05"), "lastMessage": "Warden 审批 · " + statusText,
	}
}

func approvalApplicant(n approvalNotification) string {
	name := sanitizeDingTalkText(n.PrincipalName)
	if name == "" {
		name = "未知申请人"
	}
	if n.PrincipalKind == string(domain.PrincipalKindAgentInstance) {
		return "Agent · " + name
	}
	return name
}

func (c *dingTalkCardClient) send(ctx context.Context, n approvalNotification) error {
	payload := map[string]any{
		"callbackType": "STREAM", "cardTemplateId": c.templateID,
		"cardData":    map[string]any{"cardParamMap": c.cardData(n)},
		"openSpaceId": "dtv1.card//im_group." + c.openConversationID,
		"outTrackId":  dingTalkCardTrackID(n.RequestID),
		"imGroupOpenSpaceModel": map[string]any{
			"supportForward": false,
			"notification":   map[string]any{"alertContent": "Warden 审批通知", "notificationOff": false},
		},
		"imGroupOpenDeliverModel": map[string]any{"robotCode": c.robotCode},
	}
	return c.do(ctx, http.MethodPost, c.createEndpoint, payload)
}

func (c *dingTalkCardClient) update(ctx context.Context, n approvalNotification) error {
	payload := map[string]any{
		"outTrackId":        dingTalkCardTrackID(n.RequestID),
		"cardData":          map[string]any{"cardParamMap": c.cardData(n)},
		"cardUpdateOptions": map[string]bool{"updateCardDataByKey": true},
	}
	return c.do(ctx, http.MethodPut, c.updateEndpoint, payload)
}

func (c *dingTalkCardClient) do(ctx context.Context, method, endpoint string, payload any) error {
	token, err := c.app.token(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create DingTalk card request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("DingTalk card network request failed")
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DingTalk card HTTP %d", resp.StatusCode)
	}
	var result struct {
		Success *bool  `json:"success"`
		Code    string `json:"code"`
		Result  struct {
			DeliverResults []struct {
				Success bool `json:"success"`
			} `json:"deliverResults"`
		} `json:"result"`
	}
	if len(responseBody) > 0 && json.Unmarshal(responseBody, &result) == nil {
		if result.Success != nil && !*result.Success {
			return fmt.Errorf("DingTalk card rejected request: code %s", result.Code)
		}
		if len(result.Result.DeliverResults) > 0 {
			delivered := false
			for _, delivery := range result.Result.DeliverResults {
				delivered = delivered || delivery.Success
			}
			if !delivered {
				return errors.New("DingTalk card was created but not delivered")
			}
		}
	}
	return nil
}

func (h *Handler) startDingTalkCardStream(ctx context.Context) {
	if h.dingTalkCard == nil {
		return
	}
	client := streamclient.NewStreamClient(streamclient.WithAppCredential(streamclient.NewAppCredentialConfig(h.dingTalkCard.app.appKey, h.dingTalkCard.app.appSecret)))
	client.RegisterCardCallbackRouter(h.handleDingTalkCardCallback)
	go func() {
		if err := client.Start(ctx); err != nil && ctx.Err() == nil {
			log.Printf("DingTalk card Stream start failed: %v", err)
			return
		}
		log.Printf("DingTalk card Stream connected")
		<-ctx.Done()
		client.Close()
	}()
}

func (h *Handler) handleDingTalkCardCallback(ctx context.Context, request *dingcard.CardRequest) (*dingcard.CardResponse, error) {
	if request == nil || h.principals == nil || h.dingTalkCard == nil || h.dingTalkCard.app == nil {
		return nil, errors.New("DingTalk card callback unavailable")
	}
	if !dingTalkCardCallbackSpaceAllowed(request, h.dingTalkCard.openConversationID) {
		return nil, errors.New("DingTalk card callback space is not allowed")
	}
	log.Printf("DingTalk card callback received: out_track_id=%s user_id=%s user_id_type=%d action_ids=%v", request.OutTrackId, request.UserId, request.UserIdType, request.CardActionData.CardPrivateData.ActionIdList)
	requestID, approve, err := dingTalkCardDecision(request)
	if err != nil {
		return nil, err
	}
	userID, approverName, err := h.resolveDingTalkCallbackUser(ctx, request.UserId)
	if err != nil {
		return nil, err
	}
	// Channel test cards deliberately have no database approval request. They
	// only verify card delivery and the authenticated Stream callback, and must
	// never create a Grant or trigger an execution.
	if strings.HasPrefix(requestID, "channel-test-") {
		status := "REJECTED"
		if approve {
			status = "APPROVED"
		}
		notification := approvalNotification{RequestID: requestID, Status: status, ApproverName: approverName}
		if h.dingTalkCard != nil {
			if err := h.dingTalkCard.update(ctx, notification); err != nil {
				log.Printf("DingTalk test card update failed: %v", err)
			}
		}
		return &dingcard.CardResponse{
			CardUpdateOptions: &dingcard.CardUpdateOptions{UpdateCardDataByKey: true},
			CardData:          &dingcard.CardDataDto{CardParamMap: h.dingTalkCard.cardData(notification)},
		}, nil
	}
	principal, err := h.principals.GetByID(ctx, adminPrincipalID)
	if err != nil || principal == nil || principal.Status != domain.PrincipalStatusActive || principal.Kind != domain.PrincipalKindUser || principal.ID != adminPrincipalID {
		return nil, errors.New("DingTalk card principal unavailable")
	}
	actor := &AuthenticatedIdentity{Principal: principal, Scopes: []string{"pam:approve"}, Kind: "DINGTALK_CARD_STREAM", AuditMetadata: map[string]any{
		"auth_channel": "dingtalk_card_stream", "dingtalk_user_id": userID, "dingtalk_user_name": approverName,
	}}
	approvedRequest, err := h.decideApproval(ctx, requestID, approve, actor)
	if err != nil {
		_, code, _ := approvalDecisionHTTPError(err)
		log.Printf("DingTalk card decision failed: request_id=%s code=%s", requestID, code)
		if response := h.reconcileDingTalkCardResponse(ctx, requestID, approverName); response != nil {
			return response, nil
		}
		return nil, err
	}
	status := "REJECTED"
	if approve {
		status = "APPROVED"
	}
	h.notifyApprovalAsync(ctx, *approvedRequest, "access_request."+strings.ToLower(status), approverName)
	notification := h.approvalNotificationForRequest(ctx, *approvedRequest, status, "access_request."+strings.ToLower(status), approverName)
	return &dingcard.CardResponse{
		CardUpdateOptions: &dingcard.CardUpdateOptions{UpdateCardDataByKey: true},
		CardData:          &dingcard.CardDataDto{CardParamMap: h.dingTalkCard.cardData(notification)},
	}, nil
}

func (h *Handler) reconcileDingTalkCardResponse(ctx context.Context, requestID, approverName string) *dingcard.CardResponse {
	request, err := h.requests.GetByID(ctx, requestID)
	if err != nil || request == nil {
		return nil
	}
	status := string(request.Status)
	if request.Status == domain.RequestPending && request.ExecutionID != "" {
		execution, execErr := h.executions.GetByID(ctx, request.ExecutionID)
		if execErr != nil || execution == nil {
			return nil
		}
		switch execution.Status {
		case domain.ExecCancelled:
			status = string(domain.RequestCancelled)
			_ = h.requests.UpdateStatus(ctx, request.ID, status, "", nil)
		case domain.ExecExpired:
			status = string(domain.RequestExpired)
		default:
			return nil
		}
	}
	event := "access_request." + strings.ToLower(status)
	notification := h.approvalNotificationForRequest(ctx, *request, status, event, approverName)
	return &dingcard.CardResponse{
		CardUpdateOptions: &dingcard.CardUpdateOptions{UpdateCardDataByKey: true},
		CardData:          &dingcard.CardDataDto{CardParamMap: h.dingTalkCard.cardData(notification)},
	}
}

func dingTalkCardCallbackSpaceAllowed(request *dingcard.CardRequest, expected string) bool {
	return request != nil && strings.TrimSpace(expected) != "" && strings.TrimSpace(request.SpaceId) == strings.TrimSpace(expected)
}

func (h *Handler) resolveDingTalkCallbackUser(ctx context.Context, rawUserID string) (string, string, error) {
	userID := strings.TrimSpace(rawUserID)
	if userID == "" || len(userID) > 256 || strings.ContainsAny(userID, "\r\n") {
		return "", "", errors.New("DingTalk card callback user is invalid")
	}
	approverName := userID
	lookupCtx, cancelLookup := context.WithTimeout(ctx, 2*time.Second)
	defer cancelLookup()
	if resolvedName, lookupErr := h.dingTalkCard.app.userName(lookupCtx, userID); lookupErr != nil {
		log.Printf("DingTalk callback user lookup failed: user_id=%s error=%v", userID, lookupErr)
	} else {
		approverName = sanitizeDingTalkText(resolvedName)
	}
	return userID, approverName, nil
}

func dingTalkCardDecision(request *dingcard.CardRequest) (string, bool, error) {
	if request == nil {
		return "", false, errors.New("invalid DingTalk card action")
	}
	requestID := strings.TrimSpace(request.GetActionString("request_id"))
	if requestID == "" {
		requestID = strings.TrimPrefix(strings.TrimSpace(request.OutTrackId), dingTalkCardTrackPrefix)
	}
	action := strings.ToLower(strings.TrimSpace(request.GetActionString("action")))
	if action == "" && len(request.CardActionData.CardPrivateData.ActionIdList) > 0 {
		action = strings.ToLower(strings.TrimSpace(request.CardActionData.CardPrivateData.ActionIdList[0]))
	}
	approve := action == "approve" || action == "agree" || strings.Contains(action, "approve")
	if requestID == "" || (!approve && action != "reject" && !strings.Contains(action, "reject")) {
		return "", false, errors.New("invalid DingTalk card action")
	}
	return requestID, approve, nil
}
