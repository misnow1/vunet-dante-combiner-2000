# Break-glass procedure

If the combiner is down, clients **lose the path to the Martin amps** — VuNET stops discovering and stops controlling. Dante Controller, WWB and Lake keep working if the Dante VLAN is up, because in the shipped profile the client is already on Dante and never needed the combiner for those.

(That is for `client_vlan: dante`, which both shipped profiles use. If your site runs `client_vlan: control`, read every direction here reversed: VuNET survives and Dante Controller / WWB / Lake are what you lose.)

Do **not** fail open by pointing clients at a switch SVI as a gateway “workaround” onto the other VLAN. That reintroduces asymmetric paths and can look half-working while bypassing SNAT — and it is how PTP/media leaks start.

## Immediate recovery

1. **Dante Controller / Lake / WWB** — Stay where you are, on the **Dante** VLAN. This path never needed the combiner.
2. **VuNET / Yamaha / MixPad** — Connect a laptop to the **Martin Control** VLAN (access port or correct SSID), as before the combiner existed.
3. Need VuNET and Dante Controller at once — Dual NIC / two cables / two interfaces.
4. **DiGiCo + Waves SoundGrid** — Stay on the SoundGrid switch. Never patch SG onto Control to “share the WAP.”

## Confirm combiner death vs client issue

On a client (on Dante, in the shipped profile):

- Ping the combiner Dante IP
- Open `http://<dante-ip>:8080/`
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
