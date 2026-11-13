package domain

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConnectorRequestSignature(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"connectorId":"connector-1"}`)
	now := time.Unix(1_750_000_000, 0)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resources/sync?full=true", nil)
	if err := SignConnectorRequest(req, body, key, now, "unique-nonce"); err != nil {
		t.Fatal(err)
	}
	if nonce, err := VerifyConnectorRequest(req, body, key, now.Add(time.Minute), 2*time.Minute); err != nil || nonce != "unique-nonce" {
		t.Fatalf("valid signature rejected: nonce=%q err=%v", nonce, err)
	}

	if _, err := VerifyConnectorRequest(req, []byte(`{"connectorId":"other"}`), key, now, 2*time.Minute); err == nil {
		t.Fatal("body tampering was accepted")
	}
	req.Method = http.MethodPut
	if _, err := VerifyConnectorRequest(req, body, key, now, 2*time.Minute); err == nil {
		t.Fatal("method tampering was accepted")
	}
}

func TestConnectorRequestSignatureRejectsExpiredTimestamp(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_750_000_000, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/connectors/id/tasks", nil)
	if err := SignConnectorRequest(req, nil, key, now, "unique-nonce"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyConnectorRequest(req, nil, key, now.Add(3*time.Minute), 2*time.Minute); err == nil {
		t.Fatal("expired connector request was accepted")
	}
}
