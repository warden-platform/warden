package domain

import (
	"html"
	"regexp"
	"strings"
)

const maxErrorMessageRunes = 1024
const maxErrorSummaryRunes = 160

var htmlTagPattern = regexp.MustCompile(`(?is)<[^>]*>`)
var unterminatedTagPattern = regexp.MustCompile(`(?is)<[^>]*$`)
var activeContentPattern = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
var authorizationPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,;]+`)
var assignmentSecretPattern = regexp.MustCompile(`(?i)(\b(?:password|passwd|access_token|api_key|secret_key|client_secret)\s*[:=]\s*)[^\s,;]+`)
var urlCredentialPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s:/@]+:)[^\s/@]+(@)`)
var nonAlphanumericPattern = regexp.MustCompile(`[^a-z0-9]`)

func sensitiveResultKey(key string) bool {
	normalized := nonAlphanumericPattern.ReplaceAllString(strings.ToLower(key), "")
	for _, marker := range []string{"password", "passwd", "authorization", "token", "apikey", "secretkey", "clientsecret", "privatekey", "cookie", "userdata", "cloudinit"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func sanitizeResultString(value string) string {
	value = authorizationPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = assignmentSecretPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	return urlCredentialPattern.ReplaceAllString(value, `${1}[REDACTED]${2}`)
}

// SanitizeResult removes common credential material before an execution result
// is persisted. It preserves operational output while recursively redacting
// sensitive fields and obvious secrets embedded in logs or URLs.
func SanitizeResult(value map[string]any) map[string]any {
	sanitized, _ := sanitizeResultValue(value, 0).(map[string]any)
	return sanitized
}

func sanitizeResultValue(value any, depth int) any {
	if depth > 32 {
		return "[TRUNCATED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveResultKey(key) {
				result[key] = "[REDACTED]"
			} else {
				result[key] = sanitizeResultValue(item, depth+1)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = sanitizeResultValue(item, depth+1)
		}
		return result
	case string:
		return sanitizeResultString(typed)
	default:
		return value
	}
}

// stripHTMLMarkup repeatedly removes tags and unescapes entities until the
// text is stable, so escaped markup (&lt;link ...&gt;) and truncated,
// unterminated tags (<link rel="stylesheet" ...) are cleaned as well.
func stripHTMLMarkup(body string) string {
	for i := 0; i < 4; i++ {
		before := body
		body = activeContentPattern.ReplaceAllString(body, " ")
		body = htmlTagPattern.ReplaceAllString(body, " ")
		body = unterminatedTagPattern.ReplaceAllString(body, " ")
		body = html.UnescapeString(body)
		if body == before {
			break
		}
	}
	body = htmlTagPattern.ReplaceAllString(body, " ")
	body = unterminatedTagPattern.ReplaceAllString(body, " ")
	body = strings.ReplaceAll(body, "<", " ")
	return strings.Join(strings.Fields(body), " ")
}

// SanitizeErrorMessage keeps downstream failures useful without allowing a
// complete HTML error page, embedded scripts, crumbs, or an unbounded payload
// to enter audit APIs and management tables.
func SanitizeErrorMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	index := strings.Index(value, "<")
	if escaped := strings.Index(value, "&lt;"); escaped >= 0 && (index < 0 || escaped < index) {
		index = escaped
	}
	if index >= 0 {
		prefix := strings.TrimSpace(value[:index])
		text := stripHTMLMarkup(value[index:])
		textRunes := []rune(text)
		if len(textRunes) > maxErrorSummaryRunes {
			text = string(textRunes[:maxErrorSummaryRunes]) + "…"
		}
		if prefix != "" {
			value = prefix + " [HTML content removed]"
			if text != "" {
				value += " " + text
			}
		} else {
			value = "[HTML content removed] " + text
		}
	}
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxErrorMessageRunes {
		value = string(runes[:maxErrorMessageRunes]) + "…"
	}
	return value
}
