package main

import (
	"os"
)

func main() {
	code := dispatch(os.Args[1:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}
