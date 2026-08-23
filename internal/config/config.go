package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Site struct {
	Hostname          string      `yaml:"hostname"`
	PhysicalInterface string      `yaml:"physical_interface"`
	StatusListen      string      `yaml:"status_listen"`
	VLANs             VLANs       `yaml:"vlans"`
	MgmtDHCP          MgmtDHCP    `yaml:"mgmt_dhcp"`
	ManagementAccess  []string    `yaml:"management_access"`
	AllowlistFiles    []string    `yaml:"allowlist_files"`
	DenyPrefixes      []string    `yaml:"deny_multicast_prefixes"`
	DanteUnicastUDP   []int       `yaml:"dante_unicast_udp_ports"`
	Allowlists        []Allowlist `yaml:"-"`
	ConfigDir         string      `yaml:"-"`
}

type VLANs struct {
	Mgmt    VLAN `yaml:"mgmt"`
	Control VLAN `yaml:"control"`
	Dante   VLAN `yaml:"dante"`
}

type VLAN struct {
	ID            int    `yaml:"id"`
	Address       string `yaml:"address"`
	Prefix        int    `yaml:"prefix"`
	InterfaceName string `yaml:"interface_name"`
	// Untagged puts L3 on the physical NIC (native/PVID). At most one VLAN may
	// be untagged: a switch port has exactly one PVID. On an "audio trunk" port
	// that is dante (PVID 201) with control tagged.
	Untagged bool `yaml:"untagged"`
	// Gateway/DNS are lab conveniences for a Mgmt VLAN that has an uplink.
	// Production Mgmt is isolated and needs neither. Only allowed on mgmt.
	Gateway string   `yaml:"gateway"`
	DNS     []string `yaml:"dns"`
}

func (v VLAN) Iface(phys string) string {
	if v.Untagged {
		return phys
	}
	if v.InterfaceName != "" {
		return v.InterfaceName
	}
	return fmt.Sprintf("%s.%d", phys, v.ID)
}

func (v VLAN) Network() (*net.IPNet, error) {
	ip := net.ParseIP(v.Address)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("not an IPv4 address: %q", v.Address)
	}
	mask := net.CIDRMask(v.Prefix, 32)
	return &net.IPNet{IP: ip.Mask(mask), Mask: mask}, nil
}

type MgmtDHCP struct {
	// Enabled defaults to false when omitted. Combiner DHCP is off unless
	// an optional Mgmt VLAN is present and this is set true.
	Enabled    *bool  `yaml:"enabled"`
	RangeStart string `yaml:"range_start"`
	RangeEnd   string `yaml:"range_end"`
	Lease      string `yaml:"lease"`
	Domain     string `yaml:"domain"`
}

// IsEnabled reports whether Mgmt DHCP/dnsmasq should run (default false).
func (d MgmtDHCP) IsEnabled() bool {
	if d.Enabled == nil {
		return false
	}
	return *d.Enabled
}

type Allowlist struct {
	Name   string       `yaml:"name"`
	VLAN   string       `yaml:"vlan"` // dante only
	Groups []AllowGroup `yaml:"groups"`
}

type AllowGroup struct {
	Name      string   `yaml:"name"`
	Address   string   `yaml:"address"`
	Addresses []string `yaml:"addresses"`
	Port      int      `yaml:"port"`
	PortEnd   int      `yaml:"port_end"`
	Ports     []int    `yaml:"ports"`
	Proto     string   `yaml:"proto"`
	Direction string   `yaml:"direction"` // both|to-control|from-control (to-mgmt/from-mgmt are aliases)
	Notes     string   `yaml:"notes"`
}

// Endpoint is one (address, port) membership after expanding a group.
type Endpoint struct {
	Address string
	Port    int
}

func (g AllowGroup) PortMax() int {
	if g.PortEnd > g.Port {
		return g.PortEnd
	}
	return g.Port
}

