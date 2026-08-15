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
function formatGroupEndpoints(g) {
  const addrs = (g.addresses && g.addresses.length)
    ? g.addresses.slice()
    : (g.address ? [g.address] : []);
  let ports = '';
  if (g.ports && g.ports.length) {
    ports = g.ports.join(',');
  } else if (g.port) {
    ports = String(g.port);
    if (g.port_end && g.port_end > g.port) ports += '-' + g.port_end;
  }
  if (!addrs.length) return ports ? (':' + ports) : '';
  const addrPart = addrs.length === 1 ? addrs[0] : addrs.join(',');
  return ports ? (addrPart + ':' + ports) : addrPart;
}
function groupCatalog(d) {
  const map = {};
  (d.allowlists || []).forEach(a => {
    (a.groups || []).forEach(g => {
      if (typeof g === 'string') return;
      const key = g.key || ((a.name || '') + '/' + (g.name || ''));
      const ep = formatGroupEndpoints(g);
      const parts = [
        'Allowlist group ' + key,
        'VLAN: ' + (a.vlan || ''),
        ep ? ('Address: ' + ep) : '',
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
          const ep = formatGroupEndpoints(g);
          sub.textContent = (g.key || g.name) + (ep ? ('  ' + ep) : '');
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
