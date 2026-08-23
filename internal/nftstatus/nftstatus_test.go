package nftstatus

import (
	"strings"
	"testing"
)

func TestParseTextIndented(t *testing.T) {
	sample := `table inet combiner {
	counter drop_ptp {
		packets 4211 bytes 379000
	}
	counter drop_deny_mcast {
		packets 12 bytes 900
	}
	counter snat_to_dante {
		packets 100 bytes 8000
	}
}
`
	c := parseText(sample)
	if c.DropPTP != 4211 {
		t.Fatalf("drop_ptp=%d", c.DropPTP)
	}
	if c.DropDenyMcast != 12 {
		t.Fatalf("drop_deny_mcast=%d", c.DropDenyMcast)
	}
	if c.SNATToDante != 100 {
		t.Fatalf("snat_to_dante=%d", c.SNATToDante)
	}
}

func TestParseTextNamed(t *testing.T) {
	sample := `iifname "eth0.20" counter name drop_ptp packets 9 bytes 90 drop`
	c := parseText(sample)
	if c.DropPTP != 9 {
		t.Fatalf("drop_ptp=%d", c.DropPTP)
	}
}

// Read falls back through three nft invocations; the first error is nil when
// that call succeeded but returned unparseable JSON. joinErr must tolerate that
// rather than dereferencing it.
func TestJoinErrToleratesNilFirstError(t *testing.T) {
	got := joinErr("bad json", nil, errTest{"scrape failed"})
	if got == "" {
		t.Fatal("expected a diagnostic")
	}
	if !strings.Contains(got, "scrape failed") {
		t.Errorf("missing underlying error: %q", got)
	}
}

func TestJoinErrAlwaysReturnsSomething(t *testing.T) {
	if got := joinErr("", nil); got == "" {
		t.Error("expected a non-empty fallback diagnostic")
	}
}

type errTest struct{ s string }

func (e errTest) Error() string { return e.s }