// ResolvedAddresses returns the group's address list (plural or singular form).
func (g AllowGroup) ResolvedAddresses() []string {
	if len(g.Addresses) > 0 {
		return append([]string(nil), g.Addresses...)
	}
	if g.Address != "" {
		return []string{g.Address}
	}
	return nil
}

// ResolvedPorts returns the group's port list (ports, or port…port_end).
func (g AllowGroup) ResolvedPorts() []int {
	if len(g.Ports) > 0 {
		return append([]int(nil), g.Ports...)
	}
	if g.Port < 1 {
		return nil
	}
	max := g.PortMax()
	out := make([]int, 0, max-g.Port+1)
	for p := g.Port; p <= max; p++ {
		out = append(out, p)
	}
	return out
}

// Endpoints returns the cartesian product of resolved addresses × ports.
func (g AllowGroup) Endpoints() []Endpoint {
	addrs := g.ResolvedAddresses()
	ports := g.ResolvedPorts()
	out := make([]Endpoint, 0, len(addrs)*len(ports))
	for _, a := range addrs {
		for _, p := range ports {
			out = append(out, Endpoint{Address: a, Port: p})
		}
	}
	return out
}

// strictUnmarshal decodes YAML with KnownFields, so an unrecognized or
// misspelled key is an error instead of a silently ignored one. A typo like
// `untaged: true` would otherwise yield a valid-looking config with the wrong
// topology — tagged where the port is native, or vice versa.
func strictUnmarshal(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("empty config")
		}
		return err
	}
	return nil
}

func LoadSite(path string) (*Site, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Site
	if err := strictUnmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.ConfigDir = filepath.Dir(path)
	if s.StatusListen == "" {
		s.StatusListen = ":8080"
	}
	if s.Hostname == "" {
		s.Hostname = "combiner"
	}
	normalized, err := normalizeDenyPrefixes(s.DenyPrefixes)
	if err != nil {
		return nil, err
	}
	s.DenyPrefixes = normalized
	if err := s.validate(); err != nil {
		return nil, err
	}
	for _, rel := range s.AllowlistFiles {
		p := rel
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.ConfigDir, rel)
		}
		al, err := loadAllowlist(p)
		if err != nil {
			return nil, fmt.Errorf("allowlist %s: %w", p, err)
		}
		s.Allowlists = append(s.Allowlists, *al)
	}
	return &s, nil
}

// normalizeDenyPrefixes dedupes and canonicalizes site.yaml deny_multicast_prefixes.
// That list is the sole source of truth for reflector and nftables deny rules.
func normalizeDenyPrefixes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("deny_multicast_prefixes required (non-empty)")
	}
	seen := map[string]struct{}{}
	var out []string
	for _, p := range raw {
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			ip := net.ParseIP(p)
			if ip == nil || ip.To4() == nil {
				return nil, fmt.Errorf("deny_multicast_prefixes: bad prefix %q", p)
			}
			_, n, err = net.ParseCIDR(ip.String() + "/32")
			if err != nil {
				return nil, fmt.Errorf("deny_multicast_prefixes: bad prefix %q", p)
			}
		}
		if n.IP.To4() == nil {
			return nil, fmt.Errorf("deny_multicast_prefixes: not IPv4 %q", p)
		}
		if !n.IP.IsMulticast() {
			return nil, fmt.Errorf("deny_multicast_prefixes: not multicast %q", p)
		}
		key := n.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out, nil
}

