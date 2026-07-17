//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package main

import (
	"fmt"
	"os"
	"runtime"
)

func lockFile(*os.File) error {
	return fmt.Errorf("repository locking is unsupported on %s", runtime.GOOS)
}

func unlockFile(*os.File) error {
	return nil
}
