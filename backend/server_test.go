package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShuttingDownMiddlewareRejectsNewWork(t *testing.T) {
	t.Cleanup(func() { shuttingDown.Store(false) })

	reached := false
	h := requestIDMiddleware(shuttingDownMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})))

	// Before shutdown: passes through.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/x", nil))
	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("pre-shutdown request blocked (code %d)", rec.Code)
	}

	// After shutdown begins: rejected with 503 shutting_down.
	shuttingDown.Store(true)
	reached = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/x", nil))
	if reached {
		t.Fatal("request reached handler during shutdown")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body apiErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.Error.Code != "shutting_down" {
		t.Fatalf("expected shutting_down public error, got %+v err=%v", body, err)
	}
}

func TestShutdownStageBudgetWithinGrace(t *testing.T) {
	// The staged deadlines must sum to at most the 55s budget the Compose
	// stop_grace_period (75s) is sized against.
	total := httpDrainDeadline + socketDrainDeadline + outboxDrainDeadline
	if total.Seconds() > 55 {
		t.Fatalf("staged shutdown budget %.0fs exceeds 55s", total.Seconds())
	}
}
