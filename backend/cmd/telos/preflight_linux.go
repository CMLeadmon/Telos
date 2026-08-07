//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func detectDistro() string {
	if f, err := os.Open("/etc/os-release"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "ID=") {
				id := strings.Trim(line[3:], `"'`)
				if id != "" {
					return id
				}
			}
		}
	}
	return "unknown"
}

// distroFamily reads the host's identity and defers the actual mapping to
// familyFromIDs, which is shared and platform-independent.
func distroFamily() string {
	var idLike string
	if f, err := os.Open("/etc/os-release"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "ID_LIKE=") {
				idLike = strings.Trim(line[8:], `"'`)
				break
			}
		}
	}
	return familyFromIDs(detectDistro(), idLike)
}

func isRoot() bool {
	return os.Geteuid() == 0
}

func runPlatformPreflightChecks() []PreflightCheckResult {
	var results []PreflightCheckResult

	if res, ok := checkOverlayDriver(); ok {
		results = append(results, res)
	}
	if res, ok := checkTunModule(); ok {
		results = append(results, res)
	}
	if res, ok := checkRegistries(); ok {
		results = append(results, res)
	}
	if res, ok := checkPrivilegedPorts(); ok {
		results = append(results, res)
	}

	return results
}

func checkOverlayDriver() (PreflightCheckResult, bool) {
	if _, err := exec.LookPath("podman"); err != nil {
		return PreflightCheckResult{}, false
	}
	if isRoot() {
		return PreflightCheckResult{}, false
	}

	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		homeDir = os.Getenv("HOME")
	}

	fstype := "unknown"
	var stat unix.Statfs_t
	if err := unix.Statfs(homeDir, &stat); err == nil {
		stType := int64(stat.Type)
		switch {
		case stType == int64(unix.BTRFS_SUPER_MAGIC) || stType == 0x9123683e:
			fstype = "btrfs"
		case stType == 0x2fc12fc1:
			fstype = "zfs"
		case stType == int64(unix.OVERLAYFS_SUPER_MAGIC) || stType == 0x794c7604:
			fstype = "overlayfs"
		case stType == int64(unix.EXT4_SUPER_MAGIC) || stType == 0xef53:
			fstype = "ext4"
		case stType == int64(unix.XFS_SUPER_MAGIC) || stType == 0x58465342:
			fstype = "xfs"
		}
	}

	switch fstype {
	case "btrfs", "zfs", "overlayfs":
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("storage driver: native overlay ok on %s", fstype),
		}, true
	}

	if _, err := exec.LookPath("fuse-overlayfs"); err == nil {
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("storage driver: fuse-overlayfs present (needed on %s)", fstype),
		}, true
	}

	return PreflightCheckResult{
		Status:  StatusFail,
		Message: fmt.Sprintf("fuse-overlayfs missing — rootless podman cannot use overlay on %s", fstype),
		Hint:    installHint("fuse-overlayfs"),
	}, true
}

func checkTunModule() (PreflightCheckResult, bool) {
	if _, err := exec.LookPath("podman"); err != nil {
		return PreflightCheckResult{}, false
	}
	if isRoot() {
		return PreflightCheckResult{}, false
	}

	fi, err := os.Stat("/dev/net/tun")
	if err == nil && (fi.Mode()&os.ModeCharDevice != 0) {
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: "kernel: /dev/net/tun present",
		}, true
	}

	return PreflightCheckResult{
		Status:  StatusFail,
		Message: "/dev/net/tun missing — rootless container networking will fail",
		Hint:    "sudo modprobe tun    # persist: echo tun | sudo tee /etc/modules-load.d/tun.conf",
	}, true
}

func checkRegistries() (PreflightCheckResult, bool) {
	if _, err := exec.LookPath("podman"); err != nil {
		return PreflightCheckResult{}, false
	}

	homeDir, _ := os.UserHomeDir()
	files := []string{
		filepath.Join(homeDir, ".config", "containers", "registries.conf"),
		"/etc/containers/registries.conf",
	}

	re := regexp.MustCompile(`^[[:space:]]*unqualified-search-registries`)
	found := false

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			if re.MatchString(scanner.Text()) {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if found {
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: "podman: unqualified-search-registries configured",
		}, true
	}

	return PreflightCheckResult{
		Status:  StatusFail,
		Message: "podman cannot resolve short image names (no unqualified-search-registries)",
		Hint:    `mkdir -p ~/.config/containers && printf 'unqualified-search-registries = ["docker.io"]\n' > ~/.config/containers/registries.conf`,
	}, true
}

func checkPrivilegedPorts() (PreflightCheckResult, bool) {
	if isRoot() {
		return PreflightCheckResult{}, false
	}
	if _, err := exec.LookPath("podman"); err != nil {
		return PreflightCheckResult{}, false
	}

	start := 1024
	if data, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start"); err == nil {
		if val, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			start = val
		}
	}

	if start <= 80 {
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("ports: unprivileged binding allowed from %d", start),
		}, true
	}

	return PreflightCheckResult{
		Status:  StatusWarn,
		Message: fmt.Sprintf("rootless runtime cannot bind :80/:443 (unprivileged start = %d)", start),
		Hint:    "Set TRAEFIK_HTTP_PORT / TRAEFIK_HTTPS_PORT in .env (telos init does this), or: sudo sysctl -w net.ipv4.ip_unprivileged_port_start=80",
	}, true
}
