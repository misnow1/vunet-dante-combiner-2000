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
	// Clients sit on Dante, so only VU-NET is reflected: Dante, Shure and Lake
	// are native to the client VLAN and must not be listed.
	if len(site.Allowlists) != 1 {
		t.Fatalf("allowlists %d", len(site.Allowlists))
	}
	var vunetGroups int
	for _, al := range site.Allowlists {
		if al.Name == "vunet" {
			vunetGroups = len(al.Groups)
		}
	}
	if vunetGroups < 1 {
		t.Fatal("expected vunet groups")
	}
	if site.MgmtIface() != "" {
		t.Fatalf("production example must omit mgmt, got %s", site.MgmtIface())
	}
	// Dante is both the PVID/untagged VLAN and the client VLAN.
	if site.ClientIface() != "eth0" {
		t.Fatalf("client iface %s", site.ClientIface())
	}
	if site.MgmtDHCP.IsEnabled() {
		t.Fatal("example site should leave DHCP disabled")
	}
	var names []string
	for _, al := range site.Allowlists {
		names = append(names, al.Name)
	}
	if strings.Join(names, ",") != "vunet" {
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

func TestUntaggedAllowedOnAnyRole(t *testing.T) {
	for _, role := range []string{"mgmt", "control", "dante"} {
		t.Run(role, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "site.yaml")
			v := map[string]string{
				"mgmt":    "{id: 10, address: 10.10.0.1, prefix: 24}",
				"control": "{id: 20, address: 10.20.0.1, prefix: 24}",
				"dante":   "{id: 30, address: 10.30.0.1, prefix: 24}",
			}
			v[role] = strings.TrimSuffix(v[role], "}") + ", untagged: true}"
			content := `
hostname: x
physical_interface: eth0
vlans:
  mgmt: ` + v["mgmt"] + `
  control: ` + v["control"] + `
  dante: ` + v["dante"] + `
mgmt_dhcp: {enabled: false}
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
			got := map[string]string{
				"mgmt":    site.MgmtIface(),
				"control": site.VLANs.Control.Iface("eth0"),
				"dante":   site.VLANs.Dante.Iface("eth0"),
			}
			if got[role] != "eth0" {
				t.Fatalf("untagged %s iface = %s, want eth0", role, got[role])
			}
			for other, name := range got {
				if other != role && name == "eth0" {
					t.Fatalf("tagged %s should not be on eth0", other)
				}
			}
		})
	}
}

// A switch port has exactly one PVID.
func TestRejectTwoUntaggedVLANs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  control: {id: 20, address: 10.20.0.1, prefix: 24, untagged: true}
  dante: {id: 30, address: 10.30.0.1, prefix: 24, untagged: true}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadSite(path)
	if err == nil {
		t.Fatal("expected one-PVID error")
	}
	if !strings.Contains(err.Error(), "one PVID") {
		t.Fatalf("error %v", err)
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

// The shipped profiles both put clients on Dante, so neither loads
// allowlists/dante.yaml. Exercise its expansion directly against the real file
// rather than losing the coverage.
func TestLoadExampleDanteControlExpanded(t *testing.T) {
	allow, err := filepath.Abs(filepath.Join("..", "..", "config", "allowlists", "dante.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  control: {id: 200, address: 10.200.0.1, prefix: 21, interface_name: eth0.200}
  dante: {id: 201, address: 10.201.0.1, prefix: 21, untagged: true}
mgmt_dhcp: {enabled: false}
allowlist_files: [` + allow + `]
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	site, err := config.LoadSite(path)
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

// The production example is an "audio trunk" port: Dante on the PVID
// (untagged) carrying the clients, Control tagged carrying the amps.
// Everything downstream keys off interface names.
func TestExampleSiteAudioTrunk(t *testing.T) {
	site, err := config.LoadSite(filepath.Join("..", "..", "config", "site.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := site.VLANs.Dante.Iface(site.PhysicalInterface); got != "eth0" {
		t.Fatalf("dante iface %s, want eth0 (untagged/PVID)", got)
	}
	if got := site.ClientIface(); got != "eth0" {
		t.Fatalf("client iface %s, want eth0 (clients live on Dante)", got)
	}
	if site.HasMgmt() {
		t.Fatal("audio-trunk example should not configure mgmt")
	}
	if got := site.PeerRole(); got != "control" {
		t.Fatalf("peer role %s, want control (the amps)", got)
	}
	peer, err := site.PeerIface("control")
	if err != nil {
		t.Fatal(err)
	}
	if peer != "eth0.200" {
		t.Fatalf("control peer iface %s, want eth0.200 (tagged)", peer)
	}
	if peer == site.ClientIface() {
		t.Fatal("peer iface must differ from client iface")
	}
}

// Both shipped profiles now set management_access explicitly (clients are on
// Dante, so Dante must reach SSH or the operator has no way in), so the DEFAULT
// needs a config of its own or the behaviour goes untested.
func TestManagementAccessDefaults(t *testing.T) {
	write := func(t *testing.T, body string) *config.Site {
		t.Helper()
		path := filepath.Join(t.TempDir(), "site.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		site, err := config.LoadSite(path)
		if err != nil {
			t.Fatal(err)
		}
		return site
	}

	// No mgmt VLAN: the default is Control alone.
	site := write(t, `
hostname: x
physical_interface: eth0
vlans:
  control: {id: 200, address: 10.200.0.1, prefix: 21, interface_name: eth0.200}
  dante: {id: 201, address: 10.201.0.1, prefix: 21, untagged: true}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
`)
	if got := site.ManagementRoles(); len(got) != 1 || got[0] != "control" {
		t.Fatalf("roles %v, want [control]", got)
	}
	if got := site.ManagementIfaces(); len(got) != 1 || got[0] != "eth0.200" {
		t.Fatalf("ifaces %v, want [eth0.200]", got)
	}

	// With a mgmt VLAN, an omitted list widens to Control + Mgmt.
	lab := write(t, `
hostname: x
physical_interface: eth0
vlans:
  mgmt: {id: 1, address: 192.168.1.2, prefix: 24, untagged: true}
  control: {id: 200, address: 10.200.0.1, prefix: 21, interface_name: eth0.200}
  dante: {id: 201, address: 10.201.0.1, prefix: 21, interface_name: eth0.201}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
`)
	if got := lab.ManagementIfaces(); len(got) != 2 || got[0] != "eth0.200" || got[1] != "eth0" {
		t.Fatalf("lab ifaces %v, want [eth0.200 eth0]", got)
	}
}

// Clients live on Dante in both shipped profiles, so Dante must be able to
// reach SSH and the status page — otherwise a field unit is console-only.
func TestShippedExamplesReachableFromTheClientVLAN(t *testing.T) {
	for _, name := range []string{"site.example.yaml", "site.lab-flat.example.yaml"} {
		t.Run(name, func(t *testing.T) {
			site, err := config.LoadSite(filepath.Join("..", "..", "config", name))
			if err != nil {
				t.Fatal(err)
			}
			if site.ClientRole() != "dante" {
				t.Fatalf("client role %s, want dante", site.ClientRole())
			}
			var ok bool
			for _, r := range site.ManagementRoles() {
				if r == site.ClientRole() {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("management_access %v does not include the client VLAN %q",
					site.ManagementRoles(), site.ClientRole())
			}
		})
	}
}

func TestManagementAccessExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
management_access: [Control, DANTE]
vlans:
  control: {id: 200, address: 10.200.0.1, prefix: 21}
  dante: {id: 201, address: 10.201.0.1, prefix: 21, untagged: true}
mgmt_dhcp: {enabled: false}
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
	got := site.ManagementIfaces()
	if len(got) != 2 || got[0] != "eth0.200" || got[1] != "eth0" {
		t.Fatalf("ifaces %v, want [eth0.200 eth0]", got)
	}
}

func TestManagementAccessRejects(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"unknown role", "[control, soundgrid]", "unknown role"},
		{"duplicate", "[control, control]", "duplicate role"},
		{"mgmt absent", "[mgmt]", "vlans.mgmt is not configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "site.yaml")
			content := `
hostname: x
physical_interface: eth0
management_access: ` + tc.value + `
vlans:
  control: {id: 200, address: 10.200.0.1, prefix: 21}
  dante: {id: 201, address: 10.201.0.1, prefix: 21, untagged: true}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes: [224.0.1.128/30, 224.0.1.132/32, 239.255.0.0/16]
`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := config.LoadSite(path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

// A misspelled key must fail loudly: `untaged: true` would otherwise produce a
// valid-looking config with the wrong tagging, and the service would come up
// on interfaces the switch port does not carry.
func TestRejectUnknownKeys(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "top level",
			yaml: "totally_bogus_key: yes\n",
			want: "totally_bogus_key",
		},
		{
			name: "near miss on management_access",
			yaml: "managment_access: [control]\n",
			want: "managment_access",
		},
	}
	base := `
hostname: x
physical_interface: eth0
vlans:
  control: {id: 200, address: 10.200.0.1, prefix: 21}
  dante: {id: 201, address: 10.201.0.1, prefix: 21, untagged: true}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes: [224.0.1.128/30]
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "site.yaml")
			if err := os.WriteFile(path, []byte(base+tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := config.LoadSite(path)
			if err == nil {
				t.Fatal("expected unknown-key error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestRejectUnknownVLANKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "site.yaml")
	content := `
hostname: x
physical_interface: eth0
vlans:
  control: {id: 200, address: 10.200.0.1, prefix: 21}
  dante: {id: 201, address: 10.201.0.1, prefix: 21, untaged: true}
mgmt_dhcp: {enabled: false}
allowlist_files: []
deny_multicast_prefixes: [224.0.1.128/30]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadSite(path)
	if err == nil {
		t.Fatal("expected error for misspelled untagged")
	}
	if !strings.Contains(err.Error(), "untaged") {
		t.Fatalf("error %v", err)
	}
}

func TestRejectUnknownAllowlistKey(t *testing.T) {
	dir := t.TempDir()
	// `adress` instead of `address` would otherwise leave the group with no
	// endpoints and silently reflect nothing.
	sitePath := writeSiteWithAllowlist(t, dir, `
name: bad
vlan: dante
groups:
  - name: g
    adress: 224.0.0.251
    port: 5353
`)
	_, err := config.LoadSite(sitePath)
	if err == nil {
		t.Fatal("expected error for misspelled allowlist key")
	}
	if !strings.Contains(err.Error(), "adress") {
		t.Fatalf("error %v", err)
	}
}