func loadAllowlist(path string) (*Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var al Allowlist
	if err := strictUnmarshal(data, &al); err != nil {
		return nil, err
	}
	role := strings.ToLower(al.VLAN)
	if role != "dante" {
		return nil, fmt.Errorf("vlan must be dante (Control-native protocols are not reflected), got %q", al.VLAN)
	}
	al.VLAN = role
	for i := range al.Groups {
		g := &al.Groups[i]
		if g.Proto == "" {
			g.Proto = "udp"
		}
		if g.Direction == "" {
			g.Direction = "both"
		}
		switch g.Direction {
		case "to-mgmt":
			g.Direction = "to-control"
		case "from-mgmt":
			g.Direction = "from-control"
		}
		switch g.Direction {
		case "both", "to-control", "from-control":
		default:
			return nil, fmt.Errorf("group %s: bad direction %q", g.Name, g.Direction)
		}
		if !strings.EqualFold(g.Proto, "udp") {
			return nil, fmt.Errorf("group %s: only udp supported", g.Name)
		}
		if err := validateAllowGroupAddresses(g); err != nil {
			return nil, err
		}
		if err := validateAllowGroupPorts(g); err != nil {
			return nil, err
		}
	}
	return &al, nil
}

func validateAllowGroupAddresses(g *AllowGroup) error {
	hasSingular := g.Address != ""
	hasPlural := len(g.Addresses) > 0
	switch {
	case hasSingular && hasPlural:
		return fmt.Errorf("group %s: set address or addresses, not both", g.Name)
	case !hasSingular && !hasPlural:
		return fmt.Errorf("group %s: address or addresses required", g.Name)
	}
	for _, addr := range g.ResolvedAddresses() {
		ip := net.ParseIP(addr)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("group %s: bad address %q", g.Name, addr)
		}
		if !ip.IsMulticast() {
			return fmt.Errorf("group %s: address %q is not multicast", g.Name, addr)
		}
	}
	return nil
}

func validateAllowGroupPorts(g *AllowGroup) error {
	hasPorts := len(g.Ports) > 0
	hasPort := g.Port != 0
	hasPortEnd := g.PortEnd != 0
	if hasPorts && (hasPort || hasPortEnd) {
		return fmt.Errorf("group %s: set ports or port/port_end, not both", g.Name)
	}
	if hasPorts {
		for _, p := range g.Ports {
			if p < 1 || p > 65535 {
				return fmt.Errorf("group %s: bad port %d", g.Name, p)
			}
		}
		return nil
	}
	if hasPortEnd && !hasPort {
		return fmt.Errorf("group %s: port required when port_end is set", g.Name)
	}
	if !hasPort {
		return fmt.Errorf("group %s: port or ports required", g.Name)
	}
	if g.Port < 1 || g.Port > 65535 {
		return fmt.Errorf("group %s: bad port %d", g.Name, g.Port)
	}
	if hasPortEnd && (g.PortEnd < g.Port || g.PortEnd > 65535) {
		return fmt.Errorf("group %s: bad port_end %d", g.Name, g.PortEnd)
	}
	return nil
}

func (v VLAN) Configured() bool {
	return v.ID != 0 || v.Address != "" || v.Untagged || v.InterfaceName != "" || v.Gateway != "" || len(v.DNS) > 0
}

func (s *Site) HasMgmt() bool {
	return s.VLANs.Mgmt.Configured()
}

