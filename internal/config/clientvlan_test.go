package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSite(t *testing.T, body string, allowlists map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if len(allowlists) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, "allowlists"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range allowlists {
			if err := os.WriteFile(filepath.Join(dir, "allowlists", name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := filepath.Join(dir, "site.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const baseVLANs = `
physical_interface: eth0
vlans:
  control:
    id: 200
    address: 10.200.0.1
    prefix: 21
    interface_name: eth0.200
  dante:
    id: 201
    address: 10.201.0.1
    prefix: 21
    untagged: true
deny_multicast_prefixes:
  - 224.0.1.128/30
`

const vunetAllowlist = `
name: vunet
vlan: control
groups:
  - name: vunet-discovery
    address: 239.254.10.2
    ports: [6002, 54077]
    proto: udp
    direction: both
`

const danteAllowlist = `
name: dante
vlan: dante
groups:
  - name: mdns
    address: 224.0.0.251
    port: 5353
    proto: udp
    direction: both
`

// Omitting client_vlan must keep the historical control-client behavior.
func TestClientVLANDefaultsToControl(t *testing.T) {
	site, err := LoadSite(writeSite(t, baseVLANs, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := site.ClientRole(); got != "control" {
		t.Errorf("ClientRole = %q, want control", got)
	}
	if got := site.PeerRole(); got != "dante" {
		t.Errorf("PeerRole = %q, want dante", got)
	}
	if got := site.ClientIface(); got != "eth0.200" {
		t.Errorf("ClientIface = %q, want eth0.200", got)
	}
	if got := site.PeerAddress(); got != "10.201.0.1" {
		t.Errorf("PeerAddress = %q, want 10.201.0.1", got)
	}
}

func TestClientVLANDanteInvertsRoles(t *testing.T) {
	site, err := LoadSite(writeSite(t, "client_vlan: dante\n"+baseVLANs, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := site.ClientRole(); got != "dante" {
		t.Errorf("ClientRole = %q, want dante", got)
	}
	if got := site.PeerRole(); got != "control" {
		t.Errorf("PeerRole = %q, want control", got)
	}
	// dante is untagged, so the client lands on the physical NIC.
	if got := site.ClientIface(); got != "eth0" {
		t.Errorf("ClientIface = %q, want eth0", got)
	}
	if got := site.PeerAddress(); got != "10.200.0.1" {
		t.Errorf("PeerAddress = %q, want 10.200.0.1", got)
	}
}

func TestClientVLANRejectsUnknownRole(t *testing.T) {
	_, err := LoadSite(writeSite(t, "client_vlan: mgmt\n"+baseVLANs, nil))
	if err == nil {
		t.Fatal("expected mgmt to be rejected as a client_vlan")
	}
	if !strings.Contains(err.Error(), "client_vlan") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// A control-side allowlist is only legal once the client has moved to Dante.
func TestVunetAllowlistRequiresDanteClient(t *testing.T) {
	files := map[string]string{"vunet.yaml": vunetAllowlist}
	body := baseVLANs + "allowlist_files: [allowlists/vunet.yaml]\n"

	if _, err := LoadSite(writeSite(t, body, files)); err == nil {
		t.Fatal("control allowlist must be rejected under the default control-client profile")
	}

	site, err := LoadSite(writeSite(t, "client_vlan: dante\n"+body, files))
	if err != nil {
		t.Fatalf("control allowlist should load with client_vlan: dante: %v", err)
	}
	if len(site.Allowlists) != 1 || site.Allowlists[0].Name != "vunet" {
		t.Fatalf("unexpected allowlists: %+v", site.Allowlists)
	}
}

// The mirror case: a dante allowlist is meaningless once clients live on Dante.
func TestDanteAllowlistRejectedUnderDanteClient(t *testing.T) {
	files := map[string]string{"dante.yaml": danteAllowlist}
	body := "client_vlan: dante\n" + baseVLANs + "allowlist_files: [allowlists/dante.yaml]\n"
	_, err := LoadSite(writeSite(t, body, files))
	if err == nil {
		t.Fatal("dante allowlist must be rejected when dante is the client VLAN")
	}
	if !strings.Contains(err.Error(), "control") {
		t.Errorf("error should name the expected peer role, got: %v", err)
	}
}

func TestPeerIfaceRejectsClientRole(t *testing.T) {
	site, err := LoadSite(writeSite(t, "client_vlan: dante\n"+baseVLANs, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := site.PeerIface("dante"); err == nil {
		t.Fatal("dante must not be a legal peer when it is the client VLAN")
	}
	if _, err := site.PeerIface("control"); err != nil {
		t.Fatalf("control should be the peer: %v", err)
	}
}
