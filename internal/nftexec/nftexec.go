// Package nftexec resolves the nft binary and builds commands against it.
//
// nft lives in /usr/sbin, which is absent from a non-root login PATH on Debian.
// Looking it up by bare name therefore succeeds for the (root) service and
// fails for an operator running combiner-status by hand — the exact moment the
// counters matter most. Resolve explicitly so both paths behave the same.
package nftexec

import (
	"errors"
	"os"
	"os/exec"
	"sync"
)

// sbinFallbacks are searched when PATH lookup fails, covering non-merged-usr
// layouts as well as Debian/Raspberry Pi OS.
var sbinFallbacks = []string{"/usr/sbin/nft", "/sbin/nft", "/usr/local/sbin/nft"}

var (
	once       sync.Once
	resolved   string
	resolveErr error
)

// ErrNotFound reports that no nft binary could be located.
var ErrNotFound = errors.New("nft not found in PATH or /usr/sbin, /sbin, /usr/local/sbin")

func resolve() (string, error) {
	once.Do(func() {
		if p, err := exec.LookPath("nft"); err == nil {
			resolved = p
			return
		}
		for _, p := range sbinFallbacks {
			if isExecutable(p) {
				resolved = p
				return
			}
		}
		resolveErr = ErrNotFound
	})
	return resolved, resolveErr
}

// Path returns the resolved nft binary path.
func Path() (string, error) { return resolve() }

// Available reports whether an nft binary could be located at all. Callers use
// this to distinguish "not installed" (skip) from "ran and failed" (report).
func Available() bool {
	_, err := resolve()
	return err == nil
}

// Command builds an *exec.Cmd for nft with args. It returns an error only when
// no nft binary exists, so callers never silently treat a missing binary as a
// zeroed result.
func Command(args ...string) (*exec.Cmd, error) {
	p, err := resolve()
	if err != nil {
		return nil, err
	}
	return exec.Command(p, args...), nil
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode()&0o111 != 0
}
