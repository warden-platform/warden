package domain

import "regexp"

// DiagnosticClass is a provider-agnostic classification for a single diagnostic
// step within a session trace. It collapses the two existing status enums
// (ExecutionStatus and ProviderResponseStatus) plus the provider error code
// into one uniform bucket so an agent can reason about a cross-platform trace
// without knowing each platform's native status vocabulary.
type DiagnosticClass string

const (
	DiagnosticSucceeded    DiagnosticClass = "SUCCEEDED"
	DiagnosticPartial      DiagnosticClass = "PARTIAL"
	DiagnosticNoData       DiagnosticClass = "NO_DATA"       // 无数据
	DiagnosticTimeout      DiagnosticClass = "TIMEOUT"       // 超时
	DiagnosticNoPermission DiagnosticClass = "NO_PERMISSION" // 无权限
	DiagnosticNotConnected DiagnosticClass = "NOT_CONNECTED" // 未接入
	DiagnosticFailed       DiagnosticClass = "FAILED"
	DiagnosticUnknown      DiagnosticClass = "UNKNOWN"
)

// traceIDPattern reuses the provider correlation-key character set but permits
// a leading digit, because NewTraceID generates hex values that may start with
// 0-9. It never admits uppercase, whitespace, or header-sensitive separators.
var traceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// ValidateTraceID reports whether id is safe to use as a session trace
// identifier. The value is used only for correlation; it never influences an
// authorization decision.
func ValidateTraceID(id string) bool {
	return id != "" && traceIDPattern.MatchString(id)
}

// ClassifyDiagnosticStatus maps the three sources of a completed execution into
// one DiagnosticClass. Precedence is error code first (most specific), then the
// provider response status, and finally the execution status. A zero provider
// status means the execution did not carry a provider result.
func ClassifyDiagnosticStatus(exec ExecutionStatus, provider ProviderResponseStatus, errorCode string) DiagnosticClass {
	switch errorCode {
	case ProviderErrTimeout:
		return DiagnosticTimeout
	case ProviderErrMethodDenied, ProviderErrPathDenied, ProviderErrQueryDenied,
		ProviderErrHeaderDenied, ProviderErrScopeDenied, ProviderErrCapabilityMismatch:
		return DiagnosticNoPermission
	case ProviderErrUpstreamUnknown, ProviderErrEndpointUnsafe, ProviderErrRedirectDenied,
		ProviderErrIntegrationInactive, ProviderErrResourceUnavailable:
		return DiagnosticNotConnected
	}
	switch provider {
	case ProviderResponseNoData:
		return DiagnosticNoData
	case ProviderResponseSucceeded:
		return DiagnosticSucceeded
	case ProviderResponsePartial:
		return DiagnosticPartial
	case ProviderResponseFailed:
		return DiagnosticFailed
	case ProviderResponseUnknown:
		return DiagnosticUnknown
	}
	switch exec {
	case ExecSucceeded:
		return DiagnosticSucceeded
	case ExecFailed:
		return DiagnosticFailed
	case ExecExpired, ExecCancelled:
		return DiagnosticUnknown
	default:
		return DiagnosticUnknown
	}
}
