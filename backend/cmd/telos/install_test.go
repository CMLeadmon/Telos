package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// A trimmed .env.example carrying one of every shape the generator has to
// handle: a placeholder secret, the differently-worded bootstrap placeholder, a
// setting that must survive untouched, a comment, and a blank line.
const exampleEnv = `# Telos node configuration
TELOS_DOMAIN=community.example.com

STORAGE_PATH=/mnt/storage/shared
POSTGRES_PASSWORD=change-me
TELOS_OWNER_PASSWORD=change-me
TELOS_RUNTIME_PASSWORD=change-me
REDIS_PASSWORD=change-me
GRIMMORY_ADMIN_PASSWORD=change-me
GRIMMORY_DB_PASSWORD=change-me
MARIADB_ROOT_PASSWORD=change-me
TELOS_BOOTSTRAP_TOKEN=generate-me-with-openssl-rand-hex-24
`

func writeExample(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, ".env.example")
	if err := os.WriteFile(path, []byte(exampleEnv), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func envValues(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated env: %v", err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || strings.HasPrefix(line, "#") {
			continue
		}
		values[key] = value
	}
	return values
}

// The whole point of generation. Asserting only that the key is still present
// would pass on a file that kept every password at "change-me".
func TestGenerateConfigEnvReplacesEveryPlaceholder(t *testing.T) {
	dir := t.TempDir()
	example := writeExample(t, dir)
	envPath := filepath.Join(dir, ".env")

	if err := generateConfigEnv(example, envPath); err != nil {
		t.Fatalf("generateConfigEnv: %v", err)
	}

	values := envValues(t, envPath)
	hex48 := regexp.MustCompile(`^[0-9a-f]{48}$`)
	seen := map[string]string{}
	for _, key := range initSecrets {
		got, ok := values[key]
		if !ok {
			t.Fatalf("%s missing from the generated .env", key)
		}
		if got == "change-me" || strings.HasPrefix(got, "generate-me") {
			t.Fatalf("%s was left at its placeholder %q", key, got)
		}
		if !hex48.MatchString(got) {
			t.Fatalf("%s = %q, want 24 random bytes as hex", key, got)
		}
		// Independently random: one secret reused across services means one
		// leak compromises all of them.
		if other, dup := seen[got]; dup {
			t.Fatalf("%s and %s were given the same secret", other, key)
		}
		seen[got] = key
	}
}

func TestGenerateConfigEnvLeavesEverythingElseAlone(t *testing.T) {
	dir := t.TempDir()
	example := writeExample(t, dir)
	envPath := filepath.Join(dir, ".env")

	if err := generateConfigEnv(example, envPath); err != nil {
		t.Fatalf("generateConfigEnv: %v", err)
	}

	values := envValues(t, envPath)
	if values["TELOS_DOMAIN"] != "community.example.com" {
		t.Errorf("TELOS_DOMAIN = %q, want it untouched", values["TELOS_DOMAIN"])
	}
	if values["STORAGE_PATH"] != "/mnt/storage/shared" {
		t.Errorf("STORAGE_PATH = %q, want it untouched", values["STORAGE_PATH"])
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# Telos node configuration") {
		t.Error("the comment header did not survive generation")
	}
}

// Regenerating .env on a live node issues database passwords that no longer
// match the credentials inside the existing Postgres and MariaDB volumes, and
// the stack stops being able to read its own data. Refusing is the only safe
// default; overwriting is a separate, deliberate act.
func TestGenerateConfigEnvRefusesToClobberAnExistingNode(t *testing.T) {
	dir := t.TempDir()
	example := writeExample(t, dir)
	envPath := filepath.Join(dir, ".env")
	existing := "POSTGRES_PASSWORD=the-live-one\n"
	if err := os.WriteFile(envPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	err := generateConfigEnv(example, envPath)
	if !errors.Is(err, errEnvExists) {
		t.Fatalf("error = %v, want errEnvExists", err)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Fatalf("the existing .env was modified: %q", string(data))
	}
}

func TestGenerateConfigEnvWritesAPrivateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	example := writeExample(t, dir)
	envPath := filepath.Join(dir, ".env")

	if err := generateConfigEnv(example, envPath); err != nil {
		t.Fatalf("generateConfigEnv: %v", err)
	}

	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	// It holds every credential on the node; group and other have no business
	// reading it.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

// A failed run must leave no half-written file that a later run would mistake
// for a real .env — the file is staged beside the target and renamed into
// place only once every secret is set.
func TestGenerateConfigEnvLeavesNoDebrisWhenTheExampleIsMissing(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	if err := generateConfigEnv(filepath.Join(dir, "absent.example"), envPath); err == nil {
		t.Fatal("expected an error for a missing .env.example")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Fatalf("left %q behind after a failed run", entry.Name())
	}
}

func TestSetEnvVarReplacesInPlaceAndAppendsWhenAbsent(t *testing.T) {
	lines := []string{"A=1", "B=2", "# comment"}

	lines = setEnvVar(lines, "B", "replaced")
	if lines[1] != "B=replaced" {
		t.Fatalf("in-place replacement produced %q", lines[1])
	}
	if len(lines) != 3 {
		t.Fatalf("replacement changed the line count to %d", len(lines))
	}

	lines = setEnvVar(lines, "C", "appended")
	if got := lines[len(lines)-1]; got != "C=appended" {
		t.Fatalf("append produced %q", got)
	}
}

// A key must match on the whole name. Prefix matching would let TELOS_OWNER
// overwrite TELOS_OWNER_PASSWORD, silently replacing a generated secret with a
// setting.
func TestSetEnvVarMatchesWholeKeysOnly(t *testing.T) {
	lines := []string{"TELOS_OWNER_PASSWORD=secret"}

	lines = setEnvVar(lines, "TELOS_OWNER", "someone")

	if lines[0] != "TELOS_OWNER_PASSWORD=secret" {
		t.Fatalf("a prefix key overwrote a different variable: %q", lines[0])
	}
	if len(lines) != 2 || lines[1] != "TELOS_OWNER=someone" {
		t.Fatalf("expected TELOS_OWNER appended, got %v", lines)
	}
}

func TestGenerateSecretIsRandomAndWideEnough(t *testing.T) {
	first, err := generateSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two calls returned the same secret")
	}
	// 24 bytes, matching `openssl rand -hex 24` in cli/commands/init.sh, so a
	// node reads the same however it was initialized.
	if len(first) != 48 {
		t.Fatalf("secret length = %d, want 48 hex characters", len(first))
	}
}

// Windows has neither /usr/local/bin nor unprivileged symlinks, so the same
// candidate list would find nothing there and uninstall would report success
// having removed nothing.
func TestExecutableLocationsArePlatformCorrect(t *testing.T) {
	posix := executableLocations("linux", "/home/someone")
	if len(posix) == 0 {
		t.Fatal("no candidates for linux")
	}
	foundUserLocal := false
	for _, path := range posix {
		if path == "/home/someone/.local/bin/telos" {
			foundUserLocal = true
		}
		if strings.HasSuffix(path, ".exe") {
			t.Errorf("linux candidate %q has a Windows extension", path)
		}
	}
	if !foundUserLocal {
		t.Errorf("linux candidates %v omit the link install.sh --user creates", posix)
	}

	windows := executableLocations("windows", `C:\Users\Someone`)
	if len(windows) == 0 {
		t.Fatal("no candidates for windows")
	}
	for _, path := range windows {
		if !strings.HasSuffix(path, "telos.exe") {
			t.Errorf("windows candidate %q is not an .exe", path)
		}
	}
}
