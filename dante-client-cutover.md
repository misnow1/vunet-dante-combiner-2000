# Dante-client cutover runbook

Switches the combiner from the default control-client profile to
`client_vlan: dante`: control clients move to Dante Primary, and the combiner
reflects Martin VU-NET onto them instead of reflecting Dante onto Control.

**This is a cutover, not a test.** It changes which VLAN the operator's laptop
lives on and replaces the entire nftables ruleset. Not during a show.

Revert is a documented path back, not a `git checkout` — read section 6 before
starting.

---

## 0. Pre-flight

**Clear earlier experiment state first**, so the revert target is the known-good
canonical config rather than a half-modified one:

```bash
# undo the 8708 allowlist narrowing (not loaded in the new profile, but the
# revert path depends on it)
sudo cp -a /etc/combiner/allowlists/dante.yaml.bak-8708 /etc/combiner/allowlists/dante.yaml

# drop the hand-inserted cross-subnet test rules
sudo nft -f /etc/nftables.conf
```

Snapshot everything the cutover replaces:

```bash
sudo mkdir -p /root/combiner-backup
sudo cp -a /etc/combiner/site.yaml        /root/combiner-backup/site.yaml
sudo cp -a /etc/nftables.conf             /root/combiner-backup/nftables.conf
sudo cp -a /usr/local/bin/combiner        /root/combiner-backup/combiner
sudo cp -a /usr/local/bin/combiner-status /root/combiner-backup/combiner-status
sudo nft list ruleset > /root/combiner-backup/ruleset.txt
sudo ls -la /root/combiner-backup
```

Confirm SSH survives the change: the new profile sets
`management_access: [dante, control]`, so port 22 stays reachable from both.
Verify that line is present before applying — if it is not, a Dante-side SSH
session is cut the moment the ruleset loads.

```bash
grep -A1 management_access config/site.dante-client.example.yaml
```

---

## 1. Move the laptop to Dante Primary

Switch port: untagged/PVID **201**. Static address as before
(`10.201.0.18/21` worked), **no gateway**, and this time **no static route** —
the combiner handles Control reachability.

Confirm before continuing:

```bash
ping 10.201.0.1        # combiner, Dante side
```

---

## 2. Install the new binary

The running binary predates `client_vlan` and parses config with
`KnownFields(true)`, so it will **reject** the new site.yaml as an unknown key.
The binary must be replaced first or the service will not start.

From the repo checkout on the Pi (branch `feat/client-vlan-dante`):

```bash
cd ~/vunet-dante-combiner-2000
git fetch origin && git checkout feat/client-vlan-dante && git pull

go build -o /tmp/combiner ./cmd/combiner
go build -o /tmp/combiner-status ./cmd/combiner-status
sudo install -m 0755 /tmp/combiner /tmp/combiner-status /usr/local/bin/
```

---

## 3. Install the new config

```bash
sudo cp config/site.dante-client.example.yaml /etc/combiner/site.yaml
sudo cp config/allowlists/vunet.yaml /etc/combiner/allowlists/vunet.yaml
```

Validate **before** touching the ruleset. Config errors exit non-zero here
rather than leaving a dead service:

```bash
sudo combiner -config /etc/combiner/site.yaml -check
```

Expect `config OK`, `allowlists 1 (vunet)`, and
`reflector 2 group memberships on 2 udp ports`. It will also report
`nftables drift MISMATCH` — correct at this point, since the ruleset is still
the old profile's. That is the next step.

---

## 4. Regenerate and apply the ruleset

```bash
python3 deploy/pi/generate-nftables.py /etc/combiner/site.yaml /tmp/nft.conf
sudo nft -c -f /tmp/nft.conf          # must pass before applying
sudo cp /tmp/nft.conf /etc/nftables.conf
sudo nft -f /etc/nftables.conf
sudo conntrack -F
sudo systemctl restart combiner
```

Confirm the ruleset is what it should be — **especially that the denies did not
follow the client**:

```bash
sudo combiner -config /etc/combiner/site.yaml -check      # expect: drift none, EXIT 0
sudo nft list chain inet combiner forward | grep -E "drop_ptp|accept$"
```

The PTP deny must still name **`eth0.200`** (Control). If it names `eth0`,
stop and revert — that would drop PTP toward Dante.

---

## 5. Verify — both protocols

### VU-NET (the new path)

**Clear the VU-NET project first.** This is the step that makes the test real:
with a saved project VU-NET reconnects to cached amp IPs over TCP and appears
to work while discovery is completely broken. That false positive has already
happened once during this investigation.

- [ ] New/empty project — no amps defined
- [ ] Hit discover
- [ ] All **12** amps appear (allow a second pass; VU-NET's known bug misses
      some on the first even on a flat network)
- [ ] A control change (gain trim / mute) reaches an amp
- [ ] Sessions hold for 5+ minutes

Watch the reflector actually doing the work:

```bash
sudo journalctl -u combiner -f | grep 239.254.10.2
sudo conntrack -L | grep 63489 | wc -l      # expect 12
```

### Dante Controller (the reason for the cutover)

- [ ] Devices enumerate
- [ ] **Metering tab populates** — this never worked before
- [ ] Device settings can be changed
- [ ] A subscription change takes

`10.202.1.x` (Dante Secondary) stays unreachable — VLAN 202 is not on the
combiner's trunk. Expected, unrelated to this change.

---

## 6. Revert

Full path back to the control-client profile:

```bash
sudo cp -a /root/combiner-backup/site.yaml        /etc/combiner/site.yaml
sudo cp -a /root/combiner-backup/nftables.conf    /etc/nftables.conf
sudo install -m 0755 /root/combiner-backup/combiner /root/combiner-backup/combiner-status /usr/local/bin/
sudo nft -f /etc/nftables.conf
sudo conntrack -F
sudo systemctl restart combiner
sudo combiner -config /etc/combiner/site.yaml -check     # expect drift none
```

Then move the laptop back to a Control port. Note the old binary predates
`client_vlan`, so it pairs only with the old config — restore both or neither.

---

## What each outcome means

| Result | Conclusion |
| --- | --- |
| VU-NET discovers 12 from an empty project, Dante metering works | **Cutover succeeds.** Merge #16 and make this the site profile. |
| Dante works, VU-NET finds nothing | Reflection is not carrying discovery. Capture on Dante for `239.254.10.2` and check the reflector joined on the Control side. |
| VU-NET finds amps but cannot control them | Discovery fine, unicast/SNAT wrong. Check `snat_to_control` and `conntrack -L \| grep 63489`. |
| PTP deny names `eth0` | **Stop and revert.** The deny anchoring regressed; do not leave this running. |
