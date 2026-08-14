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

func TestMgmtDHCPDisabledSkipsRangeValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.50, prefix: 24}
  control: {id: 20, address: 10.20.0.1, prefix: 24}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp:
  enabled: false
allowlist_files: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	site, err := config.LoadSite(path)
	if err != nil {
		t.Fatal(err)
	}
	if site.MgmtDHCP.IsEnabled() {
		t.Fatal("expected mgmt_dhcp disabled")
	}
}

func TestMgmtDHCPEnabledByDefault(t *testing.T) {
	root := filepath.Join("..", "..", "config", "site.example.yaml")
	site, err := config.LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	if !site.MgmtDHCP.IsEnabled() {
		t.Fatal("example site should leave DHCP enabled by default")
	}
}

func TestMgmtUntaggedUsesPhysicalIface(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 1, address: 192.168.1.2, prefix: 24, untagged: true}
  control: {id: 200, address: 10.200.0.1, prefix: 21}
  dante: {id: 201, address: 10.201.0.1, prefix: 21}
mgmt_dhcp:
  enabled: false
allowlist_files: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	site, err := config.LoadSite(path)
	if err != nil {
		t.Fatal(err)
	}
	if site.MgmtIface() != "eth0" {
		t.Fatalf("mgmt iface %s", site.MgmtIface())
	}
	if site.VLANs.Control.Iface("eth0") != "eth0.200" {
		t.Fatalf("control iface %s", site.VLANs.Control.Iface("eth0"))
	}
}

func TestRejectTaggedIfaceEqualPhysical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.1, prefix: 24, interface_name: eth0}
  control: {id: 20, address: 10.20.0.1, prefix: 24}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp: {enabled: false}
allowlist_files: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSite(path); err == nil {
		t.Fatal("expected error for tagged interface_name equal to physical_interface")
	}
}

func TestRejectUntaggedControl(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.1, prefix: 24}
  control: {id: 20, address: 10.20.0.1, prefix: 24, untagged: true}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp: {enabled: false}
allowlist_files: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSite(path); err == nil {
		t.Fatal("expected untagged control error")
	}
}
