package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FloorDenyPrefixes are always denied for reflection and should match nftables floor.
// 224.0.1.128/30 covers .128-.131; .132 is explicit so the full PTP set 129-132 is covered.
var FloorDenyPrefixes = []string{
	"224.0.1.128/30",
	"224.0.1.132/32",
	"239.255.0.0/16",
	"239.69.0.0/16",
	"239.254.3.3/32",
}

type Site struct {
	Hostname          string   `yaml:"hostname"`
	PhysicalInterface string   `yaml:"physical_interface"`
	StatusListen      string   `yaml:"status_listen"`
	VLANs             VLANs    `yaml:"vlans"`
	MgmtDHCP          MgmtDHCP `yaml:"mgmt_dhcp"`
	AllowlistFiles    []string `yaml:"allowlist_files"`
	DenyPrefixes      []string `yaml:"deny_multicast_prefixes"`
	DanteUnicastUDP   []int    `yaml:"dante_unicast_udp_ports"`
	Allowlists        []Allowlist
	ConfigDir         string `yaml:"-"`
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
	// Untagged puts L3 on the physical NIC (native/PVID). Only allowed on mgmt.
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
	// Enabled defaults to true when omitted. Set false to skip dnsmasq
	// (lab on a shared Mgmt LAN that already has DHCP).
	Enabled    *bool  `yaml:"enabled"`
	RangeStart string `yaml:"range_start"`
	RangeEnd   string `yaml:"range_end"`
	Lease      string `yaml:"lease"`
	Domain     string `yaml:"domain"`
}

// IsEnabled reports whether Mgmt DHCP/dnsmasq should run (default true).
func (d MgmtDHCP) IsEnabled() bool {
	if d.Enabled == nil {
		return true
	}
	return *d.Enabled
}

type Allowlist struct {
	Name   string       `yaml:"name"`
	VLAN   string       `yaml:"vlan"` // control|dante only
	Groups []AllowGroup `yaml:"groups"`
}

type AllowGroup struct {
	Name      string `yaml:"name"`
	Address   string `yaml:"address"`
	Port      int    `yaml:"port"`
	PortEnd   int    `yaml:"port_end"`
	Proto     string `yaml:"proto"`
	Direction string `yaml:"direction"` // both|to-mgmt|from-mgmt
	Notes     string `yaml:"notes"`
}

func (g AllowGroup) PortMax() int {
	if g.PortEnd > g.Port {
		return g.PortEnd
	}
	return g.Port
}

func LoadSite(path string) (*Site, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Site
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	s.ConfigDir = filepath.Dir(path)
	if s.StatusListen == "" {
		s.StatusListen = ":8080"
	}
	if s.Hostname == "" {
		s.Hostname = "combiner"
	}
	s.DenyPrefixes = mergeDenyPrefixes(s.DenyPrefixes)
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

func mergeDenyPrefixes(extra []string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			ip := net.ParseIP(p)
			if ip == nil {
				return
			}
			_, n, err = net.ParseCIDR(ip.String() + "/32")
			if err != nil {
				return
			}
		}
		key := n.String()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	for _, p := range FloorDenyPrefixes {
		add(p)
	}
	for _, p := range extra {
		add(p)
	}
	return out
}

func loadAllowlist(path string) (*Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var al Allowlist
	if err := yaml.Unmarshal(data, &al); err != nil {
		return nil, err
	}
	role := strings.ToLower(al.VLAN)
	if role != "control" && role != "dante" {
		return nil, fmt.Errorf("vlan must be control or dante, got %q", al.VLAN)
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
		case "both", "to-mgmt", "from-mgmt":
		default:
			return nil, fmt.Errorf("group %s: bad direction %q", g.Name, g.Direction)
		}
		ip := net.ParseIP(g.Address)
		if ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("group %s: bad address %q", g.Name, g.Address)
		}
		if !ip.IsMulticast() {
			return nil, fmt.Errorf("group %s: address %q is not multicast", g.Name, g.Address)
		}
		if !strings.EqualFold(g.Proto, "udp") {
			return nil, fmt.Errorf("group %s: only udp supported", g.Name)
		}
		if g.Port < 1 || g.Port > 65535 {
			return nil, fmt.Errorf("group %s: bad port %d", g.Name, g.Port)
		}
		if g.PortEnd != 0 && (g.PortEnd < g.Port || g.PortEnd > 65535) {
			return nil, fmt.Errorf("group %s: bad port_end %d", g.Name, g.PortEnd)
		}
	}
	return &al, nil
}

func (s *Site) validate() error {
	if s.PhysicalInterface == "" {
		return fmt.Errorf("physical_interface required")
	}
	vlans := []struct {
		name string
		v    VLAN
	}{
		{"mgmt", s.VLANs.Mgmt},
		{"control", s.VLANs.Control},
		{"dante", s.VLANs.Dante},
	}
	ids := map[int]string{}
	ifaces := map[string]string{}
	var nets []*net.IPNet
	for _, item := range vlans {
		v := item.v
		if v.Untagged && item.name != "mgmt" {
			return fmt.Errorf("%s: untagged is only allowed on mgmt", item.name)
		}
		if v.Untagged && v.InterfaceName != "" && v.InterfaceName != s.PhysicalInterface {
			return fmt.Errorf("mgmt: untagged interface_name must be omitted or equal physical_interface %q", s.PhysicalInterface)
		}
		// A tagged VLAN named after the NIC would make networkd treat the real
		// link as a VLAN netdev and stop managing it.
		if !v.Untagged && v.Iface(s.PhysicalInterface) == s.PhysicalInterface {
			return fmt.Errorf("%s: interface_name must differ from physical_interface %q (use untagged: true on mgmt for a native VLAN)", item.name, s.PhysicalInterface)
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
		if v.ID < 1 || v.ID > 4094 {
			return fmt.Errorf("%s: invalid vlan id %d", item.name, v.ID)
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

	if !s.MgmtDHCP.IsEnabled() {
		return nil
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
	case "control":
		return s.VLANs.Control.Iface(phys), nil
	case "dante":
		return s.VLANs.Dante.Iface(phys), nil
	default:
		return "", fmt.Errorf("unknown vlan role %q (want control|dante)", role)
	}
}

func (s *Site) MgmtIface() string {
	return s.VLANs.Mgmt.Iface(s.PhysicalInterface)
}

// Denied reports whether ip falls in the compiled deny floor (+ site extras).
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
