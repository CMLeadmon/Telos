package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

var (
	// errUninstallAborted reports that the member said no, or that there was no
	// way to ask. Not a failure — a decision.
	errUninstallAborted = errors.New("uninstall aborted")

	// errNoTerminal reports that a question that must be asked could not be.
	errNoTerminal = errors.New("no terminal to confirm on")
)

type uninstallOptions struct {
	// appDir is the installed application tree — the checkout itself for a
	// --link-only install.
	appDir string
	// candidates are the executable locations to examine, from
	// executableLocations.
	candidates []string
	// purge also deletes appDir, and with it the .env inside.
	purge bool
	// assumeYes skips the ordinary confirmation. It deliberately does not skip
	// the question about .env.
	assumeYes bool
	// interactive reports whether there is a terminal to prompt on.
	interactive bool
	// confirm asks a yes/no question. nil means it cannot be asked.
	confirm func(prompt string) bool
}

// removableEntries returns the candidates this installer put there, leaving
// anything else alone.
//
// On POSIX that is a symlink resolving to a telos executable — install.sh
// links, so a real file of the same name belongs to someone else. Windows has
// no unprivileged symlinks, so there the installed thing is the executable
// itself, sitting in the install directory.
func removableEntries(candidates []string, exeName, installDir string) []string {
	found := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate)
		if err != nil {
			continue
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(candidate)
			if err != nil {
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(candidate), target)
			}
			if filepath.Base(target) == exeName {
				found = append(found, candidate)
			}
		case info.Mode().IsRegular():
			// Guarded, because filepath.Clean("") is "." — an unset installDir
			// would match any bare relative candidate and delete a file called
			// telos out of the working directory.
			if installDir == "" {
				continue
			}
			if filepath.Clean(filepath.Dir(candidate)) == filepath.Clean(installDir) {
				found = append(found, candidate)
			}
		}
	}
	return found
}

// runUninstallPlan removes what the installer added, and nothing else.
//
// Storage directories and container volumes are never touched here: those are
// node data, not installed files, and destroying them is a separate explicit
// act. .env is the exception that has to be called out, because it lives inside
// the application tree and --purge therefore deletes it.
func runUninstallPlan(opts uninstallOptions, out io.Writer) error {
	color := useColor(out)
	confirm := opts.confirm
	if confirm == nil {
		// Nothing to ask on. Every question answers no.
		confirm = func(string) bool { return false }
	}

	fmt.Fprintf(out, "%s\n\n", boldText("Telos uninstaller", color))

	found := removableEntries(opts.candidates, executableName(runtime.GOOS), opts.appDir)
	if len(found) == 0 {
		fmt.Fprintln(out, "  No telos executable found in the usual places.")
	} else {
		fmt.Fprintln(out, "Will remove:")
		for _, path := range found {
			fmt.Fprintf(out, "  %s\n", path)
		}
	}
	if opts.purge {
		fmt.Fprintf(out, "  %s\n", redText(opts.appDir+" (entire directory)", color))
	}
	fmt.Fprintln(out)

	if !opts.assumeYes && !confirm("Proceed?") {
		fmt.Fprintln(out, "Aborted.")
		return errUninstallAborted
	}

	for _, path := range found {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("could not remove %s: %w", path, err)
		}
		fmt.Fprintf(out, "  %s  removed %s\n", paintIcon("ok", color), path)
	}

	if opts.purge {
		envPath := filepath.Join(opts.appDir, ".env")
		_, err := os.Stat(envPath)
		switch {
		case err == nil:
			fmt.Fprintf(out, "\n  %s  %s holds this node's credentials.\n",
				paintIcon("warn", color), envPath)
			fmt.Fprintln(out, "  Purging deletes it. Copy it somewhere safe first if you want to keep it.")

			// Deliberately outside --yes. These credentials are paired with the
			// Postgres and MariaDB volumes that were encrypted against them;
			// regenerating them later leaves a node that cannot read its own
			// data, and there is no way back.
			//
			// A closed stdin answers instantly and emptily, which used to fall
			// through to "kept" and exit 0 — so a scripted --purge --yes
			// reported success having done nothing at all. Fail loudly.
			if !opts.interactive || opts.confirm == nil {
				return fmt.Errorf(
					"refusing to delete %s with %w — re-run interactively, or move .env aside first",
					envPath, errNoTerminal)
			}
			if !confirm(fmt.Sprintf("Delete %s anyway?", envPath)) {
				fmt.Fprintf(out, "\n  Kept %s.\n", opts.appDir)
				return nil
			}
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("could not check for %s: %w", envPath, err)
		}

		if err := os.RemoveAll(opts.appDir); err != nil {
			return fmt.Errorf("could not remove %s: %w", opts.appDir, err)
		}
		fmt.Fprintf(out, "  %s  removed %s\n", paintIcon("ok", color), opts.appDir)
	}

	fmt.Fprintf(out, "\n%s telos uninstalled\n\n", greenText("✔", color))
	fmt.Fprintln(out, dimText("Container volumes and your storage directory were not touched —", color))
	fmt.Fprintln(out, dimText("they are node data. To destroy them: telos stop --volumes, and", color))
	fmt.Fprintln(out, dimText("delete STORAGE_PATH by hand.", color))
	return nil
}
