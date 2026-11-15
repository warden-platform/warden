package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
	dingcard "github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
)

func TestDingTalkCardCreateAndDeliverPayload(t *testing.T) {
	var delivered map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"accessToken":"app-token","expireIn":7200}`))
		case "/card":
			if r.Header.Get("x-acs-dingtalk-access-token") != "app-token" {
				t.Error("application access token missing")
			}
			_ = json.NewDecoder(r.Body).Decode(&delivered)
			_, _ = w.Write([]byte(`{"success":true,"result":{"deliverResults":[{"success":true}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app := &dingTalkCoolAppClient{appKey: "ding-app-key", appSecret: "secret-value-long-enough", http: server.Client(), tokenEndpoint: server.URL + "/token"}
	client := &dingTalkCardClient{app: app, templateID: "template-id", openConversationID: "cid-group", robotCode: "robot-code", http: server.Client(), createEndpoint: server.URL + "/card"}
	if err := client.send(context.Background(), approvalNotification{RequestID: "request-id", Status: "PENDING", PrincipalName: "alice"}); err != nil {
		t.Fatal(err)
	}
	if delivered["callbackType"] != "STREAM" || delivered["cardTemplateId"] != "template-id" || delivered["openSpaceId"] != "dtv1.card//im_group.cid-group" || delivered["outTrackId"] != "warden-approval:request-id" {
		t.Fatalf("unexpected card payload: %#v", delivered)
	}
	robot, _ := delivered["imGroupOpenDeliverModel"].(map[string]any)
	if robot["robotCode"] != "robot-code" {
		t.Fatalf("unexpected robot delivery model: %#v", robot)
	}
	space, _ := delivered["imGroupOpenSpaceModel"].(map[string]any)
	if space["supportForward"] != false {
		t.Fatalf("unexpected group space model: %#v", space)
	}
}

func TestDingTalkCardApplicantDistinguishesAgentFromAdmin(t *testing.T) {
	client := &dingTalkCardClient{}
	agentData := client.cardData(approvalNotification{Status: "PENDING", PrincipalName: "AIOps Agent", PrincipalKind: string(domain.PrincipalKindAgentInstance)})
	if agentData["applicant"] != "Agent · AIOps Agent" {
		t.Fatalf("unexpected agent applicant: %q", agentData["applicant"])
	}
	adminData := client.cardData(approvalNotification{Status: "PENDING", PrincipalName: "admin", PrincipalKind: string(domain.PrincipalKindUser)})
	if adminData["applicant"] != "admin" {
		t.Fatalf("unexpected admin applicant: %q", adminData["applicant"])
	}
	cancelled := client.cardData(approvalNotification{Status: "CANCELLED", PrincipalName: "admin"})
	if cancelled["statusText"] != "已取消" || cancelled["approvalStatus"] != "CANCELLED" {
		t.Fatalf("unexpected cancelled card state: %#v", cancelled)
	}
}

func TestDingTalkCardRejectsEmptyDeliveryResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"deliverResults":[{"success":false,"errorMsg":"spaces of card is empty"}]}}`))
	}))
	defer server.Close()
	client := &dingTalkCardClient{app: &dingTalkCoolAppClient{accessToken: "token", tokenExpiry: time.Now().Add(time.Hour)}, http: server.Client()}
	if err := client.do(context.Background(), http.MethodPost, server.URL, map[string]string{"test": "value"}); err == nil {
		t.Fatal("empty delivery result was accepted")
	}
}

func TestDingTalkCardDecisionUsesTrackAndActionID(t *testing.T) {
	request := &dingcard.CardRequest{OutTrackId: "warden-approval:req-1", CardActionData: dingcard.PrivateCardActionData{CardPrivateData: dingcard.CardPrivateData{ActionIdList: []string{"approve"}}}}
	id, approve, err := dingTalkCardDecision(request)
	if err != nil || id != "req-1" || !approve {
		t.Fatalf("id=%q approve=%v err=%v", id, approve, err)
	}
	request.CardActionData.CardPrivateData.ActionIdList = []string{"reject"}
	_, approve, err = dingTalkCardDecision(request)
	if err != nil || approve {
		t.Fatalf("approve=%v err=%v", approve, err)
	}
}

func TestDingTalkCardDecisionAcceptsAgreeAction(t *testing.T) {
	request := &dingcard.CardRequest{OutTrackId: "warden-approval:req-1", CardActionData: dingcard.PrivateCardActionData{CardPrivateData: dingcard.CardPrivateData{ActionIdList: []string{"agree"}}}}
	_, approve, err := dingTalkCardDecision(request)
	if err != nil || !approve {
		t.Fatalf("approve=%v err=%v", approve, err)
	}
}

func TestDingTalkCardDecisionRejectsUnknownAction(t *testing.T) {
	request := &dingcard.CardRequest{OutTrackId: "warden-approval:req-1", CardActionData: dingcard.PrivateCardActionData{CardPrivateData: dingcard.CardPrivateData{ActionIdList: []string{"open-link"}}}}
	if _, _, err := dingTalkCardDecision(request); err == nil {
		t.Fatal("unknown action was accepted")
	}
}

func TestDingTalkCardChannelTestCannotResolveToRealRequest(t *testing.T) {
	request := &dingcard.CardRequest{OutTrackId: "warden-approval:channel-test-123", CardActionData: dingcard.PrivateCardActionData{CardPrivateData: dingcard.CardPrivateData{ActionIdList: []string{"approve"}}}}
	id, approve, err := dingTalkCardDecision(request)
	if err != nil || id != "channel-test-123" || !approve {
		t.Fatalf("id=%q approve=%v err=%v", id, approve, err)
	}
}

func TestDingTalkCardCallbackOnlyAcceptsConfiguredGroup(t *testing.T) {
	request := &dingcard.CardRequest{SpaceId: "cid-allowed"}
	if !dingTalkCardCallbackSpaceAllowed(request, "cid-allowed") {
		t.Fatal("configured group was rejected")
	}
	if dingTalkCardCallbackSpaceAllowed(request, "cid-other") || dingTalkCardCallbackSpaceAllowed(request, "") {
		t.Fatal("unconfigured group was accepted")
	}
}

func TestDingTalkUserNameUsesAppTokenAndUserID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/get" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("access_token") != "app-token" || r.Form.Get("userid") != "ding-user-1" || r.Form.Get("language") != "zh_CN" {
			t.Errorf("unexpected user detail form: %#v", r.Form)
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","result":{"userid":"ding-user-1","name":"张三","active":true}}`))
	}))
	defer server.Close()
	client := &dingTalkCoolAppClient{
		http: server.Client(), accessToken: "app-token", tokenExpiry: time.Now().Add(time.Hour),
		userDetailEndpoint: server.URL + "/user/get",
	}
	name, err := client.userName(context.Background(), "ding-user-1")
	if err != nil || name != "张三" {
		t.Fatalf("name=%q err=%v", name, err)
	}
}

func TestDingTalkApprovalAuditKeepsExternalUser(t *testing.T) {
	identity := &AuthenticatedIdentity{
		Principal:     &domain.Principal{ID: adminPrincipalID, Kind: domain.PrincipalKindUser},
		AuditMetadata: map[string]any{"dingtalk_user_id": "ding-user-1", "dingtalk_user_name": "张三"},
	}
	event := executionAuditEvent("access_request.approved", identity, nil, nil, "execution-id", "trace-id")
	if event.ActorID != adminPrincipalID || event.Metadata["dingtalk_user_id"] != "ding-user-1" || event.Metadata["dingtalk_user_name"] != "张三" {
		t.Fatalf("unexpected audit event: %#v", event)
	}
}
