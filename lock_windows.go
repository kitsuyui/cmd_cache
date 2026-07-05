//go:build windows

package main

import (
	"errors"
	"os"
)

// flockExclusive returns an error on Windows because syscall.Flock is not
// available. The caller falls back to running without a lock.
func flockExclusive(f *os.File) error {
	return errors.ErrUnsupported
}

// flockUnlock is a no-op on Windows.
func flockUnlock(f *os.File) error {
	return nil
}