func (s *Site) validate() error {
	if s.PhysicalInterface == "" {
		return fmt.Errorf("physical_interface required")
	}
	vlans := []struct {
		name     string
		v        VLAN
		required bool
	}{
		{"mgmt", s.VLANs.Mgmt, false},
		{"control", s.VLANs.Control, true},
		{"dante", s.VLANs.Dante, true},
	}
	ids := map[int]string{}
	ifaces := map[string]string{}
	untaggedRole := ""
	var nets []*net.IPNet
	for _, item := range vlans {
		v := item.v
		if !v.Configured() {
			if item.required {
				return fmt.Errorf("%s: vlan required", item.name)
			}
			continue
		}
		if v.ID < 1 || v.ID > 4094 {
			return fmt.Errorf("%s: invalid vlan id %d", item.name, v.ID)
		}
		if v.Address == "" {
			return fmt.Errorf("%s: address required", item.name)
		}
		if v.Untagged {
			// A switch port has exactly one PVID, so only one VLAN can be native.
			if untaggedRole != "" {
				return fmt.Errorf("%s: untagged already set on %s (a port has one PVID)", item.name, untaggedRole)
			}
			untaggedRole = item.name
			if v.InterfaceName != "" && v.InterfaceName != s.PhysicalInterface {
				return fmt.Errorf("%s: untagged interface_name must be omitted or equal physical_interface %q", item.name, s.PhysicalInterface)
			}
		}
		// A tagged VLAN named after the NIC would make networkd treat the real
		// link as a VLAN netdev and stop managing it.
		if !v.Untagged && v.Iface(s.PhysicalInterface) == s.PhysicalInterface {
			return fmt.Errorf("%s: interface_name must differ from physical_interface %q (set untagged: true on the native/PVID VLAN)", item.name, s.PhysicalInterface)
		}
		if item.name != "mgmt" {
			if v.Gateway != "" {
				return fmt.Errorf("%s: gateway is only allowed on mgmt", item.name)
			}
			if len(v.DNS) > 0 {
				return fmt.Errorf("%s: dns is only allowed on mgmt", item.name)
			}
		}
		for _, d := range v.DNS {
			if ip := net.ParseIP(d); ip == nil || ip.To4() == nil {
				return fmt.Errorf("%s: dns %q must be IPv4", item.name, d)
			}
		}
		if prev, ok := ids[v.ID]; ok {
			return fmt.Errorf("duplicate vlan id %d (%s and %s)", v.ID, prev, item.name)
		}
		ids[v.ID] = item.name

		ip := net.ParseIP(v.Address)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("%s: address must be IPv4, got %q", item.name, v.Address)
		}
		if v.Prefix < 8 || v.Prefix > 30 {
			return fmt.Errorf("%s: invalid prefix %d", item.name, v.Prefix)
		}
		n, err := v.Network()
		if err != nil {
			return fmt.Errorf("%s: %w", item.name, err)
		}
		if !n.Contains(ip) {
			return fmt.Errorf("%s: address %s outside prefix /%d", item.name, v.Address, v.Prefix)
		}
		for _, other := range nets {
			if n.Contains(other.IP) || other.Contains(n.IP) {
				return fmt.Errorf("%s: subnet %s overlaps another VLAN", item.name, n)
			}
		}
		nets = append(nets, n)

		ifi := v.Iface(s.PhysicalInterface)
		if prev, ok := ifaces[ifi]; ok {
			return fmt.Errorf("duplicate interface %s (%s and %s)", ifi, prev, item.name)
		}
		ifaces[ifi] = item.name
	}

	if s.HasMgmt() {
		if gw := s.VLANs.Mgmt.Gateway; gw != "" {
			ip := net.ParseIP(gw)
			if ip == nil || ip.To4() == nil {
				return fmt.Errorf("mgmt: gateway %q must be IPv4", gw)
			}
			mgmtNet, err := s.VLANs.Mgmt.Network()
			if err != nil {
				return err
			}
			if !mgmtNet.Contains(ip) {
				return fmt.Errorf("mgmt: gateway %s outside mgmt subnet %s", gw, mgmtNet)
			}
		}
	}

	if err := s.validateManagementAccess(); err != nil {
		return err
	}

	if !s.MgmtDHCP.IsEnabled() {
		return nil
	}
	if !s.HasMgmt() {
		return fmt.Errorf("mgmt_dhcp.enabled requires vlans.mgmt")
	}
	start := net.ParseIP(s.MgmtDHCP.RangeStart)
	end := net.ParseIP(s.MgmtDHCP.RangeEnd)
	if start == nil || end == nil || start.To4() == nil || end.To4() == nil {
		return fmt.Errorf("mgmt_dhcp range must be IPv4 when enabled")
	}
	mgmtNet, _ := s.VLANs.Mgmt.Network()
	if !mgmtNet.Contains(start) || !mgmtNet.Contains(end) {
		return fmt.Errorf("mgmt_dhcp range must be inside mgmt subnet")
	}
	return nil
}

