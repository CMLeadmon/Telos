package main

import (
	"net/http/httptest"
	"testing"
)

func TestMetricsHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	handler := metricsHandler()
	handler.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !containsSubstring(body, "telos_active_sockets") {
		t.Errorf("metrics response missing telos_active_sockets: %s", body)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstringHelper(s, substr))
}

func containsSubstringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true;
		}
	}
	return false;
}
