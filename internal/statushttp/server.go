package statushttp

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"time"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
	"github.com/msnow/vunet-dante-combiner-2000/internal/inventory"
	"github.com/msnow/vunet-dante-combiner-2000/internal/netinfo"
	"github.com/msnow/vunet-dante-combiner-2000/internal/nftstatus"
	"github.com/msnow/vunet-dante-combiner-2000/internal/reflector"
)

//go:embed static/*
var staticFS embed.FS

type Server struct {
	Site *config.Site
	Inv  *inventory.Store
	Ref  *reflector.Service
}

type payload struct {
	Hostname   string                `json:"hostname"`
	Time       time.Time             `json:"time"`
	Interfaces []netinfo.IfaceStatus `json:"interfaces"`
	NFT        nftstatus.Counters    `json:"nft"`
	Reflector  reflector.Stats       `json:"reflector"`
	Hosts      []inventory.Host      `json:"hosts"`
	Groups     []inventory.Group     `json:"groups"`
	DHCPLeases []string              `json:"dhcp_leases"`
	Allowlists []allowlistSummary    `json:"allowlists"`
	Notes      []string              `json:"notes"`
}

type allowlistSummary struct {
	Name   string                  `json:"name"`
	VLAN   string                  `json:"vlan"`
	Groups []allowlistGroupSummary `json:"groups"`
}

type allowlistGroupSummary struct {
	Key       string   `json:"key"` // allowlist/group — same string as hosts.last_group
	Name      string   `json:"name"`
	Address   string   `json:"address,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Port      int      `json:"port,omitempty"`
	PortEnd   int      `json:"port_end,omitempty"`
	Ports     []int    `json:"ports,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}

func (s *Server) Snapshot() payload {
	site := s.Site
	ifaces := []netinfo.IfaceStatus{
		netinfo.Describe("mgmt", site.MgmtIface()),
		netinfo.Describe("control", site.VLANs.Control.Iface(site.PhysicalInterface)),
		netinfo.Describe("dante", site.VLANs.Dante.Iface(site.PhysicalInterface)),
	}
	hosts := s.Inv.List()
	sort.Slice(hosts, func(i, j int) bool {
		if hosts[i].VLAN == hosts[j].VLAN {
			return hosts[i].IP < hosts[j].IP
		}
		return hosts[i].VLAN < hosts[j].VLAN
	})
	groups := s.Inv.ListGroups()
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].VLAN == groups[j].VLAN {
			if groups[i].Address == groups[j].Address {
				return groups[i].Port < groups[j].Port
			}
			return groups[i].Address < groups[j].Address
		}
		return groups[i].VLAN < groups[j].VLAN
	})
	var als []allowlistSummary
	for _, al := range site.Allowlists {
		sum := allowlistSummary{Name: al.Name, VLAN: al.VLAN}
		for _, g := range al.Groups {
			sum.Groups = append(sum.Groups, summarizeAllowGroup(al.Name, g))
		}
		als = append(als, sum)
	}
	notes := []string{
		"Non-zero drop_ptp / drop_deny_mcast / drop_forward_mcast while Dante is live are healthy.",
		"Control↔Dante drops should stay ~0; if rising, something is trying to hairpin through the combiner.",
		"Multicast never kernel-forwards; only the allowlisted reflector crosses VLANs.",
		"Joined groups are from allowlists (IGMP joins on Mgmt + Control/Dante). Observed groups appear once reflected traffic is seen.",
		"Mgmt has no DNS by design — open status via http://<mgmt-ip>:8080/",
		"VuNET/Lake discovery requires on-site capture into allowlist YAML.",
	}
	return payload{
		Hostname:   site.Hostname,
		Time:       time.Now().UTC(),
		Interfaces: ifaces,
		NFT:        nftstatus.Read(),
		Reflector:  s.Ref.Stats(),
		Hosts:      hosts,
		Groups:     groups,
		DHCPLeases: netinfo.DHCPLeases(
			"/var/lib/misc/dnsmasq.leases",
			"/var/lib/dnsmasq/dnsmasq.leases",
		),
		Allowlists: als,
		Notes:      notes,
	}
}

func summarizeAllowGroup(allowlist string, g config.AllowGroup) allowlistGroupSummary {
	out := allowlistGroupSummary{
		Key:   allowlist + "/" + g.Name,
		Name:  g.Name,
		Notes: g.Notes,
	}
	addrs := g.ResolvedAddresses()
	ports := g.ResolvedPorts()
	if len(g.Addresses) > 0 {
		out.Addresses = addrs
	} else if len(addrs) == 1 {
		out.Address = addrs[0]
	}
	if len(g.Ports) > 0 {
		out.Ports = ports
	} else if g.Port != 0 {
		out.Port = g.Port
		if g.PortEnd > g.Port {
			out.PortEnd = g.PortEnd
		}
	}
	return out
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Snapshot())
	})

	staticRoot, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("statushttp: embed static/: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(staticRoot))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		b, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b)
	})
	return mux
}
