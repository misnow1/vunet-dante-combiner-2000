package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
)

func loadTestSite(t *testing.T, path string) *config.Site {
	t.Helper()
	site, err := config.LoadSite(path)
	if err != nil {
		t.Fatalf("LoadSite(%s): %v", path, err)
	}
	return site
}

func captureFacts(t *testing.T, site *config.Site) string {
	t.Helper()
	var buf bytes.Buffer
	printSiteFacts(&buf, site)
	return buf.String()
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"combiner", `'combiner'`},
		{"eth0", `'eth0'`},
		{"", `''`},
		{"it's", `'it'\''s'`},
		{"a b", `'a b'`},
		{"$(rm -rf /)", `'$(rm -rf /)'`},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// install.sh eval's -print-facts output, so a value that survives Go quoting but
// not shell quoting would execute. Round-trip through a real shell to prove it
// does not.
func TestShellQuoteSurvivesRealShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}
	for _, in := range []string{"combiner", "it's", "$(echo pwned)", "`echo pwned`", `a"b`, "x;y"} {
		script := "V=" + shellQuote(in) + `; printf %s "$V"`
		out, err := exec.Command(sh, "-c", script).Output()
		if err != nil {
			t.Fatalf("sh failed for %q: %v", in, err)
		}
		if string(out) != in {
			t.Errorf("round-trip of %q gave %q", in, string(out))
		}
	}
}

func TestPrintSiteFactsEmitsAllKeys(t *testing.T) {
	site := loadTestSite(t, "../../config/site.example.yaml")
	got := captureFacts(t, site)

	for _, key := range []string{
		"COMBINER_HOSTNAME=",
		"COMBINER_PHYSICAL_INTERFACE=",
		"COMBINER_MGMT_DHCP_ENABLED=",
		"COMBINER_MGMT_DNS_COUNT=",
	} {
		if !strings.Contains(got, key) {
			t.Errorf("missing %s in:\n%s", key, got)
		}
	}
	// install.sh compares this against the string "1"/"0", not a boolean.
	if !strings.Contains(got, "COMBINER_MGMT_DHCP_ENABLED='0'") {
		t.Errorf("expected dhcp disabled for the default profile, got:\n%s", got)
	}
}
