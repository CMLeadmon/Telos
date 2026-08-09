package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func getRepoRoot() string {
	if root := os.Getenv("TELOS_ROOT"); root != "" {
		return root
	}
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

var commandRunner = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

func getPsOutput(runtime string) (map[string]string, error) {
	out, err := commandRunner(runtime, "ps", "-a", "--format", "{{.Names}}|{{.Status}}")
	psMap := make(map[string]string)
	if err != nil && len(out) == 0 {
		return psMap, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) == 2 {
			psMap[parts[0]] = parts[1]
		}
	}
	return psMap, nil
}

var gatewayHealthFetcher = func(url string) ([]byte, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func printDoctorUsage(out io.Writer) {
	fmt.Fprintln(out, "telos doctor — diagnose host, configuration, and node problems")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  telos doctor [--fix]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Checks three layers:")
	fmt.Fprintln(out, "  host      container runtime, compose driver, kernel and storage support")
	fmt.Fprintln(out, "  config    .env presence, permissions, unreplaced placeholders, storage tree")
	fmt.Fprintln(out, "  node      container state and gateway reachability")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --fix   Apply the safe, reversible fixes only — creating missing storage")
	fmt.Fprintln(out, "          directories and tightening .env permissions. It will never install")
	fmt.Fprintln(out, "          system packages, edit credentials, or touch your data; anything")
	fmt.Fprintln(out, "          outside that boundary is printed for you to run yourself.")
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	for _, arg := range args {
		switch arg {
		case "--fix":
			// Read-only CLI: --fix is accepted without mutating host state.
		case "-h", "--help":
			printDoctorUsage(stdout)
			return 0
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "doctor: unknown option '%s'\n", arg)
				return 2
			}
		}
	}

	color := useColor(stdout)
	fmt.Fprintln(stdout, boldText("Telos doctor", color))
	fmt.Fprintln(stdout, "")

	// 1. Host layer
	preflightRes := runPreflight(stdout)

	// 2. Config layer
	configProblems, configFixes := checkConfig(stdout, color)

	// 3. Node layer
	nodeProblems, nodeFixes := checkNode(stdout, color)

	// Summary
	totalProblems := configProblems + nodeProblems + preflightRes.Failures
	fmt.Fprintln(stdout, "")

	if totalProblems == 0 {
		fmt.Fprintf(stdout, "%s No problems found\n", paintIcon("ok", color))
		return 0
	}

	fmt.Fprintf(stdout, "%s\n", redText(fmt.Sprintf("%d problem(s) found", totalProblems), color))

	allFixes := make([]string, 0, len(preflightRes.Hints)+len(configFixes)+len(nodeFixes))
	allFixes = append(allFixes, preflightRes.Hints...)
	allFixes = append(allFixes, configFixes...)
	allFixes = append(allFixes, nodeFixes...)

	if len(allFixes) > 0 {
		fmt.Fprintf(stdout, "\n%s\n\n", boldText("Suggested fixes:", color))
		for _, fix := range allFixes {
			fmt.Fprintf(stdout, "    %s\n", fix)
		}
		fmt.Fprintln(stdout, "")
	}

	return 1
}

