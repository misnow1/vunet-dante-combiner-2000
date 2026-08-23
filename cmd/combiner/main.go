package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/msnow/vunet-dante-combiner-2000/internal/buildinfo"
	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
	"github.com/msnow/vunet-dante-combiner-2000/internal/inventory"
	"github.com/msnow/vunet-dante-combiner-2000/internal/netinfo"
	"github.com/msnow/vunet-dante-combiner-2000/internal/nftverify"
	"github.com/msnow/vunet-dante-combiner-2000/internal/reflector"
	"github.com/msnow/vunet-dante-combiner-2000/internal/statushttp"
)

// preflight prints what the combiner would run with. Config and allowlist
// errors have already exited non-zero by this point; a missing interface is
// reported as a warning because -check is also run on laptops, where the VLAN
// devices legitimately do not exist.
func preflight(out *os.File, path string, site *config.Site, ref *reflector.Service) (ok bool) {
	fmt.Fprintf(out, "combiner %s\n", buildinfo.String())
	fmt.Fprintf(out, "config OK: %s\n\n", path)

	type row struct {
		role, iface, vlan, addr string
	}
	rows := []row{}
	add := func(role string, v config.VLAN) {
		tag := fmt.Sprintf("%d tagged", v.ID)
		if v.Untagged {
			tag = fmt.Sprintf("%d untagged/PVID", v.ID)
		}
		rows = append(rows, row{role, v.Iface(site.PhysicalInterface), tag, fmt.Sprintf("%s/%d", v.Address, v.Prefix)})
	}
	if site.HasMgmt() {
		add("mgmt", site.VLANs.Mgmt)
	}
	add("control", site.VLANs.Control)
	add("dante", site.VLANs.Dante)

	var missing []string
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tINTERFACE\tVLAN\tADDRESS\tLINK")
	for _, r := range rows {
		st := netinfo.Describe(r.role, r.iface)
		link := "up"
		switch {
		case st.Error != "":
			link = "MISSING"
			missing = append(missing, fmt.Sprintf("%s (%s)", r.iface, r.role))
		case !st.Up:
			link = "down"
		case !st.HasAddr:
			link = "up, no address"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.role, r.iface, r.vlan, r.addr, link)
	}
	_ = w.Flush()

	ports := map[int]struct{}{}
	names := make([]string, 0, len(site.Allowlists))
	for _, al := range site.Allowlists {
		names = append(names, al.Name)
		for _, g := range al.Groups {
			for _, ep := range g.Endpoints() {
				ports[ep.Port] = struct{}{}
			}
		}
	}

	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "status listen\t%s\n", site.StatusListen)
	fmt.Fprintf(w, "management access\t%s\n", strings.Join(site.ManagementIfaces(), ", "))
	fmt.Fprintf(w, "deny prefixes\t%d\n", len(site.DenyPrefixes))
	fmt.Fprintf(w, "allowlists\t%d (%s)\n", len(site.Allowlists), strings.Join(names, ", "))
	fmt.Fprintf(w, "reflector\t%d group memberships on %d udp ports\n", ref.Stats().Groups, len(ports))
	_ = w.Flush()

	// Comparing the loaded ruleset against the config is the whole point of a
	// preflight: validating site.yaml in isolation cannot see that the kernel
	// is still enforcing rules generated from an older copy of it.
	fmt.Fprintln(out)
	drift := nftverify.Verify(site)
	switch {
	case drift.Skipped != "":
		fmt.Fprintf(out, "nftables drift   skipped (%s)\n", drift.Skipped)
	case drift.OK():
		fmt.Fprintf(out, "nftables drift   none (%s)\n", strings.Join(drift.Checked, ", "))
	default:
		fmt.Fprintln(out, "nftables drift   MISMATCH")
		for _, p := range drift.Problems {
			fmt.Fprintf(out, "  - %s\n", p)
		}
		fmt.Fprintln(out, "  the loaded ruleset disagrees with site.yaml; traffic follows the")
		fmt.Fprintln(out, "  ruleset, not the YAML. Regenerate and reload:")
		fmt.Fprintf(out, "    sudo python3 deploy/pi/generate-nftables.py %s /etc/nftables.conf\n", path)
		fmt.Fprintln(out, "    sudo nft -f /etc/nftables.conf && sudo conntrack -F")
	}

	if ref.Stats().Groups == 0 {
		fmt.Fprintln(out, "\nWARNING: no multicast groups allowlisted — discovery reflection will be idle")
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "\nWARNING: interface(s) not present: %s\n", strings.Join(missing, ", "))
		fmt.Fprintln(out, "         combiner will exit at startup until systemd-networkd creates them")
	}

	// Missing interfaces stay a warning (-check runs on laptops too); only a
	// ruleset that contradicts the config fails the preflight.
	return drift.OK()
}

// shellQuote renders s as a single-quoted shell word. install.sh eval's this
// output, so Go's %q is not good enough — it escapes for Go, not for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printSiteFacts emits the site.yaml values install.sh needs, as shell
// assignments. Quoting is safe because the loader has already validated these
// as hostnames, interface names, and integers.
func printSiteFacts(out io.Writer, site *config.Site) {
	enabled := "0"
	if site.MgmtDHCP.IsEnabled() {
		enabled = "1"
	}
	hostname := site.Hostname
	if hostname == "" {
		hostname = "combiner"
	}

	fmt.Fprintf(out, "COMBINER_HOSTNAME=%s\n", shellQuote(hostname))
	fmt.Fprintf(out, "COMBINER_PHYSICAL_INTERFACE=%s\n", shellQuote(site.PhysicalInterface))
	fmt.Fprintf(out, "COMBINER_MGMT_DHCP_ENABLED=%s\n", shellQuote(enabled))
	fmt.Fprintf(out, "COMBINER_MGMT_DNS_COUNT=%s\n", shellQuote(strconv.Itoa(len(site.VLANs.Mgmt.DNS))))
}

func main() {
	cfgPath := flag.String("config", "/etc/combiner/site.yaml", "path to site.yaml")
	checkOnly := flag.Bool("check", false, "validate config, report a preflight summary, and exit")
	printFacts := flag.Bool("print-facts", false, "print shell-sourceable COMBINER_* facts from site.yaml and exit")
	showVersion := flag.Bool("version", false, "print the build version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	site, err := config.LoadSite(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// install.sh sources this instead of parsing site.yaml with python3-yaml —
	// a module the installer is itself responsible for installing. Emitted
	// before the reflector is built so it works on a box with no interfaces.
	if *printFacts {
		printSiteFacts(os.Stdout, site)
		return
	}

	inv := inventory.New()
	// reflector.New cross-checks allowlists against the deny floor, so building
	// it is part of validation — not just startup.
	ref, err := reflector.New(site, inv)
	if err != nil {
		log.Fatalf("reflector: %v", err)
	}

	if *checkOnly {
		// Exit non-zero on drift so install scripts and CI treat a stale
		// ruleset as the failure it is.
		if !preflight(os.Stdout, *cfgPath, site, ref) {
			os.Exit(1)
		}
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		// Reflector errors are degraded (logged); status page stays up.
		if err := ref.Run(ctx); err != nil {
			log.Printf("reflector stopped: %v", err)
		}
	}()

	srv := &statushttp.Server{Site: site, Inv: inv, Ref: ref}
	httpSrv := &http.Server{
		Addr:              site.StatusListen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	go func() {
		log.Printf("status listening on %s", site.StatusListen)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
}
