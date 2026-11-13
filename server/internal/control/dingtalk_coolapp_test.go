package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDingTalkCoolAppResolvesUserWithV2PasswordlessAPI(t *testing.T) {
	var tokenCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app-token":
			tokenCalls++
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["appKey"] != "app-key" || body["appSecret"] != "app-secret-value" {
				t.Error("application credentials missing")
			}
			_, _ = w.Write([]byte(`{"accessToken":"access-token","expireIn":7200}`))
		case "/user-token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["clientId"] != "app-key" || body["clientSecret"] != "app-secret-value" || body["code"] != "one-time-auth-code" || body["grantType"] != "authorization_code" {
				t.Error("V2 user token request is incomplete")
			}
			_, _ = w.Write([]byte(`{"accessToken":"user-access-token","corpId":"corp-id"}`))
		case "/me":
			if r.Header.Get("x-acs-dingtalk-access-token") != "user-access-token" {
				t.Error("user access token missing")
			}
			_, _ = w.Write([]byte(`{"unionId":"union-id"}`))
		case "/union-user":
			if r.URL.Query().Get("access_token") != "access-token" {
				t.Error("access token missing")
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["unionid"] != "union-id" {
				t.Error("union ID missing")
			}
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","result":{"userid":"ding-user-1"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &dingTalkCoolAppClient{appKey: "app-key", appSecret: "app-secret-value", http: server.Client(), tokenEndpoint: server.URL + "/app-token", userTokenEndpoint: server.URL + "/user-token", currentUserEndpoint: server.URL + "/me", unionUserEndpoint: server.URL + "/union-user"}
	for i := 0; i < 2; i++ {
		userID, err := client.resolveUserID(context.Background(), "one-time-auth-code", "corp-id")
		if err != nil {
			t.Fatal(err)
		}
		if userID != "ding-user-1" {
			t.Fatalf("userID=%q", userID)
		}
	}
	if tokenCalls != 1 {
		t.Fatalf("access token was not cached: calls=%d", tokenCalls)
	}
}

func TestDingTalkCoolAppNetworkErrorsDoNotLeakCredentials(t *testing.T) {
	client := &dingTalkCoolAppClient{appKey: "key", appSecret: "must-not-leak-secret", http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed for " + request.URL.String())
	})}, userTokenEndpoint: "https://api.dingtalk.invalid/user-token"}
	_, err := client.resolveUserID(context.Background(), "code", "corp-id")
	if err == nil {
		t.Fatal("expected network error")
	}
	if strings.Contains(err.Error(), "must-not-leak-secret") {
		t.Fatal("AppSecret leaked through error")
	}
}

func TestDingTalkCoolAppSessionHasSeparateAudience(t *testing.T) {
	h := &Handler{jwtSigningKey: []byte("0123456789abcdef0123456789abcdef")}
	raw, _, err := h.issueDingTalkSession("ding-user", "张三", "corp-id", "cid-group")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := h.parseDingTalkSession(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "ding-user" || claims.OpenConversationID != "cid-group" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err := h.parseJWT(raw); err == nil {
		t.Fatal("DingTalk session was accepted as an admin Web session")
	}
}

func TestDingTalkCoolAppRejectsCrossSiteMutation(t *testing.T) {
	t.Setenv("PAM_CORS_ORIGIN", "https://pam.example.test")
	request := httptest.NewRequest(http.MethodPost, "https://pam.example.test/api/v1/dingtalk/auth", nil)
	request.Header.Set("Origin", "https://evil.example.test")
	if dingTalkRequestOriginValid(request) {
		t.Fatal("cross-site origin was accepted")
	}
	request.Header.Set("Origin", "https://pam.example.test")
	if !dingTalkRequestOriginValid(request) {
		t.Fatal("configured same origin was rejected")
	}
}
