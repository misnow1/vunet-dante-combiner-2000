# Break-glass procedure

If the combiner is down, **Mgmt is a dead end**. Do not debug Wi-Fi or DHCP on Mgmt until the box is confirmed healthy.

## Immediate recovery

Use the pre-combiner workflow:

1. **VuNET** — Connect the laptop/tablet to the **Martin Control** VLAN (access port or correct SSID). Use core-switch DHCP pool or static addressing as today.
2. **Dante Controller / Lake** — Connect to the **Dante** VLAN the same way.
3. Need both at once — Dual NIC / two cables / two interfaces, as before.

Do **not** try to fail open by pointing Mgmt clients at switch SVIs as a gateway “workaround.” That reintroduces asymmetric paths and can look half-working while bypassing SNAT.

## Confirm combiner death vs client issue

On any Mgmt client that still has a lease:

- Ping the combiner Mgmt IP
- Open `http://<mgmt-ip>:8080/`
- If neither works, assume combiner/WAP/path failure and fall back as above

From a console on the combiner (if reachable):

```bash
sudo combiner-status
sudo systemctl status combiner nftables dnsmasq
```

## After repair

1. Restore trunk and site YAML
2. Confirm `combiner-status` shows Control + Dante carriers and non-zero PTP drop counters when Dante is live (drops are healthy)
3. Rejoin Mgmt Wi-Fi / Ethernet; renew DHCP
4. Verify each app discovers devices before releasing the dual-NIC fallback
