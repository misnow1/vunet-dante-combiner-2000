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
	Key     string `json:"key"` // allowlist/group — same string as hosts.last_group
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	PortEnd int    `json:"port_end,omitempty"`
	Notes   string `json:"notes,omitempty"`
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
			sum.Groups = append(sum.Groups, allowlistGroupSummary{
				Key:     al.Name + "/" + g.Name,
				Name:    g.Name,
				Address: g.Address,
				Port:    g.Port,
				PortEnd: g.PortEnd,
				Notes:   g.Notes,
			})
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
  h2 { margin:0 0 0.35rem; font-size:0.95rem; text-transform:uppercase; letter-spacing:0.06em; color:var(--muted); }
  .hint { color: var(--muted); font-size:0.8rem; margin:0 0 0.75rem; line-height:1.35; }
  table { width:100%; border-collapse: collapse; font-size:0.9rem; }
  th, td { text-align:left; padding:0.4rem 0.35rem; border-bottom:1px solid var(--line); vertical-align:top; }
  th { color:var(--muted); font-weight:600; }
  th[title], td[title], .tip { cursor: help; border-bottom: 1px dotted var(--muted); }
  .pill { display:inline-block; padding:0.1rem 0.45rem; border-radius:999px; font-size:0.75rem; }
  .up { background:#143528; color:var(--ok); }
  .down { background:#3a1520; color:var(--bad); }
  .metric { font-variant-numeric: tabular-nums; }
  .notes li { margin:0.35rem 0; color:var(--muted); }
  code { font-family: "IBM Plex Mono", ui-monospace, monospace; font-size:0.85em; }
  .hdr-controls { display:flex; align-items:center; gap:0.85rem; flex-wrap:wrap; color:var(--muted); font-size:0.9rem; }
  .hdr-controls label { display:inline-flex; align-items:center; gap:0.35rem; cursor:pointer; user-select:none; }
  .hdr-controls button {
    background: transparent; color: var(--fg); border:1px solid var(--line); border-radius:6px;
    padding:0.25rem 0.6rem; font: inherit; cursor:pointer;
  }
  .hdr-controls button:hover { border-color: #7ab7ff; }
  .hdr-controls a { color:#7ab7ff; }
</style>
</head>
<body>
<header>
  <div>
    <h1 id="title">Combiner</h1>
    <div class="sub" id="when"></div>
  </div>
  <div class="hdr-controls">
    <label title="Poll /api/status every 5 seconds">
      <input type="checkbox" id="auto-refresh" checked/>
      Auto-refresh (5s)
    </label>
    <button type="button" id="refresh-now" title="Fetch status once">Refresh</button>
    <a href="/api/status">/api/status</a>
  </div>
</header>
<main>
  <div class="grid" id="ifaces"></div>
  <div class="grid">
    <section><h2>nftables counters</h2><p class="hint">Kernel drops and SNAT. Non-zero PTP/media drops on a live Dante network are healthy.</p><div id="nft"></div></section>
    <section><h2>Reflector</h2><p class="hint">Userspace multicast reflector between Mgmt and Control/Dante. Kernel never forwards multicast.</p><div id="ref"></div></section>
  </div>
  <section>
    <h2>Discovered hosts</h2>
    <p class="hint">Unicast sources that sent allowlisted multicast discovery/control. <strong>Group</strong> is <code>allowlist/name</code> from YAML (hover a cell for address/port/notes).</p>
    <div id="hosts"></div>
  </section>
  <section>
    <h2>Multicast groups (Control / Dante)</h2>
    <p class="hint">IGMP joins from allowlists on the production VLAN interfaces. <strong>Pkts seen</strong> increments when that group is reflected.</p>
    <div id="mcast"></div>
  </section>
  <section><h2>DHCP leases (Mgmt)</h2><div id="leases"></div></section>
  <section><h2>Allowlists</h2><p class="hint">Configured reflection policy. Empty VuNET/Lake lists need on-site capture.</p><div id="allow"></div></section>
  <section><h2>Notes</h2><ul class="notes" id="notes"></ul></section>
</main>
<script>
function el(tag, text) {
  const n = document.createElement(tag);
  if (text != null) n.textContent = String(text);
  return n;
}
function th(label, tip) {
  const n = el('th', label);
  if (tip) n.title = tip;
  return n;
}
function row(table, k, v, tip) {
  const tr = el('tr');
  const ktd = el('td', k);
  if (tip) ktd.title = tip;
  tr.appendChild(ktd);
  tr.appendChild(el('td', v));
  table.appendChild(tr);
}
function groupCatalog(d) {
  const map = {};
  (d.allowlists || []).forEach(a => {
    (a.groups || []).forEach(g => {
      if (typeof g === 'string') return;
      const key = g.key || ((a.name || '') + '/' + (g.name || ''));
      let ports = String(g.port || '');
      if (g.port_end && g.port_end > g.port) ports += '-' + g.port_end;
      const parts = [
        'Allowlist group ' + key,
        'VLAN: ' + (a.vlan || ''),
        g.address ? ('Address: ' + g.address + (ports ? ':' + ports : '')) : '',
        g.notes || ''
      ].filter(Boolean);
      map[key] = parts.join(' · ');
    });
  });
  (d.reflector && d.reflector.memberships || []).forEach(m => {
    const key = (m.allowlist || '') + '/' + (m.name || '');
    if (map[key]) return;
    map[key] = [
      'Allowlist group ' + key,
      'VLAN: ' + (m.vlan || ''),
      'Address: ' + (m.address || '') + ':' + (m.port || ''),
      'Joined on ' + (m.peer_iface || '')
    ].join(' · ');
  });
  return map;
}
async function refresh() {
  const r = await fetch('/api/status');
  const d = await r.json();
  const tips = groupCatalog(d);
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
  row(nftTable, 'drop_ptp', n.drop_ptp, 'PTP (224.0.1.129-132 udp/319-320) dropped toward Mgmt/Control — expected non-zero when Dante is live.');
  row(nftTable, 'drop_deny_mcast', n.drop_deny_mcast, 'Hard-denied multicast prefixes (media/PTP floor).');
  row(nftTable, 'drop_forward_mcast', n.drop_forward_mcast, 'Any multicast the kernel tried to forward — should only rise if something bypasses the reflector path.');
  row(nftTable, 'drop_control_dante', n.drop_control_dante, 'Packets trying to hairpin Control↔Dante through the combiner.');
  row(nftTable, 'snat_to_control', n.snat_to_control, 'Unicast sessions SNATed so Control devices see the combiner address.');
  row(nftTable, 'snat_to_dante', n.snat_to_dante, 'Unicast sessions SNATed so Dante devices see the combiner address.');
  if (n.error) row(nftTable, 'error', n.error);
  nftWrap.appendChild(nftTable);

  const refWrap = document.getElementById('ref');
  refWrap.replaceChildren();
  const refTable = el('table');
  refTable.className = 'metric';
  const ref = d.reflector || {};
  row(refTable, 'groups', ref.groups, 'Count of allowlisted multicast group entries loaded from YAML.');
  row(refTable, 'listeners_up', ref.listeners_up, 'UDP ports currently listening for reflection.');
  row(refTable, 'listeners_fail', ref.listeners_fail, 'Listener start failures (bind/join errors).');
  row(refTable, 'packets_in', ref.packets_in, 'Multicast datagrams read by the reflector.');
  row(refTable, 'packets_out', ref.packets_out, 'Datagrams re-sent onto the peer VLAN.');
  row(refTable, 'packets_drop', ref.packets_drop, 'Read packets that did not match an allowlisted group/direction.');
  row(refTable, 'last_packet', ref.last_packet || '—', 'Most recent reflected packet summary.');
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
    head.appendChild(th('VLAN', 'Interface role where the source was last heard (mgmt, control, or dante).'));
    head.appendChild(th('IP', 'Source IP of the discovery/control sender.'));
    head.appendChild(th('Group', 'allowlist/name from config YAML. Example: dante/mdns = Dante allowlist, mDNS group (224.0.0.251:5353). Hover a cell for details.'));
    head.appendChild(th('Pkts', 'Packets reflected from this source.'));
    head.appendChild(th('Last seen', 'UTC timestamp of the most recent packet.'));
    table.appendChild(head);
    hosts.forEach(h => {
      const tr = el('tr');
      tr.appendChild(el('td', h.vlan));
      const ip = el('td');
      const code = el('code', h.ip);
      ip.appendChild(code);
      tr.appendChild(ip);
      const gtd = el('td', h.last_group);
      gtd.className = 'tip';
      gtd.title = tips[h.last_group] || ('Allowlist group key: ' + (h.last_group || '') + '. Format is allowlist/name from YAML.');
      tr.appendChild(gtd);
      const pk = el('td', h.packet_count);
      pk.className = 'metric';
      tr.appendChild(pk);
      tr.appendChild(el('td', h.last_seen));
      table.appendChild(tr);
    });
    hostsWrap.appendChild(table);
  }

  const mcastWrap = document.getElementById('mcast');
  mcastWrap.replaceChildren();
  const memberships = (ref.memberships || []).slice().sort((a, b) => {
    if (a.vlan === b.vlan) {
      if (a.address === b.address) return (a.port || 0) - (b.port || 0);
      return String(a.address).localeCompare(String(b.address));
    }
    return String(a.vlan).localeCompare(String(b.vlan));
  });
  const observed = {};
  (d.groups || []).forEach(g => {
    observed[g.vlan + '|' + g.address + '|' + g.port] = g;
  });
  if (!memberships.length) {
    const empty = el('div', 'No allowlisted groups joined yet (VuNET/Lake allowlists are empty until capture).');
    empty.className = 'sub';
    mcastWrap.appendChild(empty);
  } else {
    const table = el('table');
    const head = el('tr');
    head.appendChild(th('VLAN', 'Production VLAN for this allowlist (control or dante).'));
    head.appendChild(th('Group', 'allowlist/name from YAML. Hover for address, ports, and notes.'));
    head.appendChild(th('Address', 'Multicast destination IP.'));
    head.appendChild(th('Port', 'UDP port (or start of a port range).'));
    head.appendChild(th('Iface', 'Peer interface where this group is joined.'));
    head.appendChild(th('Pkts seen', 'Reflected packets for this group on the production VLAN.'));
    head.appendChild(th('Last source', 'Most recent unicast source that spoke on this group.'));
    head.appendChild(th('Last seen', 'UTC timestamp of the most recent packet.'));
    table.appendChild(head);
    memberships.forEach(m => {
      const key = m.vlan + '|' + m.address + '|' + m.port;
      const gkey = (m.allowlist || '') + '/' + (m.name || '');
      const o = observed[key] || {};
      const tr = el('tr');
      tr.appendChild(el('td', m.vlan));
      const gtd = el('td', gkey);
      gtd.className = 'tip';
      gtd.title = tips[gkey] || gkey;
      tr.appendChild(gtd);
      const addr = el('td');
      addr.appendChild(el('code', m.address));
      tr.appendChild(addr);
      const port = el('td', m.port);
      port.className = 'metric';
      tr.appendChild(port);
      tr.appendChild(el('td', m.peer_iface || ''));
      const pk = el('td', o.packet_count || 0);
      pk.className = 'metric';
      tr.appendChild(pk);
      tr.appendChild(el('td', o.last_source || '—'));
      tr.appendChild(el('td', o.last_seen || '—'));
      table.appendChild(tr);
    });
    mcastWrap.appendChild(table);
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
    const groups = a.groups || [];
    if (!groups.length) {
      const sub = el('div', '(empty — capture needed)');
      sub.className = 'sub';
      div.appendChild(sub);
    } else {
      groups.forEach(g => {
        const sub = el('div');
        sub.className = 'sub tip';
        if (typeof g === 'string') {
          sub.textContent = g;
        } else {
          let ports = String(g.port || '');
          if (g.port_end && g.port_end > g.port) ports += '-' + g.port_end;
          sub.textContent = (g.key || g.name) + '  ' + (g.address || '') + (ports ? ':' + ports : '');
          sub.title = tips[g.key] || (g.notes || '');
        }
        div.appendChild(sub);
      });
    }
    allowWrap.appendChild(div);
  });

  const notes = document.getElementById('notes');
  notes.replaceChildren();
  (d.notes || []).forEach(n => notes.appendChild(el('li', n)));
}

const REFRESH_MS = 5000;
const AUTO_KEY = 'combiner.status.autoRefresh';
let timer = null;
const autoBox = document.getElementById('auto-refresh');
const refreshBtn = document.getElementById('refresh-now');

function setAutoRefresh(on) {
  autoBox.checked = !!on;
  try { localStorage.setItem(AUTO_KEY, on ? '1' : '0'); } catch (_) {}
  if (timer) { clearInterval(timer); timer = null; }
  if (on) timer = setInterval(() => { refresh().catch(() => {}); }, REFRESH_MS);
}

autoBox.addEventListener('change', () => setAutoRefresh(autoBox.checked));
refreshBtn.addEventListener('click', () => { refresh().catch(() => {}); });

try {
  const saved = localStorage.getItem(AUTO_KEY);
  if (saved === '0') autoBox.checked = false;
} catch (_) {}

refresh().catch(() => {});
setAutoRefresh(autoBox.checked);
</script>
</body>
</html>
`
