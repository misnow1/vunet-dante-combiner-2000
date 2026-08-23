package nftverify

import (
	"strings"
	"testing"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
)

// audioTrunk mirrors config/site.example.yaml: dante untagged on the physical
// NIC (PVID 201), control tagged on eth0.200.
func audioTrunk() *config.Site {
	return &config.Site{
		PhysicalInterface: "eth0",
		VLANs: config.VLANs{
			Control: config.VLAN{ID: 200, Address: "10.200.0.1", Prefix: 21, InterfaceName: "eth0.200"},
			Dante:   config.VLAN{ID: 201, Address: "10.201.0.1", Prefix: 21, Untagged: true},
		},
	}
}

// loaded is nft's own rendering of the ruleset, which differs from the
// generated file: it quotes counter names and writes "snat ip to".
func loaded(snatAddr string) string {
	return `table inet combiner {
	chain forward {
		type filter hook forward priority filter; policy drop;
		iifname "eth0.200" oifname "eth0" ip daddr != 224.0.0.0/4 accept
	}
	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		oifname "eth0" ip saddr != ` + snatAddr + ` counter name "snat_to_dante" snat ip to ` + snatAddr + `
	}
}`
}

func TestCleanRulesetHasNoDrift(t *testing.T) {
	r := compare(loaded("10.201.0.1"), audioTrunk())
	if !r.OK() {
		t.Fatalf("expected no drift, got %v", r.Problems)
	}
	if len(r.Checked) == 0 {
		t.Fatal("expected checked invariants to be reported")
	}
}

// TestStaleSNATAddressIsDetected reproduces the outage this package exists to
// catch: site.yaml was switched to the audio-trunk profile but the ruleset was
// still the one generated from an older flat-lab config, so every Control->Dante
// reply was addressed to 192.168.33.212 — a host on no local subnet.
func TestStaleSNATAddressIsDetected(t *testing.T) {
	r := compare(loaded("192.168.33.212"), audioTrunk())
	if r.OK() {
		t.Fatal("stale SNAT address was not detected")
	}
	joined := strings.Join(r.Problems, "\n")
	for _, want := range []string{"snat_to_dante", "192.168.33.212", "10.201.0.1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problem text missing %q; got:\n%s", want, joined)
		}
	}
}

// The generated file writes "snat to"; nft renders "snat ip to". Both must parse
// or the check would report drift against a correct ruleset.
func TestGeneratedFileSyntaxParses(t *testing.T) {
	generated := `  chain postrouting {
    oifname "eth0" ip saddr != 10.201.0.1 counter name snat_to_dante snat to 10.201.0.1
  }
  iifname "eth0.200" oifname "eth0" accept`
	if r := compare(generated, audioTrunk()); !r.OK() {
		t.Fatalf("generated-file syntax reported drift: %v", r.Problems)
	}
}

func TestMissingSNATRuleIsDetected(t *testing.T) {
	ruleset := `table inet combiner {
	chain forward { iifname "eth0.200" oifname "eth0" accept }
	chain postrouting { type nat hook postrouting priority srcnat; policy accept; }
}`
	r := compare(ruleset, audioTrunk())
	if r.OK() {
		t.Fatal("missing snat_to_dante rule was not detected")
	}
	if !strings.Contains(strings.Join(r.Problems, "\n"), "missing") {
		t.Errorf("expected a 'missing' problem, got %v", r.Problems)
	}
}

// A ruleset carrying snat_to_control while site.yaml has no Mgmt VLAN was built
// from a different profile entirely.
func TestUnexpectedControlSNATIsDetected(t *testing.T) {
	ruleset := loaded("10.201.0.1") + `
		oifname "eth0.200" ip saddr != 10.200.0.1 counter name "snat_to_control" snat ip to 10.200.0.1`
	r := compare(ruleset, audioTrunk())
	if r.OK() {
		t.Fatal("unexpected snat_to_control was not detected")
	}
	if !strings.Contains(strings.Join(r.Problems, "\n"), "snat_to_control") {
		t.Errorf("expected snat_to_control problem, got %v", r.Problems)
	}
}

// A VLAN retag leaves rules pointing at an interface that no longer carries the
// traffic, which the SNAT check alone would miss.
func TestStaleInterfaceNameIsDetected(t *testing.T) {
	site := audioTrunk()
	site.VLANs.Control.InterfaceName = "eth0.300"
	r := compare(loaded("10.201.0.1"), site)
	if r.OK() {
		t.Fatal("stale control interface name was not detected")
	}
	if !strings.Contains(strings.Join(r.Problems, "\n"), "eth0.300") {
		t.Errorf("expected eth0.300 problem, got %v", r.Problems)
	}
}
