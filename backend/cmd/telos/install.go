package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The variables that must receive an independently-random secret. Every one of
// these is a placeholder in .env.example, and leaving any at its placeholder is
// a production credential leak — so they are generated, never prompted for.
//
// Kept in step with INIT_SECRETS in cli/commands/init.sh: the two CLIs
// initialize the same node and must produce the same file.
var initSecrets = []string{
	"POSTGRES_PASSWORD",
	"TELOS_OWNER_PASSWORD",
	"TELOS_RUNTIME_PASSWORD",
	"REDIS_PASSWORD",
	"GRIMMORY_ADMIN_PASSWORD",
	"GRIMMORY_DB_PASSWORD",
	"MARIADB_ROOT_PASSWORD",
	"TELOS_BOOTSTRAP_TOKEN",
}

// errEnvExists reports that this node is already initialized. Callers turn it
// into advice rather than an error message: an existing .env usually means
// there is nothing left to do.
var errEnvExists = errors.New(".env already exists")

// 24 bytes, matching `openssl rand -hex 24` in the bash CLI, so a node reads
// the same however it was initialized.
const secretBytes = 24

func generateSecret() (string, error) {
	buf := make([]byte, secretBytes)
	// Never discard this error. A silent failure here writes an all-zero
	// "secret" into a production .env — a node that boots, looks healthy, and
	// has a publicly guessable database password.
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("could not generate a secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// setEnvVar replaces key's line in place, or appends one if the key is absent.
//
// The match is on the whole key, not a prefix: TELOS_OWNER must not overwrite
// TELOS_OWNER_PASSWORD, which would replace a generated secret with a setting
// and leave the real password nowhere.
func setEnvVar(lines []string, key, value string) []string {
	prefix := key + "="
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = prefix + value
			return lines
		}
	}
	return append(lines, prefix+value)
}

// generateConfigEnv writes envPath from examplePath with every secret freshly
// generated. It refuses to overwrite an existing .env.
//
// Regenerating on a live node issues database passwords that no longer match
// the credentials already inside the Postgres and MariaDB volumes, and the
// stack stops being able to read its own data. Overwriting has to be a separate
// deliberate act, with the old file backed up first.
func generateConfigEnv(examplePath, envPath string) error {
	if _, err := os.Stat(envPath); err == nil {
		return errEnvExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("could not check for an existing %s: %w", envPath, err)
	}

	content, err := os.ReadFile(examplePath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", examplePath, err)
	}

	lines := strings.Split(string(content), "\n")
	for _, key := range initSecrets {
		secret, err := generateSecret()
		if err != nil {
			return err
		}
		lines = setEnvVar(lines, key, secret)
	}

	return writePrivateFile(envPath, strings.Join(lines, "\n"))
}

// writePrivateFile stages the content beside the target and renames it into
// place only once it is complete.
//
// An interrupted write must never leave a half-finished .env behind: it would
// look initialized while still holding placeholder passwords, which is worse
// than having no .env at all.
func writePrivateFile(path, content string) (err error) {
	dir := filepath.Dir(path)
	staged, err := os.CreateTemp(dir, filepath.Base(path)+".staging.*")
	if err != nil {
		return fmt.Errorf("could not stage a file next to %s: %w", path, err)
	}
	name := staged.Name()
	// Harmless once the rename below has claimed it, and the whole point on
	// every path that does not get that far.
	defer func() { _ = os.Remove(name) }()

	if err := staged.Chmod(0o600); err != nil {
		staged.Close()
		return fmt.Errorf("could not restrict %s: %w", name, err)
	}
	if _, err := staged.WriteString(content); err != nil {
		staged.Close()
		return fmt.Errorf("could not write %s: %w", name, err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("could not close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("could not install %s: %w", path, err)
	}
	return nil
}

// executableName is what the CLI is called on this platform.
func executableName(goos string) string {
	if goos == "windows" {
		return "telos.exe"
	}
	return "telos"
}

// executableLocations lists where a `telos` executable may have been installed,
// most-shared first.
//
// Windows has neither /usr/local/bin nor unprivileged symlinks, so it gets the
// per-user Programs tree instead — the same list on both would find nothing
// there, and an uninstall would report success having removed nothing.
func executableLocations(goos, home string) []string {
	exe := executableName(goos)
	if goos == "windows" {
		return []string{
			filepath.Join(home, "AppData", "Local", "Programs", "Telos", exe),
		}
	}
	return []string{
		filepath.Join("/usr", "local", "bin", exe),
		filepath.Join(home, ".local", "bin", exe),
		filepath.Join("/usr", "bin", exe),
	}
}
