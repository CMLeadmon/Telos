//go:build !linux && !windows

package main

import (
	"runtime"
)

func detectDistro() string {
	return runtime.GOOS
}

func distroFamily() string {
	return runtime.GOOS
}

func runPlatformPreflightChecks() []PreflightCheckResult {
	return nil
}