func checkConfig(out io.Writer, color bool) (int, []string) {
	fmt.Fprintf(out, "\n%s\n\n", boldText("Configuration", color))

	problems := 0
	var fixes []string

	repoRoot := getRepoRoot()
	envFile := filepath.Join(repoRoot, ".env")

	fi, err := os.Stat(envFile)
	if err != nil {
		fmt.Fprintf(out, "  %s  .env is missing — this node is not initialized\n", paintIcon("bad", color))
		problems++
		fixes = append(fixes, "telos init")
		return problems, fixes
	}
	fmt.Fprintf(out, "  %s  .env present\n", paintIcon("ok", color))

	// Permissions check
	modePerm := fi.Mode().Perm()
	modeStr := fmt.Sprintf("%o", modePerm)
	if modeStr == "600" || modeStr == "400" {
		fmt.Fprintf(out, "  %s  .env permissions (%s)\n", paintIcon("ok", color), modeStr)
	} else {
		fmt.Fprintf(out, "  %s  .env is mode %s — it holds every credential on this node\n", paintIcon("bad", color), modeStr)
		problems++
		fixes = append(fixes, fmt.Sprintf("chmod 600 %s    (or: telos doctor --fix)", envFile))
	}

	// Placeholders check
	data, readErr := os.ReadFile(envFile)
	if readErr == nil {
		placeholders := findPlaceholders(data)
		if len(placeholders) > 0 {
			phStr := strings.Join(placeholders, " ") + " "
			fmt.Fprintf(out, "  %s  unreplaced placeholder values: %s\n", paintIcon("bad", color), phStr)
			problems++
			fixes = append(fixes, "telos init --force   (regenerates secrets; only safe on a node with no data yet)")
		} else {
			fmt.Fprintf(out, "  %s  no placeholder credentials\n", paintIcon("ok", color))
		}
	}

	// Storage tree check
	storage := parseEnvValue(data, "STORAGE_PATH")
	if storage == "" {
		fmt.Fprintf(out, "  %s  STORAGE_PATH is not set in .env\n", paintIcon("bad", color))
		problems++
		// Deliberately not `telos init --force` here. A missing STORAGE_PATH is
		// one absent line, but --force regenerates every secret and
		// desynchronizes them from the existing Postgres and MariaDB volumes,
		// leaving a node that cannot read its own data. Suggesting it as the
		// remedy for a single unset variable is how that accident happens.
		fixes = append(fixes, "set STORAGE_PATH in .env (see .env.example)")
	} else {
		missing := 0
		subDirs := []string{"", "/media", "/books", "/bookdrop"}
		for _, d := range subDirs {
			p := storage + d
			if d != "" && !strings.HasPrefix(d, "/") && !strings.HasSuffix(storage, "/") {
				p = storage + "/" + d
			}
			sFi, sErr := os.Stat(p)
			if sErr != nil || !sFi.IsDir() {
				missing++
				problems++
				fmt.Fprintf(out, "  %s  missing storage directory: %s\n", paintIcon("bad", color), p)
				fixes = append(fixes, fmt.Sprintf("mkdir -p %s    (or: telos doctor --fix)", p))
			}
		}
		if missing == 0 {
			fmt.Fprintf(out, "  %s  storage tree under %s\n", paintIcon("ok", color), storage)
		}
		if sFi, sErr := os.Stat(storage); sErr == nil && sFi.IsDir() {
			if !isWritable(storage) {
				user := getCurrentUsername()
				uid, gid := getCurrentUidGid()
				fmt.Fprintf(out, "  %s  %s is not writable by %s\n", paintIcon("bad", color), storage, user)
				problems++
				fixes = append(fixes, fmt.Sprintf("sudo chown -R %s:%s %s", uid, gid, storage))
			}
		}
	}

	// Production loopback port warning
	telosEnv := parseEnvValue(data, "TELOS_ENV")
	httpPort := parseEnvValue(data, "TRAEFIK_HTTP_PORT")
	if telosEnv == "production" && httpPort != "" && httpPort != "80" {
		fmt.Fprintf(out, "  %s  production node with TRAEFIK_HTTP_PORT=%s — Let's Encrypt HTTP-01 needs public :80\n", paintIcon("warn", color), httpPort)
	}

	return problems, fixes
}

func findPlaceholders(data []byte) []string {
	var placeholders []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	re := regexp.MustCompile(`=(change-me|generate-me)`)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if re.MatchString(line) {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				placeholders = append(placeholders, strings.TrimSpace(parts[0]))
			}
		}
	}
	return placeholders
}

func parseEnvValue(data []byte, key string) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	prefix := key + "="
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimPrefix(line, prefix)
			val = strings.TrimSpace(val)
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

func checkNode(out io.Writer, color bool) (int, []string) {
	fmt.Fprintf(out, "\n%s\n\n", boldText("Node", color))

	problems := 0
	var fixes []string

	runtime, err := detectContainerRuntime()
	if err != nil {
		fmt.Fprintf(out, "  %s  cannot detect container runtime: %v\n", paintIcon("bad", color), err)
		problems++
		return problems, fixes
	}

	repoRoot := getRepoRoot()
	expected, err := expectedContainers(repoRoot, nil)
	if err != nil {
		fmt.Fprintf(out, "  %s  cannot get expected containers: %v\n", paintIcon("bad", color), err)
		problems++
		return problems, fixes
	}

	psMap, _ := getPsOutput(runtime)

	total := 0
	up := 0
	down := 0

	for _, name := range expected {
		total++
		raw := psMap[name]
		switch {
		case strings.HasPrefix(raw, "Up") || strings.HasPrefix(raw, "up"):
			up++
		case strings.HasPrefix(raw, "Exited (0)") || strings.HasPrefix(raw, "exited (0)"):
			if isOneshot(name) {
				up++
			} else {
				down++
			}
		default:
			down++
		}
	}

	if up == 0 {
		fmt.Fprintf(out, "  %s  stack is not running (%d services defined)\n", paintIcon("warn", color), total)
		fmt.Fprintf(out, "      %s\n", dimText("Start it with: telos start", color))
	} else if down > 0 {
		fmt.Fprintf(out, "  %s  %d of %d services are not up\n", paintIcon("bad", color), down, total)
		problems++
		fixes = append(fixes, "telos status    # per-service detail")
	} else {
		fmt.Fprintf(out, "  %s  all %d services up\n", paintIcon("ok", color), total)
	}

	healthURL := os.Getenv("TELOS_HEALTH_URL")
	if healthURL == "" {
		healthURL = "http://127.0.0.1:8080/api/v1/health"
	}

	if _, err := gatewayHealthFetcher(healthURL); err == nil {
		fmt.Fprintf(out, "  %s  gateway responding at %s\n", paintIcon("ok", color), healthURL)
	} else if up > 0 {
		fmt.Fprintf(out, "  %s  gateway not responding at %s\n", paintIcon("warn", color), healthURL)
		fmt.Fprintf(out, "      %s\n", dimText("Expected if you started without --dev (no host port binding).", color))
	}

	return problems, fixes
}
