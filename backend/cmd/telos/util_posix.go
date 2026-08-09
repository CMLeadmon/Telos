//go:build linux || freebsd || darwin

package main

import (
	"os/user"

	"golang.org/x/sys/unix"
)

func isWritable(dir string) bool {
	return unix.Access(dir, unix.W_OK) == nil
}

func getCurrentUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "user"
}

func getCurrentUidGid() (string, string) {
	if u, err := user.Current(); err == nil {
		return u.Uid, u.Gid
	}
	return "1000", "1000"
}
