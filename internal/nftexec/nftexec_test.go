package nftexec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isExecutable(plain) {
		t.Error("non-executable file reported as executable")
	}

	bin := filepath.Join(dir, "bin")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutable(bin) {
		t.Error("executable file reported as non-executable")
	}

	if isExecutable(dir) {
		t.Error("directory reported as executable")
	}
	if isExecutable(filepath.Join(dir, "absent")) {
		t.Error("missing file reported as executable")
	}
}

// Command must fail loudly when nft is absent so callers never mistake a
// missing binary for a ruleset full of zeroes.
func TestCommandSurfacesMissingBinary(t *testing.T) {
	if Available() {
		t.Skip("nft present on this host")
	}
	if _, err := Command("list", "ruleset"); err == nil {
		t.Fatal("expected an error when nft is unavailable")
	}
}
