package control

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gitee.com/pam-platform/warden/server/internal/config"
)

const dingTalkChannelType = "dingtalk"

var allowedDingTalkEvents = []string{
	"access_request.created",
	"access_request.approved",
	"access_request.rejected",
	"access_request.expired",
	"access_request.cancelled",
}

type dingTalkClient struct {
	webhook   *url.URL
	secret    string
	publicURL string
	http      *http.Client
	now       func() time.Time
}

type approvalNotification struct {
	Event         string
	RequestID     string
	ExecutionID   string
	Status        string
	PrincipalName string
	PrincipalKind string
	ResourceName  string
	ResourceType  string
	ActionName    string
	RiskLevel     string
	Reason        string
	ExpiresAt     *time.Time
	ApproverName  string
}

func loadDingTalkClient() (*dingTalkClient, error) {
	webhook, err := config.Secret("WARDEN_DINGTALK_WEBHOOK")
	if err != nil {
		return nil, err
	}
	secret, err := config.Secret("WARDEN_DINGTALK_SECRET")
	if err != nil {
		return nil, err
	}
	if webhook == "" && secret == "" {
		return nil, nil
	}
	if webhook == "" || secret == "" {
		return nil, errors.New("WARDEN_DINGTALK_WEBHOOK and WARDEN_DINGTALK_SECRET must be configured together")
	}
	if len(secret) < 16 {
		return nil, errors.New("WARDEN_DINGTALK_SECRET must contain at least 16 bytes")
	}
	parsed, err := validateDingTalkWebhook(webhook)
	if err != nil {
		return nil, err
	}
	publicURL, err := validatePublicApprovalURL(os.Getenv("WARDEN_PUBLIC_CONTROL_URL"))
	if err != nil {
		return nil, err
	}
	return &dingTalkClient{
		webhook: parsed, secret: secret,
		publicURL: publicURL,
		http:      &http.Client{Timeout: 6 * time.Second},
		now:       time.Now,
	}, nil
}

func validateDingTalkWebhook(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("WARDEN_DINGTALK_WEBHOOK must be a valid HTTPS DingTalk robot URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "oapi.dingtalk.com" && host != "api.dingtalk.com" {
		return nil, errors.New("WARDEN_DINGTALK_WEBHOOK host must be oapi.dingtalk.com or api.dingtalk.com")
	}
	if parsed.Query().Get("access_token") == "" {
		return nil, errors.New("WARDEN_DINGTALK_WEBHOOK must include access_token")
	}
	if parsed.EscapedPath() != "/robot/send" {
		return nil, errors.New("WARDEN_DINGTALK_WEBHOOK path must be /robot/send")
	}
	return parsed, nil
}

func validatePublicApprovalURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("WARDEN_PUBLIC_CONTROL_URL must be an absolute HTTP(S) URL for DingTalk approval links")
	}
	return raw, nil
}

func (c *dingTalkClient) send(ctx context.Context, notification approvalNotification) error {
	title, markdown := c.render(notification)
	payload, err := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"title": title, "text": markdown},
	})
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			}
		}
		if err := c.post(ctx, payload); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("DingTalk delivery failed after 3 attempts: %w", lastErr)
}

func (c *dingTalkClient) post(ctx context.Context, payload []byte) error {
	timestamp := c.now().UnixMilli()
	signaturePayload := strconv.FormatInt(timestamp, 10) + "\n" + c.secret
	mac := hmac.New(sha256.New, []byte(c.secret))
	_, _ = mac.Write([]byte(signaturePayload))
	signedURL := *c.webhook
	query := signedURL.Query()
	query.Set("timestamp", strconv.FormatInt(timestamp, 10))
	query.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	signedURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, signedURL.String(), bytes.NewReader(payload))
	if err != nil {
		return errors.New("create DingTalk request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error includes the complete request URL, including access_token and
		// sign. Never propagate it into logs, audit metadata, or API responses.
		return errors.New("DingTalk network request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("DingTalk HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DingTalk HTTP %d", resp.StatusCode)
	}
	var result struct {
		Code    int    `json:"errcode"`
		Message string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return errors.New("DingTalk returned an invalid response")
	}
	if result.Code != 0 {
		return fmt.Errorf("DingTalk rejected message: code %d (%s)", result.Code, result.Message)
	}
	return nil
}

func (c *dingTalkClient) render(n approvalNotification) (string, string) {
	statusLabel := map[string]string{
		"PENDING": "待审批", "APPROVED": "已批准", "REJECTED": "已拒绝", "EXPIRED": "已过期", "TEST": "渠道测试",
	}[n.Status]
	if statusLabel == "" {
		statusLabel = n.Status
	}
	title := "Warden 审批通知 · " + statusLabel
	lines := []string{"### " + title}
	appendField := func(label, value string) {
		if value != "" {
			lines = append(lines, "- **"+label+"：** "+sanitizeDingTalkText(value))
		}
	}
	appendField("审批单", n.RequestID)
	appendField("申请人", approvalApplicant(n))
	if n.ResourceName != "" {
		appendField("目标资源", n.ResourceType+" / "+n.ResourceName)
	}
	appendField("动作", n.ActionName)
	appendField("风险等级", n.RiskLevel)
	appendField("申请原因", n.Reason)
	if n.ExpiresAt != nil {
		appendField("有效期至", n.ExpiresAt.Local().Format("2006-01-02 15:04:05 MST"))
	}
	appendField("处理人", n.ApproverName)
	if c.publicURL != "" && n.RequestID != "" {
		lines = append(lines, "", "[打开 Warden 审批详情]("+c.publicURL+"/approvals?request="+url.QueryEscape(n.RequestID)+")")
	}
	return title, strings.Join(lines, "  \n")
}

func sanitizeDingTalkText(value string) string {
	value = strings.NewReplacer(
		"\r", " ", "\n", " ", "`", "'", "[", "（", "]", "）", "(", "（", ")", "）",
		"*", "＊", "_", "＿", "#", "＃", ">", "＞",
	).Replace(value)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 300 {
		value = string(runes[:300]) + "…"
	}
	return value
}
