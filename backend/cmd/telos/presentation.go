package main

import (
	"io"
	"os"
)

func useColor(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	// A character device is the portable answer to "is this a terminal", and it
	// needs no build tags or platform syscalls. The previous ioctl-based trio
	// hard-coded unix.TCGETS, which exists only on Linux, so the CLI stopped
	// cross-compiling for darwin and freebsd — the exact regression the GOOS
	// matrix is there to catch.
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func paintIcon(status string, color bool) string {
	if !color {
		switch status {
		case "ok":
			return "✔"
		case "bad":
			return "✘"
		case "warn":
			return "!"
		default:
			return "·"
		}
	}
	switch status {
	case "ok":
		return "\x1b[32m✔\x1b[0m"
	case "bad":
		return "\x1b[31m✘\x1b[0m"
	case "warn":
		return "\x1b[33m!\x1b[0m"
	default:
		return "\x1b[90m·\x1b[0m"
	}
}

func boldText(str string, color bool) string {
	if !color {
		return str
	}
	return "\x1b[1m" + str + "\x1b[0m"
}

func dimText(str string, color bool) string {
	if !color {
		return str
	}
	return "\x1b[90m" + str + "\x1b[0m"
}

func redText(str string, color bool) string {
	if !color {
		return str
	}
	return "\x1b[31m" + str + "\x1b[0m"
}

func greenText(str string, color bool) string {
	if !color {
		return str
	}
	return "\x1b[32m" + str + "\x1b[0m"
}
