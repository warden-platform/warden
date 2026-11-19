package control

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestValidateDingTalkWebhook(t *testing.T) {
	valid := "https://oapi.dingtalk.com/robot/send?access_token=redacted"
	if _, err := validateDingTalkWebhook(valid); err != nil {
		t.Fatalf("valid webhook rejected: %v", err)
	}
	for _, candidate := range []string{
		"http://oapi.dingtalk.com/robot/send?access_token=x",
		"https://127.0.0.1/robot/send?access_token=x",
		"https://oapi.dingtalk.com/robot/send",
		"https://user@oapi.dingtalk.com/robot/send?access_token=x",
		"https://oapi.dingtalk.com/other/path?access_token=x",
	} {
		if _, err := validateDingTalkWebhook(candidate); err == nil {
			t.Fatalf("unsafe webhook accepted: %s", candidate)
		}
	}
}

func TestValidatePublicApprovalURL(t *testing.T) {
	for _, candidate := range []string{"https://warden.example.test", "http://127.0.0.1:8080"} {
		if _, err := validatePublicApprovalURL(candidate); err != nil {
			t.Fatalf("valid public URL rejected: %v", err)
		}
	}
	for _, candidate := range []string{"javascript:alert(1)", "https://user@warden.example.test", "/relative"} {
		if _, err := validatePublicApprovalURL(candidate); err == nil {
			t.Fatalf("unsafe public URL accepted: %s", candidate)
		}
	}
}

func TestDingTalkSendSignsAndRedactsCredentialsFromBody(t *testing.T) {
	webhook, _ := url.Parse("https://oapi.dingtalk.com/robot/send?access_token=top-secret-token")
	now := time.UnixMilli(1785384000123)
	secret := "SEC-this-is-a-signing-secret"
	var captured *http.Request
	var body []byte
	client := &dingTalkClient{
		webhook: webhook, secret: secret, publicURL: "https://warden.example.test",
		now: func() time.Time { return now },
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			captured = request
			body, _ = io.ReadAll(request.Body)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`))}, nil
		})},
	}
	err := client.send(context.Background(), approvalNotification{
		Status: "PENDING", RequestID: "request-1", PrincipalName: "agent", ResourceName: "api",
		ResourceType: "deployment", ActionName: "deployment.update_image", RiskLevel: "high", Reason: "release",
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := captured.URL.Query().Get("timestamp")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	wantSign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := captured.URL.Query().Get("sign"); got != wantSign {
		t.Fatalf("signature = %q, want %q", got, wantSign)
	}
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "top-secret-token") {
		t.Fatal("DingTalk credential leaked into message body")
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil || payload["msgtype"] != "markdown" {
		t.Fatalf("invalid message payload: %s", body)
	}
}

func TestDingTalkSendRetriesTransientFailure(t *testing.T) {
	webhook, _ := url.Parse("https://oapi.dingtalk.com/robot/send?access_token=x")
	attempts := 0
	client := &dingTalkClient{webhook: webhook, secret: "SEC-this-is-long-enough", now: time.Now,
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`))}, nil
		})}}
	if err := client.send(context.Background(), approvalNotification{Status: "TEST"}); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestDingTalkNetworkErrorDoesNotLeakWebhook(t *testing.T) {
	webhook, _ := url.Parse("https://oapi.dingtalk.com/robot/send?access_token=must-not-leak")
	client := &dingTalkClient{webhook: webhook, secret: "SEC-must-not-leak-either", now: time.Now,
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed for " + request.URL.String())
		})}}
	err := client.send(context.Background(), approvalNotification{Status: "TEST"})
	if err == nil {
		t.Fatal("network failure was ignored")
	}
	if strings.Contains(err.Error(), "must-not-leak") || strings.Contains(err.Error(), "access_token") {
		t.Fatalf("credential leaked through error: %v", err)
	}
}

func TestNotificationEventValidation(t *testing.T) {
	events, err := validateNotificationEvents([]string{"access_request.created", "access_request.created", "access_request.expired"})
	if err != nil || len(events) != 2 {
		t.Fatalf("valid events rejected: %v %#v", err, events)
	}
	if _, err := validateNotificationEvents(nil); err == nil {
		t.Fatal("empty event selection accepted")
	}
	if _, err := validateNotificationEvents([]string{"credential.exposed"}); err == nil {
		t.Fatal("unknown event accepted")
	}
	if got := notificationStatus("access_request.created"); got != "PENDING" {
		t.Fatalf("created status = %q", got)
	}
	if got := notificationStatus("access_request.cancelled"); got != "CANCELLED" {
		t.Fatalf("cancelled status = %q", got)
	}
}
