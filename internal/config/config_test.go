package config_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
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
	if site.MgmtIface() != "" {
		t.Fatalf("production example must omit mgmt, got %s", site.MgmtIface())
	}
	if site.ClientIface() != "eth0.200" {
		t.Fatalf("client iface %s", site.ClientIface())
	}
	if site.MgmtDHCP.IsEnabled() {
		t.Fatal("example site should leave DHCP disabled")
	}
	var names []string
	for _, al := range site.Allowlists {
		names = append(names, al.Name)
	}
	if strings.Join(names, ",") != "dante,shure,lake" {
		t.Fatalf("allowlist names %v", names)
	}
	if !site.Denied(net.ParseIP("224.0.1.129")) {
		t.Fatal("expected 224.0.1.129 denied")
	}
	if !site.Denied(net.ParseIP("224.0.1.132")) {
		t.Fatal("expected 224.0.1.132 denied")
	}
    if !site.Denied(net.ParseIP("239.69.1.1")) {
		t.Fatal("expected AES67 prefix denied")
	}
	if site.Denied(net.ParseIP("239.255.1.1")) {
		t.Fatal("ATP prefix must not deny all of 239.255.0.0/16 (Shure Discovery lives there)")
	}
	if site.Denied(net.ParseIP("224.0.0.251")) {
		t.Fatal("mDNS must not be denied")
	}
	if site.DeniedUDP(net.ParseIP("239.255.1.1"), 4321) == false {
		t.Fatal("ATP 239.255.0.0/16 UDP 4321 must be denied")
	}
	if site.DeniedUDP(net.ParseIP("239.255.254.253"), 8427) {
		t.Fatal("Shure discovery UDP 8427 must be reflectable")
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
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
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
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
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

func TestMgmtDHCPDisabledByDefault(t *testing.T) {
	root := filepath.Join("..", "..", "config", "site.example.yaml")
	site, err := config.LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	if site.MgmtDHCP.IsEnabled() {
		t.Fatal("example site should leave DHCP disabled")
	}
}

func TestLoadLabFlatOptionalMgmt(t *testing.T) {
	root := filepath.Join("..", "..", "config", "site.lab-flat.example.yaml")
	site, err := config.LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	if !site.HasMgmt() {
		t.Fatal("lab-flat should configure mgmt")
	}
	if site.MgmtIface() != "eth0" {
		t.Fatalf("mgmt iface %s", site.MgmtIface())
	}
}

func TestRejectAllowlistVLANControl(t *testing.T) {
	dir := t.TempDir()
	path := writeSiteWithAllowlist(t, dir, `
name: t
vlan: control
groups:
  - name: mdns
    address: 224.0.0.251
    port: 5353
`)
	if _, err := config.LoadSite(path); err == nil {
		t.Fatal("expected control vlan allowlist error")
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
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
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
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
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
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSite(path); err == nil {
		t.Fatal("expected untagged control error")
	}
}

func writeSiteWithAllowlist(t *testing.T, dir, allowYAML string) string {
	t.Helper()
	alPath := filepath.Join(dir, "groups.yaml")
	if err := os.WriteFile(alPath, []byte(allowYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	sitePath := filepath.Join(dir, "site.yaml")
	site := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.1, prefix: 24}
  control: {id: 20, address: 10.20.0.1, prefix: 24}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp: {enabled: false}
allowlist_files: [groups.yaml]
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
`
	if err := os.WriteFile(sitePath, []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	return sitePath
}

func TestAllowGroupScalarEndpoints(t *testing.T) {
	dir := t.TempDir()
	path := writeSiteWithAllowlist(t, dir, `
name: t
vlan: dante
groups:
  - name: mdns
    address: 224.0.0.251
    port: 5353
`)
	site, err := config.LoadSite(path)
	if err != nil {
		t.Fatal(err)
	}
	eps := site.Allowlists[0].Groups[0].Endpoints()
	if len(eps) != 1 || eps[0].Address != "224.0.0.251" || eps[0].Port != 5353 {
		t.Fatalf("endpoints %#v", eps)
	}
}

func TestAllowGroupAddressesPortRangeCartesian(t *testing.T) {
	dir := t.TempDir()
	path := writeSiteWithAllowlist(t, dir, `
name: t
vlan: dante
groups:
  - name: dante-control
    addresses:
      - 224.0.0.230
      - 224.0.0.231
      - 224.0.0.232
      - 224.0.0.233
    port: 8700
    port_end: 8708
`)
	site, err := config.LoadSite(path)
	if err != nil {
		t.Fatal(err)
	}
	g := site.Allowlists[0].Groups[0]
	eps := g.Endpoints()
	if len(eps) != 36 {
		t.Fatalf("want 36 endpoints, got %d", len(eps))
	}
	if eps[0].Address != "224.0.0.230" || eps[0].Port != 8700 {
		t.Fatalf("first %#v", eps[0])
	}
	if eps[8].Address != "224.0.0.230" || eps[8].Port != 8708 {
		t.Fatalf("ninth %#v", eps[8])
	}
	if eps[9].Address != "224.0.0.231" || eps[9].Port != 8700 {
		t.Fatalf("tenth %#v", eps[9])
	}
	if eps[35].Address != "224.0.0.233" || eps[35].Port != 8708 {
		t.Fatalf("last %#v", eps[35])
	}
}

func TestAllowGroupAddressesAndPortsList(t *testing.T) {
	dir := t.TempDir()
	path := writeSiteWithAllowlist(t, dir, `
name: t
vlan: dante
groups:
  - name: ctrl
    addresses: [224.0.0.230, 224.0.0.231]
    ports: [8700, 8701]
`)
	site, err := config.LoadSite(path)
	if err != nil {
		t.Fatal(err)
	}
	eps := site.Allowlists[0].Groups[0].Endpoints()
	if len(eps) != 4 {
		t.Fatalf("want 4 endpoints, got %d", len(eps))
	}
}

func TestAllowGroupValidationRejects(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "address and addresses",
			yaml: `
name: t
vlan: dante
groups:
  - name: g
    address: 224.0.0.251
    addresses: [224.0.0.230]
    port: 5353
`,
		},
		{
			name: "ports and port",
			yaml: `
name: t
vlan: dante
groups:
  - name: g
    address: 224.0.0.251
    port: 5353
    ports: [5353]
`,
		},
		{
			name: "ports and port_end",
			yaml: `
name: t
vlan: dante
groups:
  - name: g
    address: 224.0.0.251
    port_end: 5354
    ports: [5353]
`,
		},
		{
			name: "port_end without port",
			yaml: `
name: t
vlan: dante
groups:
  - name: g
    address: 224.0.0.251
    port_end: 5354
`,
		},
		{
			name: "neither address form",
			yaml: `
name: t
vlan: dante
groups:
  - name: g
    port: 5353
`,
		},
		{
			name: "neither port form",
			yaml: `
name: t
vlan: dante
groups:
  - name: g
    address: 224.0.0.251
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSiteWithAllowlist(t, dir, tc.yaml)
			if _, err := config.LoadSite(path); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadExampleDanteControlExpanded(t *testing.T) {
	root := filepath.Join("..", "..", "config", "site.example.yaml")
	site, err := config.LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	var ctrl *config.AllowGroup
	for _, al := range site.Allowlists {
		if al.Name != "dante" {
			continue
		}
		for i := range al.Groups {
			if al.Groups[i].Name == "dante-control" {
				ctrl = &al.Groups[i]
			}
		}
	}
	if ctrl == nil {
		t.Fatal("expected dante-control group")
	}
	if n := len(ctrl.Endpoints()); n != 36 {
		t.Fatalf("dante-control endpoints want 36 got %d", n)
	}
}

func TestRejectEmptyDenyPrefixes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.1, prefix: 24}
  control: {id: 20, address: 10.20.0.1, prefix: 24}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSite(path); err == nil {
		t.Fatal("expected empty deny_multicast_prefixes error")
	}
}

func TestRejectMissingDenyPrefixes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.1, prefix: 24}
  control: {id: 20, address: 10.20.0.1, prefix: 24}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp: {enabled: false}
allowlist_files: []
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadSite(path); err == nil {
		t.Fatal("expected missing deny_multicast_prefixes error")
	}
}

func TestDenyPrefixesDedupes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 10, address: 10.10.0.1, prefix: 24}
  control: {id: 20, address: 10.20.0.1, prefix: 24}
  dante: {id: 30, address: 10.30.0.1, prefix: 24}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes:
  - 239.255.0.0/16
  - 239.255.0.0/16
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	site, err := config.LoadSite(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.DenyPrefixes) != 1 {
		t.Fatalf("want 1 deny prefix after dedupe, got %v", site.DenyPrefixes)
	}
}
