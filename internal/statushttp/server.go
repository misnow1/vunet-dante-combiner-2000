package statushttp

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
	"github.com/msnow/vunet-dante-combiner-2000/internal/inventory"
	"github.com/msnow/vunet-dante-combiner-2000/internal/netinfo"
	"github.com/msnow/vunet-dante-combiner-2000/internal/nftstatus"
	"github.com/msnow/vunet-dante-combiner-2000/internal/reflector"
)

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
	DHCPLeases []string              `json:"dhcp_leases"`
	Allowlists []allowlistSummary    `json:"allowlists"`
	Notes      []string              `json:"notes"`
}

type allowlistSummary struct {
	Name   string   `json:"name"`
	VLAN   string   `json:"vlan"`
	Groups []string `json:"groups"`
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
	var als []allowlistSummary
	for _, al := range site.Allowlists {
		sum := allowlistSummary{Name: al.Name, VLAN: al.VLAN}
		for _, g := range al.Groups {
			sum.Groups = append(sum.Groups, g.Name+" "+g.Address)
		}
		als = append(als, sum)
	}
	notes := []string{
		"Non-zero drop_ptp / drop_deny_mcast / drop_forward_mcast while Dante is live are healthy.",
		"Control↔Dante drops should stay ~0; if rising, something is trying to hairpin through the combiner.",
		"Multicast never kernel-forwards; only the allowlisted reflector crosses VLANs.",
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
		DHCPLeases: netinfo.DHCPLeases(
			"/var/lib/misc/dnsmasq.leases",
			"/var/lib/dnsmasq/dnsmasq.leases",
		),
		Allowlists: als,
		Notes:      notes,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Snapshot())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})
	return mux
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>Combiner Status</title>
<style>
  :root { --bg:#0f1419; --fg:#e7ecf1; --muted:#9aa7b5; --ok:#3dd68c; --bad:#f31260; --card:#1a2332; --line:#2a3545; }
  * { box-sizing: border-box; }
  body { margin:0; font-family: "IBM Plex Sans", "Segoe UI", sans-serif; background: var(--bg); color: var(--fg); }
  header { padding: 1.25rem 1.5rem; border-bottom: 1px solid var(--line); display:flex; justify-content:space-between; align-items:baseline; gap:1rem; flex-wrap:wrap; }
  h1 { margin:0; font-size:1.25rem; letter-spacing:0.02em; }
  .sub { color: var(--muted); font-size:0.9rem; }
  main { padding: 1.25rem 1.5rem 3rem; display:grid; gap:1rem; }
  .grid { display:grid; gap:1rem; grid-template-columns: repeat(auto-fit,minmax(240px,1fr)); }
  section { background: var(--card); border:1px solid var(--line); border-radius: 10px; padding:1rem; }
  h2 { margin:0 0 0.75rem; font-size:0.95rem; text-transform:uppercase; letter-spacing:0.06em; color:var(--muted); }
  table { width:100%; border-collapse: collapse; font-size:0.9rem; }
  th, td { text-align:left; padding:0.4rem 0.35rem; border-bottom:1px solid var(--line); vertical-align:top; }
  th { color:var(--muted); font-weight:600; }
  .pill { display:inline-block; padding:0.1rem 0.45rem; border-radius:999px; font-size:0.75rem; }
  .up { background:#143528; color:var(--ok); }
  .down { background:#3a1520; color:var(--bad); }
  .metric { font-variant-numeric: tabular-nums; }
  .notes li { margin:0.35rem 0; color:var(--muted); }
  code { font-family: "IBM Plex Mono", ui-monospace, monospace; font-size:0.85em; }
</style>
</head>
<body>
<header>
  <div>
    <h1 id="title">Combiner</h1>
    <div class="sub" id="when"></div>
  </div>
  <div class="sub">Mgmt status · auto-refresh 5s · <a style="color:#7ab7ff" href="/api/status">/api/status</a></div>
</header>
<main>
  <div class="grid" id="ifaces"></div>
  <div class="grid">
    <section><h2>nftables counters</h2><div id="nft"></div></section>
    <section><h2>Reflector</h2><div id="ref"></div></section>
  </div>
  <section><h2>Discovered hosts</h2><div id="hosts"></div></section>
  <section><h2>DHCP leases (Mgmt)</h2><div id="leases"></div></section>
  <section><h2>Allowlists</h2><div id="allow"></div></section>
  <section><h2>Notes</h2><ul class="notes" id="notes"></ul></section>
</main>
<script>
function el(tag, text) {
  const n = document.createElement(tag);
  if (text != null) n.textContent = String(text);
  return n;
}
function row(table, k, v) {
  const tr = el('tr');
  tr.appendChild(el('td', k));
  tr.appendChild(el('td', v));
  table.appendChild(tr);
}
async function refresh() {
  const r = await fetch('/api/status');
  const d = await r.json();
  document.getElementById('title').textContent = (d.hostname || 'combiner') + ' status';
  document.getElementById('when').textContent = d.time || '';

  const ifaces = document.getElementById('ifaces');
  ifaces.replaceChildren();
  (d.interfaces || []).forEach(i => {
    const sec = el('section');
    sec.appendChild(el('h2', (i.role || '') + ' · ' + (i.name || '')));
    const pill = el('span', i.up ? 'up' : 'down');
    pill.className = 'pill ' + (i.up ? 'up' : 'down');
    sec.appendChild(pill);
    const addrs = el('div');
    addrs.className = 'sub';
    addrs.style.marginTop = '0.5rem';
    addrs.textContent = (i.addresses || []).join(', ');
    sec.appendChild(addrs);
    if (i.error) {
      const err = el('div', i.error);
      err.className = 'sub';
      sec.appendChild(err);
    }
    ifaces.appendChild(sec);
  });

  const nftWrap = document.getElementById('nft');
  nftWrap.replaceChildren();
  const nftTable = el('table');
  nftTable.className = 'metric';
  const n = d.nft || {};
  row(nftTable, 'drop_ptp (healthy if >0 on live Dante)', n.drop_ptp);
  row(nftTable, 'drop_deny_mcast', n.drop_deny_mcast);
  row(nftTable, 'drop_forward_mcast', n.drop_forward_mcast);
  row(nftTable, 'drop_control_dante', n.drop_control_dante);
  row(nftTable, 'snat_to_control', n.snat_to_control);
  row(nftTable, 'snat_to_dante', n.snat_to_dante);
  if (n.error) row(nftTable, 'error', n.error);
  nftWrap.appendChild(nftTable);

  const refWrap = document.getElementById('ref');
  refWrap.replaceChildren();
  const refTable = el('table');
  refTable.className = 'metric';
  const ref = d.reflector || {};
  row(refTable, 'groups', ref.groups);
  row(refTable, 'listeners_up', ref.listeners_up);
  row(refTable, 'listeners_fail', ref.listeners_fail);
  row(refTable, 'packets_in', ref.packets_in);
  row(refTable, 'packets_out', ref.packets_out);
  row(refTable, 'packets_drop', ref.packets_drop);
  row(refTable, 'last_packet', ref.last_packet || '—');
  row(refTable, 'last_error', ref.last_error || '—');
  refWrap.appendChild(refTable);

  const hostsWrap = document.getElementById('hosts');
  hostsWrap.replaceChildren();
  const hosts = d.hosts || [];
  if (!hosts.length) {
    const empty = el('div', 'No discovery sources seen yet.');
    empty.className = 'sub';
    hostsWrap.appendChild(empty);
  } else {
    const table = el('table');
    const head = el('tr');
    ['VLAN','IP','Group','Pkts','Last seen'].forEach(h => head.appendChild(el('th', h)));
    table.appendChild(head);
    hosts.forEach(h => {
      const tr = el('tr');
      tr.appendChild(el('td', h.vlan));
      const ip = el('td');
      const code = el('code', h.ip);
      ip.appendChild(code);
      tr.appendChild(ip);
      tr.appendChild(el('td', h.last_group));
      const pk = el('td', h.packet_count);
      pk.className = 'metric';
      tr.appendChild(pk);
      tr.appendChild(el('td', h.last_seen));
      table.appendChild(tr);
    });
    hostsWrap.appendChild(table);
  }

  const leasesWrap = document.getElementById('leases');
  leasesWrap.replaceChildren();
  const leases = d.dhcp_leases || [];
  if (!leases.length) {
    const empty = el('div', 'No dnsmasq lease file found (ok before first client).');
    empty.className = 'sub';
    leasesWrap.appendChild(empty);
  } else {
    const table = el('table');
    const head = el('tr');
    head.appendChild(el('th', 'Lease line'));
    table.appendChild(head);
    leases.forEach(l => {
      const tr = el('tr');
      const td = el('td');
      td.appendChild(el('code', l));
      tr.appendChild(td);
      table.appendChild(tr);
    });
    leasesWrap.appendChild(table);
  }

  const allowWrap = document.getElementById('allow');
  allowWrap.replaceChildren();
  (d.allowlists || []).forEach(a => {
    const div = el('div');
    div.style.marginBottom = '0.75rem';
    const strong = el('strong', a.name);
    div.appendChild(strong);
    div.appendChild(document.createTextNode(' → ' + (a.vlan || '')));
    const sub = el('div', (a.groups && a.groups.length) ? a.groups.join(', ') : '(empty — capture needed)');
    sub.className = 'sub';
    div.appendChild(sub);
    allowWrap.appendChild(div);
  });

  const notes = document.getElementById('notes');
  notes.replaceChildren();
  (d.notes || []).forEach(n => notes.appendChild(el('li', n)));
}
refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>
`
