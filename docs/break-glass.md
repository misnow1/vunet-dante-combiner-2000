# Break-glass procedure

If the combiner is down, Control clients **lose the SNAT path to Dante** (Dante Controller, WWB, Lake). VuNET, StageMix, and MixPad on Control keep working if the WAP and Control VLAN are up.

Do **not** fail open by pointing Control clients at a switch SVI as a gateway “workaround” onto Dante. That reintroduces asymmetric paths and can look half-working while bypassing SNAT — and it is how PTP/media leaks start.

## Immediate recovery

1. **VuNET / Yamaha / MixPad** — Stay on the **Martin Control** SSID/access port. This path does not need the combiner.
2. **Dante Controller / Lake / WWB** — Connect a laptop to the **Dante** VLAN (access port or correct SSID), as before the combiner existed.
3. Need VuNET and Dante Controller at once — Dual NIC / two cables / two interfaces.
4. **DiGiCo + Waves SoundGrid** — Stay on the SoundGrid switch. Never patch SG onto Control to “share the WAP.”

## Confirm combiner death vs client issue

On a Control client:

- Ping the combiner Control IP
- Open `http://<control-ip>:8080/`
- If neither works, assume combiner/WAP/path failure and fall back as above

From a console on the combiner (if reachable):

```bash
sudo combiner-status
sudo systemctl status combiner nftables
```

Optional lab Mgmt: ping that address too; it is not required for production clients.

## After repair

1. Restore trunk (Control + Dante only) and site YAML
2. Confirm `combiner-status` shows Control + Dante carriers and non-zero PTP drop counters when Dante is live (drops are healthy)
3. Confirm Control DHCP still hands out **gateway = combiner Control IP** ([`setup.md`](setup.md))
4. Rejoin Control Wi-Fi / Ethernet; renew DHCP
5. Verify Dante Controller / WWB / Lake discover via the reflector, and VuNET still sees amps natively, before releasing the dual-NIC fallback
