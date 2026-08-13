package config_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
)

func TestLoadExampleSite(t *testing.T) {
	root := filepath.Join("..", "..", "config", "site.example.yaml")
	site, err := config.LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	if site.Hostname != "combiner" {
		t.Fatalf("hostname %q", site.Hostname)
	}
	if len(site.Allowlists) != 3 {
		t.Fatalf("allowlists %d", len(site.Allowlists))
	}
	var danteGroups int
	for _, al := range site.Allowlists {
		if al.Name == "dante" {
			danteGroups = len(al.Groups)
		}
	}
	if danteGroups < 1 {
		t.Fatal("expected dante groups")
	}
	if site.MgmtIface() != "eth0.209" {
		t.Fatalf("mgmt iface %s", site.MgmtIface())
	}
	if !site.Denied(net.ParseIP("224.0.1.129")) {
		t.Fatal("expected 224.0.1.129 denied")
	}
	if !site.Denied(net.ParseIP("224.0.1.132")) {
		t.Fatal("expected 224.0.1.132 denied")
	}
	if !site.Denied(net.ParseIP("239.255.1.1")) {
		t.Fatal("expected media prefix denied")
	}
	if site.Denied(net.ParseIP("224.0.0.251")) {
		t.Fatal("mDNS must not be denied")
	}
}

func TestRejectDuplicateVLAN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.1, prefix: 24}
  control: {id: 10, address: 10.20.0.1, prefix: 24}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp: {range_start: 10.10.0.100, range_end: 10.10.0.110, lease: 1h}
allowlist_files: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSite(path); err == nil {
		t.Fatal("expected duplicate vlan id error")
	}
}
