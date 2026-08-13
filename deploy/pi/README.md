# Raspberry Pi lab deploy

Installs VLAN interfaces, Mgmt DHCP (`dnsmasq`), fail-closed `nftables`, and the `combiner` service on Debian / Raspberry Pi OS.

## Prerequisites

- Raspberry Pi with GbE (Pi 4/5 recommended)
- Switch trunk port carrying Mgmt + Control + Dante tags
- **Local console** (serial/HDMI) recommended — install disables NetworkManager/dhcpcd
- Edited `/etc/combiner/site.yaml` (start from `config/site.example.yaml`)

## Install

```bash
sudo mkdir -p /etc/combiner
sudo cp config/site.example.yaml /etc/combiner/site.yaml
sudo cp -r config/allowlists /etc/combiner/
# edit /etc/combiner/site.yaml
sudo ./deploy/pi/install.sh /etc/combiner/site.yaml --i-have-console
```

Without `--i-have-console`, the script refuses to run over SSH (it would lock you out).

### What the installer does (order matters)

1. Leaves **IP forwarding off**
2. Generates nftables and runs **`nft -c -f`** (abort if invalid)
3. Loads a bootstrap **forward drop** ruleset, then the real ruleset
4. Flushes conntrack
5. Configures VLANs / dnsmasq / combiner
6. **Enables IP forwarding last**

Avahi is disabled/masked so it does not fight the reflector on UDP 5353.

## Switch port

Configure the Pi’s switch port as a **trunk** with tagged Mgmt, Control, and Dante. Put the **WAP** on an access port **untagged Mgmt** only.

## Mgmt clients

- DHCP from the combiner; gateway = combiner Mgmt IP
- **No DNS** on Mgmt (by design) — open status at `http://<mgmt-ip>:8080/`
- `combiner.local` is not provided unless you add your own mDNS elsewhere

## Verify

```bash
ip -br addr
sudo nft list counters table inet combiner
sudo combiner-status
combiner -check -config /etc/combiner/site.yaml
curl -s http://127.0.0.1:8080/api/status | head
```

Healthy on a live Dante network: `drop_ptp` / `drop_forward_mcast` may be non-zero. That is good.

## Regenerating nftables only

```bash
sudo ./deploy/pi/generate-nftables.sh /etc/combiner/site.yaml /tmp/nft.conf
sudo nft -c -f /tmp/nft.conf
sudo cp /tmp/nft.conf /etc/nftables.conf
sudo nft -f /etc/nftables.conf
sudo conntrack -F || true
```