// PeerIface returns the production-side interface for an allowlist vlan role.
func (s *Site) PeerIface(role string) (string, error) {
	phys := s.PhysicalInterface
	switch strings.ToLower(role) {
	case "dante":
		return s.VLANs.Dante.Iface(phys), nil
	default:
		return "", fmt.Errorf("unknown vlan role %q (want dante)", role)
	}
}

func (s *Site) ClientIface() string {
	return s.VLANs.Control.Iface(s.PhysicalInterface)
}

func (s *Site) MgmtIface() string {
	if !s.HasMgmt() {
		return ""
	}
	return s.VLANs.Mgmt.Iface(s.PhysicalInterface)
}

// managementRoles is the set of VLAN roles that may reach SSH/status (22, 8080).
var managementRoles = map[string]struct{}{"control": {}, "dante": {}, "mgmt": {}}

// validateManagementAccess normalizes management_access and rejects unknown or
// unconfigured roles. Consumed by generate-nftables.py; validated here so
// `combiner -check` catches a typo before install rewrites the ruleset.
func (s *Site) validateManagementAccess() error {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(s.ManagementAccess))
	for _, raw := range s.ManagementAccess {
		role := strings.ToLower(strings.TrimSpace(raw))
		if _, ok := managementRoles[role]; !ok {
			return fmt.Errorf("management_access: unknown role %q (want control|dante|mgmt)", raw)
		}
		if role == "mgmt" && !s.HasMgmt() {
			return fmt.Errorf("management_access: mgmt listed but vlans.mgmt is not configured")
		}
		if _, ok := seen[role]; ok {
			return fmt.Errorf("management_access: duplicate role %q", raw)
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	s.ManagementAccess = out
	return nil
}

// ManagementRoles returns the VLAN roles allowed to reach SSH and the status
// page. An omitted management_access defaults to control, plus mgmt when one is
// configured — the historical behavior. An explicit list is authoritative, so a
// site that names [control, dante] on a box with mgmt loses SSH via mgmt.
func (s *Site) ManagementRoles() []string {
	if len(s.ManagementAccess) > 0 {
		return append([]string(nil), s.ManagementAccess...)
	}
	out := []string{"control"}
	if s.HasMgmt() {
		out = append(out, "mgmt")
	}
	return out
}

// ManagementIfaces maps ManagementRoles onto interface names.
func (s *Site) ManagementIfaces() []string {
	phys := s.PhysicalInterface
	var out []string
	for _, role := range s.ManagementRoles() {
		var name string
		switch role {
		case "control":
			name = s.VLANs.Control.Iface(phys)
		case "dante":
			name = s.VLANs.Dante.Iface(phys)
		case "mgmt":
			name = s.MgmtIface()
		}
		if name == "" {
			continue
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// Denied reports whether ip falls in site deny_multicast_prefixes.
func (s *Site) Denied(ip net.IP) bool {
	if ip == nil {
		return true
	}
	for _, p := range s.DenyPrefixes {
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func init() {
	_, n, err := net.ParseCIDR("239.255.0.0/16")
	if err != nil {
		panic(err)
	}
	atpMediaNet = n
}

// ATP media (Audinate). Same /16 as Shure Discovery 239.255.254.253 — match UDP 4321 only.
var atpMediaNet *net.IPNet

const atpMediaPort = 4321

// DeniedUDP is the reflector/nftables floor: site prefixes plus ATP (239.255.0.0/16 UDP 4321).
func (s *Site) DeniedUDP(ip net.IP, port int) bool {
	if s.Denied(ip) {
		return true
	}
	if ip != nil && port == atpMediaPort && atpMediaNet.Contains(ip) {
		return true
	}
	return false
}
