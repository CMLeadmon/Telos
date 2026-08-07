package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDispatchHelp(t *testing.T) {
	helpArgs := [][]string{
		{},
		{"help"},
		{"-h"},
		{"--help"},
	}

	for _, args := range helpArgs {
		var stdout, stderr bytes.Buffer
		code := dispatch(args, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("dispatch(%v) returned exit code %d, expected 0", args, code)
		}
		outStr := stdout.String()
		if !strings.Contains(outStr, "telos — operate a Telos node") {
			t.Errorf("dispatch(%v) stdout missing header: %s", args, outStr)
		}
		if !strings.Contains(outStr, "doctor       Diagnose host, config, and node problems") {
			t.Errorf("dispatch(%v) stdout missing doctor summary: %s", args, outStr)
		}
		if !strings.Contains(outStr, "init         Generate .env and create the storage tree") {
			t.Errorf("dispatch(%v) stdout missing init summary: %s", args, outStr)
		}
		if !strings.Contains(outStr, "start        Bring the stack up") {
			t.Errorf("dispatch(%v) stdout missing start summary: %s", args, outStr)
		}
		if !strings.Contains(outStr, "status       Report boot state for every service") {
			t.Errorf("dispatch(%v) stdout missing status summary: %s", args, outStr)
		}
		if !strings.Contains(outStr, "stop         Shut the stack down") {
			t.Errorf("dispatch(%v) stdout missing stop summary: %s", args, outStr)
		}
	}
}

func TestDispatchUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"unknown-command"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("dispatch(unknown-command) returned exit code %d, expected 2", code)
	}
	errStr := stderr.String()
	if !strings.Contains(errStr, "telos: unknown command 'unknown-command'") {
		t.Errorf("unexpected stderr output: %s", errStr)
	}
	if !strings.Contains(errStr, "Available commands:") {
		t.Errorf("stderr output missing 'Available commands:': %s", errStr)
	}
	if !strings.Contains(errStr, "doctor") || !strings.Contains(errStr, "init") {
		t.Errorf("stderr output missing command list: %s", errStr)
	}
}

func TestDispatchStubCommands(t *testing.T) {
	commands := []string{"doctor", "init", "start", "status", "stop"}
	for _, cmd := range commands {
		var stdout, stderr bytes.Buffer
		code := dispatch([]string{cmd}, &stdout, &stderr)
		if code == 0 {
			t.Fatalf("dispatch(%s) returned exit code 0, expected non-zero (stubbed command)", cmd)
		}
		errStr := stderr.String()
		if !strings.Contains(errStr, "not yet implemented") {
			t.Errorf("dispatch(%s) stderr missing 'not yet implemented': %s", cmd, errStr)
		}
	}
}

func TestDispatchInitForceSafety(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"init", "--force"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("dispatch(init --force) returned exit code 0, expected non-zero for stubbed init")
	}
	errStr := stderr.String()
	if !strings.Contains(errStr, "not yet implemented") {
		t.Errorf("dispatch(init --force) stderr missing 'not yet implemented': %s", errStr)
	}
}

func TestDispatchGlobalDevOption(t *testing.T) {
	os.Unsetenv("TELOS_DEV")
	defer os.Unsetenv("TELOS_DEV")

	var stdout, stderr bytes.Buffer
	code := dispatch([]string{"--dev", "help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dispatch(--dev help) returned exit code %d, expected 0", code)
	}
	if os.Getenv("TELOS_DEV") != "1" {
		t.Errorf("expected TELOS_DEV=1 after --dev flag, got %q", os.Getenv("TELOS_DEV"))
	}
}
