package main

import (
	"fmt"
	"io"
	"os"
)

type commandInfo struct {
	name    string
	summary string
}

var registeredCommands = []commandInfo{
	{name: "doctor", summary: "Diagnose host, config, and node problems"},
	{name: "init", summary: "Generate .env and create the storage tree"},
	{name: "start", summary: "Bring the stack up"},
	{name: "status", summary: "Report boot state for every service"},
	{name: "stop", summary: "Shut the stack down"},
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "telos — operate a Telos node")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  telos <command> [options]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	for _, cmd := range registeredCommands {
		fmt.Fprintf(out, "  %-12s %s\n", cmd.name, cmd.summary)
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Global options:")
	fmt.Fprintln(out, "  -h, --help     Show help for telos or for a command")
	fmt.Fprintln(out, "      --dev      Layer docker-compose.dev.yml (binds host ports for local work)")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Environment:")
	fmt.Fprintln(out, "  TELOS_DEV=1        same as --dev")
	fmt.Fprintln(out, "  TELOS_HEALTH_URL   gateway health endpoint")
	fmt.Fprintln(out, "                     (default http://127.0.0.1:8080/api/v1/health)")
	fmt.Fprintln(out, "  NO_COLOR           disable colored output")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Exit codes:")
	fmt.Fprintln(out, "  0 success   1 failure   2 telos could not run")
}

func printUnknownCommand(errOut io.Writer, cmd string) {
	fmt.Fprintf(errOut, "telos: unknown command '%s'\n\n", cmd)
	fmt.Fprintln(errOut, "Available commands:")
	for _, c := range registeredCommands {
		fmt.Fprintf(errOut, "  %-12s %s\n", c.name, c.summary)
	}
}

func dispatch(args []string, stdout, stderr io.Writer) int {
	filteredArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--dev" {
			os.Setenv("TELOS_DEV", "1")
		} else {
			filteredArgs = append(filteredArgs, arg)
		}
	}

	if len(filteredArgs) == 0 {
		printUsage(stdout)
		return 0
	}

	cmd := filteredArgs[0]

	switch cmd {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "doctor":
		return runDoctor(filteredArgs[1:], stdout, stderr)
	case "status":
		return runStatus(filteredArgs[1:], stdout, stderr)
	case "init", "start", "stop":
		fmt.Fprintf(stderr, "telos: command '%s' not yet implemented\n", cmd)
		return 1
	default:
		printUnknownCommand(stderr, cmd)
		return 2
	}
}
