package main

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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

func TestCatalogMetricsRecordBoundedOutcomes(t *testing.T) {
	before := map[CatalogReconcileOutcome]float64{}
	for _, outcome := range []CatalogReconcileOutcome{
		CatalogOutcomeCreated,
		CatalogOutcomeRefreshed,
		CatalogOutcomeRestored,
		CatalogOutcomeMissing,
		CatalogOutcomeAmbiguous,
	} {
		before[outcome] = counterValue(t, CatalogReconciledSources.WithLabelValues(string(ProviderGrimmory), string(outcome)))
		if err := recordCatalogReconciled(ProviderGrimmory, outcome, 1); err != nil {
			t.Fatalf("record %q: %v", outcome, err)
		}
		got := counterValue(t, CatalogReconciledSources.WithLabelValues(string(ProviderGrimmory), string(outcome)))
		if got != before[outcome]+1 {
			t.Fatalf("outcome %q counter = %v, want %v", outcome, got, before[outcome]+1)
		}
	}

	failedBefore := counterValue(t, CatalogReconciledSources.WithLabelValues(string(ProviderJellyfin), string(CatalogOutcomeFailed)))
	if err := RecordCatalogEnumerationFailure(ProviderJellyfin, 25*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	failedAfter := counterValue(t, CatalogReconciledSources.WithLabelValues(string(ProviderJellyfin), string(CatalogOutcomeFailed)))
	if failedAfter != failedBefore+1 {
		t.Fatalf("failed counter = %v, want %v", failedAfter, failedBefore+1)
	}
}

func counterValue(t *testing.T, counter prometheus.Counter) float64 {
	t.Helper()
	var metric dto.Metric
	if err := counter.Write(&metric); err != nil {
		t.Fatal(err)
	}
	return metric.GetCounter().GetValue()
}

func TestCatalogMetricsRejectUnboundedLabels(t *testing.T) {
	if err := recordCatalogReconciled(ProviderGrimmory, CatalogReconcileOutcome("member-123"), 1); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("outcome error = %v", err)
	}
	if err := RecordCatalogEnumerationFailure(CatalogProvider("member-123"), time.Second); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("provider error = %v", err)
	}
	if err := observeCatalogReconcileDuration(ProviderJellyfin, CatalogOperation("/private/path"), CatalogResultSuccess, time.Second); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("operation error = %v", err)
	}
	if err := observeCatalogReconcileDuration(ProviderJellyfin, CatalogOperationScan, CatalogResult("canonical-id"), time.Second); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("result error = %v", err)
	}
	before := counterValue(t, CatalogReconciledSources.WithLabelValues(string(ProviderJellyfin), string(CatalogOutcomeFailed)))
	if err := RecordCatalogEnumerationFailure(ProviderJellyfin, -time.Second); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("duration error = %v", err)
	}
	if after := counterValue(t, CatalogReconciledSources.WithLabelValues(string(ProviderJellyfin), string(CatalogOutcomeFailed))); after != before {
		t.Fatalf("invalid recorder changed failed counter from %v to %v", before, after)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstringHelper(s, substr))
}

func containsSubstringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
