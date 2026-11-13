package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	ConnectorTimestampHeader = "X-PAM-Timestamp"
	ConnectorNonceHeader     = "X-PAM-Nonce"
	ConnectorSignatureHeader = "X-PAM-Signature"
)

const connectorRequestVersion = "pam-connector-request-v1"

// SignConnectorRequest authenticates the complete request without sending the
// Connector task key over the wire. Callers must pass the exact body bytes that
// will be sent by req.
func SignConnectorRequest(req *http.Request, body, key []byte, now time.Time, nonce string) error {
	if len(key) < 32 {
		return fmt.Errorf("connector signing key must be at least 32 bytes")
	}
	if strings.TrimSpace(nonce) == "" {
		return fmt.Errorf("connector request nonce is required")
	}
	timestamp := strconv.FormatInt(now.UTC().Unix(), 10)
	signature := connectorRequestMAC(req.Method, connectorRequestTarget(req), timestamp, nonce, body, key)
	req.Header.Set(ConnectorTimestampHeader, timestamp)
	req.Header.Set(ConnectorNonceHeader, nonce)
	req.Header.Set(ConnectorSignatureHeader, "hmac-sha256:"+hex.EncodeToString(signature))
	return nil
}

// VerifyConnectorRequest verifies request freshness and integrity and returns
// the nonce so the control plane can reject replays.
func VerifyConnectorRequest(req *http.Request, body, key []byte, now time.Time, maxSkew time.Duration) (string, error) {
	if len(key) < 32 {
		return "", fmt.Errorf("connector signing key must be at least 32 bytes")
	}
	timestamp := req.Header.Get(ConnectorTimestampHeader)
	nonce := req.Header.Get(ConnectorNonceHeader)
	provided := req.Header.Get(ConnectorSignatureHeader)
	if timestamp == "" || nonce == "" || provided == "" {
		return "", fmt.Errorf("connector request signature headers are required")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid connector request timestamp")
	}
	requestTime := time.Unix(seconds, 0)
	if requestTime.Before(now.Add(-maxSkew)) || requestTime.After(now.Add(maxSkew)) {
		return "", fmt.Errorf("connector request timestamp is outside the allowed window")
	}
	const prefix = "hmac-sha256:"
	if !strings.HasPrefix(provided, prefix) {
		return "", fmt.Errorf("invalid connector request signature format")
	}
	providedMAC, err := hex.DecodeString(strings.TrimPrefix(provided, prefix))
	if err != nil {
		return "", fmt.Errorf("invalid connector request signature encoding")
	}
	expected := connectorRequestMAC(req.Method, connectorRequestTarget(req), timestamp, nonce, body, key)
	if !hmac.Equal(providedMAC, expected) {
		return "", fmt.Errorf("invalid connector request signature")
	}
	return nonce, nil
}

func connectorRequestTarget(req *http.Request) string {
	target := req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	return target
}

func connectorRequestMAC(method, path, timestamp, nonce string, body, key []byte) []byte {
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		connectorRequestVersion,
		strings.ToUpper(method),
		path,
		timestamp,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}
