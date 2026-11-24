//go:build unix

package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func lock(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, "weft.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another weft process is using %s", dir)
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
