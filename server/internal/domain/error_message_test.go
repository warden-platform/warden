package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeErrorMessageRemovesHTMLAndBoundsLength(t *testing.T) {
	input := `Jenkins returned 400: <!doctype html><html data-crumb-value="secret"><script>alert(1)</script><title>Bad Request</title></html>`
	got := SanitizeErrorMessage(input)
	if strings.Contains(got, "secret") || strings.Contains(got, "<") || !strings.Contains(got, "Jenkins returned 400") {
		t.Fatalf("unsafe sanitized error: %q", got)
	}
	long := SanitizeErrorMessage(strings.Repeat("x", 5000))
	if len([]rune(long)) > maxErrorMessageRunes+1 {
		t.Fatalf("sanitized error is not bounded: %d", len([]rune(long)))
	}
}

func TestSanitizeErrorMessageHandlesEscapedAndTruncatedHTML(t *testing.T) {
	escaped := SanitizeErrorMessage(`Jenkins returned 403: &lt;html&gt;&lt;link rel="stylesheet" href="/x.css"&gt;&lt;script&gt;alert(1)&lt;/script&gt;Denied&lt;/html&gt;`)
	if strings.Contains(escaped, "<") || strings.Contains(escaped, "&lt;") || strings.Contains(escaped, "stylesheet") || strings.Contains(escaped, "alert") {
		t.Fatalf("escaped HTML leaked through: %q", escaped)
	}
	if !strings.Contains(escaped, "Jenkins returned 403") || !strings.Contains(escaped, "Denied") {
		t.Fatalf("useful context lost: %q", escaped)
	}
	truncated := SanitizeErrorMessage(`Jenkins returned 500: <html><head><link rel="stylesheet" href="/static/style.css`)
	if strings.Contains(truncated, "<") || strings.Contains(truncated, "stylesheet") {
		t.Fatalf("unterminated tag leaked through: %q", truncated)
	}
	if !strings.Contains(truncated, "[HTML content removed]") {
		t.Fatalf("missing removal marker: %q", truncated)
	}
	summary := SanitizeErrorMessage("<html>" + strings.Repeat("word ", 200) + "</html>")
	if len([]rune(summary)) > len("[HTML content removed] ")+maxErrorSummaryRunes+1 {
		t.Fatalf("HTML summary is not bounded: %d", len([]rune(summary)))
	}
}

func TestSanitizeResultRedactsCredentialMaterial(t *testing.T) {
	got := SanitizeResult(map[string]any{
		"token":                           "top-secret",
		"OS-EXT-SRV-ATTR:user_data":       "base64-cloud-init-secret",
		"OS-EXT-SRV-ATTR:cloud-init-data": "cloud-init-secret",
		"nested": map[string]any{
			"message": "Authorization: Bearer abc123 password=hunter2",
			"dsn":     "postgresql://reader:dbpass@example.test/app",
		},
	})
	data, _ := json.Marshal(got)
	text := strings.ToLower(string(data))
	for _, secret := range []string{"top-secret", "base64-cloud-init-secret", "cloud-init-secret", "abc123", "hunter2", "dbpass"} {
		if strings.Contains(text, secret) {
			t.Fatalf("result leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("result has no redaction marker: %s", text)
	}
}
