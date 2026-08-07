//go:build windows

package main

func detectDistro() string {
	return "windows"
}

func distroFamily() string {
	return "windows"
}

func runPlatformPreflightChecks() []PreflightCheckResult {
	return nil
}
