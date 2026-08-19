package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
	"github.com/msnow/vunet-dante-combiner-2000/internal/netinfo"
	"github.com/msnow/vunet-dante-combiner-2000/internal/nftstatus"
)

func main() {
	cfgPath := flag.String("config", "/etc/combiner/site.yaml", "path to site.yaml")
	asJSON := flag.Bool("json", false, "emit JSON")
	flag.Parse()

	site, err := config.LoadSite(*cfgPath)
	if err != nil {
		// Still show nft/ifaces if config missing in weird states
		fmt.Fprintf(os.Stderr, "config warning: %v\n", err)
	}

	type snap struct {
		Interfaces []netinfo.IfaceStatus `json:"interfaces"`
		NFT        nftstatus.Counters    `json:"nft"`
		DHCPLeases []string              `json:"dhcp_leases"`
	}
	s := snap{
		NFT: nftstatus.Read(),
		DHCPLeases: netinfo.DHCPLeases(
			"/var/lib/misc/dnsmasq.leases",
			"/var/lib/dnsmasq/dnsmasq.leases",
		),
	}
	if site != nil {
		s.Interfaces = []netinfo.IfaceStatus{
			netinfo.Describe("control", site.VLANs.Control.Iface(site.PhysicalInterface)),
			netinfo.Describe("dante", site.VLANs.Dante.Iface(site.PhysicalInterface)),
		}
		if site.HasMgmt() {
			s.Interfaces = append([]netinfo.IfaceStatus{netinfo.Describe("mgmt", site.MgmtIface())}, s.Interfaces...)
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tIFACE\tUP\tADDRESSES")
	for _, i := range s.Interfaces {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\n", i.Role, i.Name, i.Up, join(i.Addresses))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "COUNTER\tPACKETS")
	fmt.Fprintf(w, "drop_ptp\t%d\n", s.NFT.DropPTP)
	fmt.Fprintf(w, "drop_deny_mcast\t%d\n", s.NFT.DropDenyMcast)
	fmt.Fprintf(w, "drop_forward_mcast\t%d\n", s.NFT.DropForwardMcast)
	fmt.Fprintf(w, "drop_ipv6_forward\t%d\n", s.NFT.DropIPv6Forward)
	fmt.Fprintf(w, "snat_to_control\t%d\n", s.NFT.SNATToControl)
	fmt.Fprintf(w, "snat_to_dante\t%d\n", s.NFT.SNATToDante)
	if s.NFT.Error != "" {
		fmt.Fprintf(w, "nft_error\t%s\n", s.NFT.Error)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "DHCP_LEASES")
	if len(s.DHCPLeases) == 0 {
		fmt.Fprintln(w, "(none)")
	}
	for _, l := range s.DHCPLeases {
		fmt.Fprintln(w, l)
	}
	_ = w.Flush()
}

func join(ss []string) string {
	if len(ss) == 0 {
		return "—"
	}
	out := ss[0]
	for i := 1; i < len(ss); i++ {
		out += ", " + ss[i]
	}
	return out
}
