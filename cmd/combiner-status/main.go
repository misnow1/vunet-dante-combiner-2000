package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/msnow/vunet-dante-combiner-2000/internal/buildinfo"
	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
	"github.com/msnow/vunet-dante-combiner-2000/internal/netinfo"
	"github.com/msnow/vunet-dante-combiner-2000/internal/nftstatus"
)

// provisioningHold reports whether the card still carries the marker that keeps
// combiner-apply from touching the network. Both paths, because /boot/firmware
// is only where newer Raspberry Pi OS mounts the FAT partition.
func provisioningHold() bool {
	for _, p := range []string{
		"/boot/firmware/combiner-provisioning",
		"/boot/combiner-provisioning",
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func main() {
	cfgPath := flag.String("config", "/etc/combiner/site.yaml", "path to site.yaml")
	asJSON := flag.Bool("json", false, "emit JSON")
	showVersion := flag.Bool("version", false, "print the build version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	site, err := config.LoadSite(*cfgPath)
	if err != nil {
		// Still show nft/ifaces if config missing in weird states
		fmt.Fprintf(os.Stderr, "config warning: %v\n", err)
	}

	type snap struct {
		Version    string                `json:"version"`
		Holding    bool                  `json:"provisioning_hold"`
		Interfaces []netinfo.IfaceStatus `json:"interfaces"`
		NFT        nftstatus.Counters    `json:"nft"`
		DHCPLeases []string              `json:"dhcp_leases"`
	}
	s := snap{
		Version: buildinfo.String(),
		Holding: provisioningHold(),
		NFT:     nftstatus.Read(),
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
	fmt.Fprintf(w, "combiner %s\n\n", s.Version)
	// A held unit has applied nothing, so every line below describes the bench
	// network it happens to have booted on rather than the config it carries.
	// Say so first, or the output reads as a broken unit.
	if s.Holding {
		fmt.Fprintf(w, "PROVISIONED — AWAITING GO-LIVE\n")
		fmt.Fprintf(w, "This unit has not applied its config. Run: sudo combiner-go-live\n\n")
	}
	fmt.Fprintln(w, "ROLE\tIFACE\tUP\tADDRESSES")
	for _, i := range s.Interfaces {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\n", i.Role, i.Name, i.Up, join(i.Addresses))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "COUNTER\tPACKETS")
	// When nft could not be read the counters are unknown, not zero. Printing
	// "0" next to an error reads as "nothing is being dropped", which is the
	// opposite of what an operator should conclude mid-outage.
	n := func(v uint64) string {
		if s.NFT.Error != "" {
			return "-"
		}
		return strconv.FormatUint(v, 10)
	}
	fmt.Fprintf(w, "drop_ptp\t%s\n", n(s.NFT.DropPTP))
	fmt.Fprintf(w, "drop_deny_mcast\t%s\n", n(s.NFT.DropDenyMcast))
	fmt.Fprintf(w, "drop_forward_mcast\t%s\n", n(s.NFT.DropForwardMcast))
	fmt.Fprintf(w, "drop_ipv6_forward\t%s\n", n(s.NFT.DropIPv6Forward))
	// The catch-all drop at the end of the forward chain: the first counter
	// worth looking at when traffic is not crossing VLANs.
	fmt.Fprintf(w, "drop_invalid_path\t%s\n", n(s.NFT.DropInvalidPath))
	fmt.Fprintf(w, "snat_to_control\t%s\n", n(s.NFT.SNATToControl))
	fmt.Fprintf(w, "snat_to_dante\t%s\n", n(s.NFT.SNATToDante))
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
