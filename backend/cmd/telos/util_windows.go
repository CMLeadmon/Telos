//go:build windows

package main

import (
	"os"
	"os/user"
)

func isWritable(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil {
		return false
	}
	return fi.Mode().Perm()&0200 != 0
}

func getCurrentUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "user"
}

func getCurrentUidGid() (string, string) {
	return "1000", "1000"
}
