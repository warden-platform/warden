package domain

import "testing"

func TestClassifyDiagnosticStatus(t *testing.T) {
	tests := []struct {
		name     string
		exec     ExecutionStatus
		provider ProviderResponseStatus
		error    string
		want     DiagnosticClass
	}{
		{"timeout error code wins", ExecFailed, "", ProviderErrTimeout, DiagnosticTimeout},
		{"method denied", ExecFailed, "", ProviderErrMethodDenied, DiagnosticNoPermission},
		{"path denied", ExecFailed, "", ProviderErrPathDenied, DiagnosticNoPermission},
		{"query denied", ExecFailed, "", ProviderErrQueryDenied, DiagnosticNoPermission},
		{"header denied", ExecFailed, "", ProviderErrHeaderDenied, DiagnosticNoPermission},
		{"scope denied", ExecFailed, "", ProviderErrScopeDenied, DiagnosticNoPermission},
		{"capability mismatch", ExecFailed, "", ProviderErrCapabilityMismatch, DiagnosticNoPermission},
		{"upstream unknown is not connected", ExecFailed, "", ProviderErrUpstreamUnknown, DiagnosticNotConnected},
		{"endpoint unsafe is not connected", ExecFailed, "", ProviderErrEndpointUnsafe, DiagnosticNotConnected},
		{"redirect denied is not connected", ExecFailed, "", ProviderErrRedirectDenied, DiagnosticNotConnected},
		{"integration inactive is not connected", ExecFailed, "", ProviderErrIntegrationInactive, DiagnosticNotConnected},
		{"resource unavailable is not connected", ExecFailed, "", ProviderErrResourceUnavailable, DiagnosticNotConnected},
		{"provider no data", ExecSucceeded, ProviderResponseNoData, "", DiagnosticNoData},
		{"provider succeeded", ExecSucceeded, ProviderResponseSucceeded, "", DiagnosticSucceeded},
		{"provider partial", ExecSucceeded, ProviderResponsePartial, "", DiagnosticPartial},
		{"provider failed", ExecFailed, ProviderResponseFailed, "", DiagnosticFailed},
		{"provider unknown", ExecUnknown, ProviderResponseUnknown, "", DiagnosticUnknown},
		{"exec succeeded without provider", ExecSucceeded, "", "", DiagnosticSucceeded},
		{"exec failed without provider", ExecFailed, "", "", DiagnosticFailed},
		{"exec expired is unknown", ExecExpired, "", "", DiagnosticUnknown},
		{"exec cancelled is unknown", ExecCancelled, "", "", DiagnosticUnknown},
		{"exec queued is unknown", ExecQueued, "", "", DiagnosticUnknown},
		{"error code overrides provider success", ExecSucceeded, ProviderResponseSucceeded, ProviderErrTimeout, DiagnosticTimeout},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyDiagnosticStatus(test.exec, test.provider, test.error); got != test.want {
				t.Fatalf("ClassifyDiagnosticStatus(%q,%q,%q) = %q, want %q", test.exec, test.provider, test.error, got, test.want)
			}
		})
	}
}

func TestValidateTraceID(t *testing.T) {
	valid := []string{
		"a",
		"trace-123",
		"abc123.def_ghi-x",
		"0abc",
		NewTraceID(),
	}
	for _, id := range valid {
		if !ValidateTraceID(id) {
			t.Errorf("ValidateTraceID(%q) = false, want true", id)
		}
	}

	invalid := []string{
		"",
		"ABC",
		"-leading-hyphen",
		".leading-dot",
		"has:colon",
		"has space",
		"UPPER",
		"has/slash",
	}
	for _, id := range invalid {
		if ValidateTraceID(id) {
			t.Errorf("ValidateTraceID(%q) = true, want false", id)
		}
	}

	// 64 chars is allowed; 65 is not.
	long := make([]byte, 64)
	for i := range long {
		long[i] = 'a'
	}
	if !ValidateTraceID(string(long)) {
		t.Error("ValidateTraceID(64 chars) = false, want true")
	}
	tooLong := make([]byte, 65)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	if ValidateTraceID(string(tooLong)) {
		t.Error("ValidateTraceID(65 chars) = true, want false")
	}
}
