//go:build linux

package main

import (
	"testing"
	"time"
)

func TestQuotaPolicyValidation(t *testing.T) {
	if err := DefaultQuotaPolicy().validate(); err != nil {
		t.Fatalf("default policy invalid: %v", err)
	}
	bad := []QuotaPolicy{
		{PerUserPhysicalBytes: 0, NodeReserveBytes: 1, ReservationTTL: time.Minute},
		{PerUserPhysicalBytes: 1, NodeReserveBytes: 0, ReservationTTL: time.Minute},
		{PerUserPhysicalBytes: 1, NodeReserveBytes: 1, ReservationTTL: 0},
	}
	for i, p := range bad {
		if err := p.validate(); err == nil {
			t.Errorf("case %d: invalid policy accepted", i)
		}
	}
}
