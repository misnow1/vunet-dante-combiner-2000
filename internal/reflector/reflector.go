package reflector

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/msnow/vunet-dante-combiner-2000/internal/config"
	"github.com/msnow/vunet-dante-combiner-2000/internal/inventory"
	"golang.org/x/net/ipv4"
)

type Stats struct {
	PacketsIn     uint64       `json:"packets_in"`
	PacketsOut    uint64       `json:"packets_out"`
	PacketsDrop   uint64       `json:"packets_drop"`
	Groups        int          `json:"groups"`
	ListenersUp   int          `json:"listeners_up"`
	ListenersFail int          `json:"listeners_fail"`
	LastError     string       `json:"last_error,omitempty"`
	LastPacket    string       `json:"last_packet,omitempty"`
	Memberships   []Membership `json:"memberships,omitempty"`
}

// Membership is a joined allowlist group on Control <-> Dante.
type Membership struct {
	Allowlist   string `json:"allowlist"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	VLAN        string `json:"vlan"` // dante
	ClientIface string `json:"client_iface"`
	PeerIface   string `json:"peer_iface"`
	Direction   string `json:"direction"`
}

type membership struct {
	groupIP   net.IP
	name      string
	allowlist string
	vlanRole  string
	peerIf    string
	direction string
}

type Service struct {
	site     *config.Site
	inv      *inventory.Store
	denyNets []*net.IPNet

	mu            sync.Mutex
	lastError     string
	lastPacket    string
	listenersUp   int
	listenersFail int
	packetsIn     uint64
	packetsOut    uint64
	packetsDrop   uint64
	memberships   []Membership
}

func New(site *config.Site, inv *inventory.Store) (*Service, error) {
	s := &Service{site: site, inv: inv}
	for _, p := range site.DenyPrefixes {
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("deny prefix %q: %w", p, err)
		}
		s.denyNets = append(s.denyNets, n)
	}
	// Refuse allowlist entries that intersect the deny floor
	for _, al := range site.Allowlists {
		for _, g := range al.Groups {
			for _, addr := range g.ResolvedAddresses() {
				ip := net.ParseIP(addr)
				for _, p := range g.ResolvedPorts() {
					if s.site.DeniedUDP(ip, p) {
						return nil, fmt.Errorf("allowlist %s/%s %s:%d is on deny floor", al.Name, g.Name, addr, p)
					}
				}
			}
		}
	}
	return s, nil
}

func (s *Service) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := append([]Membership(nil), s.memberships...)
	return Stats{
		PacketsIn:     atomic.LoadUint64(&s.packetsIn),
		PacketsOut:    atomic.LoadUint64(&s.packetsOut),
		PacketsDrop:   atomic.LoadUint64(&s.packetsDrop),
		Groups:        s.groupCount(),
		ListenersUp:   s.listenersUp,
		ListenersFail: s.listenersFail,
		LastError:     s.lastError,
		LastPacket:    s.lastPacket,
		Memberships:   mem,
	}
}

func (s *Service) setErr(msg string) {
	s.mu.Lock()
	s.lastError = msg
	s.mu.Unlock()
}

func (s *Service) setPacket(msg string) {
	s.mu.Lock()
	s.lastPacket = msg
	s.mu.Unlock()
}

func (s *Service) groupCount() int {
	n := 0
	for _, al := range s.site.Allowlists {
		for _, g := range al.Groups {
			n += len(g.Endpoints())
		}
	}
	return n
}

func (s *Service) denied(ip net.IP) bool {
	for _, n := range s.denyNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func multicastTTL(group net.IP) int {
	// mDNS: RFC 6762 prefers TTL 255
	if group.Equal(net.ParseIP("224.0.0.251")) {
		return 255
	}
	// 224.0.0.0/24 link-local control block — do not look routable
	if group[0] == 224 && group[1] == 0 && group[2] == 0 {
		return 1
	}
	return 32
}

func (s *Service) Run(ctx context.Context) error {
	clientIf := s.site.ClientIface()

	byPort := map[int][]membership{}
	seen := map[string]struct{}{} // group|port|peer
	var mems []Membership
	for _, al := range s.site.Allowlists {
		peer, err := s.site.PeerIface(al.VLAN)
		if err != nil {
			return err
		}
		if peer == clientIf {
			return fmt.Errorf("allowlist %s peer iface equals control (client) iface", al.Name)
		}
		for _, g := range al.Groups {
			for _, ep := range g.Endpoints() {
				ip := net.ParseIP(ep.Address).To4()
				if ip == nil || s.denied(ip) {
					log.Printf("skip group %s (%s)", g.Name, ep.Address)
					continue
				}
				key := fmt.Sprintf("%s|%d|%s", ip, ep.Port, peer)
				if _, ok := seen[key]; ok {
					log.Printf("dedupe membership %s udp/%d on %s", ip, ep.Port, peer)
					continue
				}
				seen[key] = struct{}{}
				byPort[ep.Port] = append(byPort[ep.Port], membership{
					groupIP:   append(net.IP(nil), ip...),
					name:      g.Name,
					allowlist: al.Name,
					vlanRole:  al.VLAN,
					peerIf:    peer,
					direction: g.Direction,
				})
				mems = append(mems, Membership{
					Allowlist:   al.Name,
					Name:        g.Name,
					Address:     ip.String(),
					Port:        ep.Port,
					VLAN:        al.VLAN,
					ClientIface: clientIf,
					PeerIface:   peer,
					Direction:   g.Direction,
				})
			}
		}
	}
	s.mu.Lock()
	s.memberships = mems
	s.mu.Unlock()

	var wg sync.WaitGroup
	for port, members := range byPort {
		port, members := port, members
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.servePort(ctx, clientIf, port, members); err != nil && ctx.Err() == nil {
				log.Printf("listener udp/%d stopped: %v", port, err)
				s.mu.Lock()
				s.listenersFail++
				s.lastError = err.Error()
				s.mu.Unlock()
				return
			}
		}()
	}
	log.Printf("reflector starting %d udp ports (%d group memberships) on %s <-> dante", len(byPort), len(seen), clientIf)
	if len(byPort) == 0 {
		log.Printf("warning: no multicast groups configured — discovery reflection idle")
	}

	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.inv.Prune(24 * time.Hour)
			}
		}
	}()

	<-ctx.Done()
	wg.Wait()
	return nil
}

func listenUDP(port int) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			err := c.Control(func(fd uintptr) {
				opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			})
			if err != nil {
				return err
			}
			return opErr
		},
	}
	return lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", port))
}

func (s *Service) servePort(ctx context.Context, clientIf string, port int, members []membership) error {
	pc, err := listenUDP(port)
	if err != nil {
		s.mu.Lock()
		s.listenersFail++
		s.mu.Unlock()
		return fmt.Errorf("listen udp/%d: %w", port, err)
	}
	defer pc.Close()
	s.mu.Lock()
	s.listenersUp++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.listenersUp--
		s.mu.Unlock()
	}()

	p := ipv4.NewPacketConn(pc)
	if err := p.SetControlMessage(ipv4.FlagDst|ipv4.FlagInterface, true); err != nil {
		return fmt.Errorf("SetControlMessage: %w", err)
	}
	_ = p.SetMulticastLoopback(false)

	clientIFI, err := net.InterfaceByName(clientIf)
	if err != nil {
		return fmt.Errorf("control iface %s: %w", clientIf, err)
	}

	peerCache := map[string]*net.Interface{}
	joined := []struct {
		ifi *net.Interface
		ip  net.IP
	}{}

	join := func(ifi *net.Interface, ip net.IP) {
		if err := p.JoinGroup(ifi, &net.UDPAddr{IP: ip}); err != nil {
			log.Printf("join %s on %s: %v", ip, ifi.Name, err)
			return
		}
		joined = append(joined, struct {
			ifi *net.Interface
			ip  net.IP
		}{ifi, ip})
	}

	for _, m := range members {
		peerIFI, ok := peerCache[m.peerIf]
		if !ok {
			peerIFI, err = net.InterfaceByName(m.peerIf)
			if err != nil {
				return fmt.Errorf("peer iface %s: %w", m.peerIf, err)
			}
			peerCache[m.peerIf] = peerIFI
		}
		join(clientIFI, m.groupIP)
		join(peerIFI, m.groupIP)
		log.Printf("membership %s/%s %s udp/%d on %s <-> %s (ttl=%d)",
			m.allowlist, m.name, m.groupIP, port, clientIf, m.peerIf, multicastTTL(m.groupIP))
	}
	defer func() {
		for _, j := range joined {
			_ = p.LeaveGroup(j.ifi, &net.UDPAddr{IP: j.ip})
		}
	}()

	findMembers := func(dst net.IP) []membership {
		var out []membership
		for _, m := range members {
			if m.groupIP.Equal(dst) {
				out = append(out, m)
			}
		}
		return out
	}

	localIPs := localIPv4Set()
	buf := make([]byte, 65535)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, cm, src, err := p.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			s.setErr(err.Error())
			continue
		}
		atomic.AddUint64(&s.packetsIn, 1)

		srcAddr, _ := src.(*net.UDPAddr)
		srcIP := ""
		if srcAddr != nil {
			srcIP = srcAddr.IP.String()
			if localIPs[srcIP] {
				continue
			}
		}

		inIf := ""
		if cm != nil && cm.IfIndex != 0 {
			ifi, err := net.InterfaceByIndex(cm.IfIndex)
			if err == nil {
				inIf = ifi.Name
			}
		}
		if cm == nil || cm.Dst == nil || !cm.Dst.IsMulticast() {
			atomic.AddUint64(&s.packetsDrop, 1)
			continue
		}
		if s.denied(cm.Dst) || s.site.DeniedUDP(cm.Dst, port) {
			atomic.AddUint64(&s.packetsDrop, 1)
			continue
		}
		matched := findMembers(cm.Dst)
		if len(matched) == 0 {
			// No fan-out: unknown destination for this listener → drop
			atomic.AddUint64(&s.packetsDrop, 1)
			continue
		}

		for _, m := range matched {
			peerIFI := peerCache[m.peerIf]
			if peerIFI == nil {
				continue
			}
			var outIf *net.Interface
			var seenVLAN string
			switch inIf {
			case clientIf:
				if m.direction == "to-control" {
					continue
				}
				outIf = peerIFI
				seenVLAN = "control"
			case m.peerIf:
				if m.direction == "from-control" {
					continue
				}
				outIf = clientIFI
				seenVLAN = m.vlanRole
			default:
				continue
			}

			s.inv.Observe(srcIP, seenVLAN, m.allowlist+"/"+m.name)
			// Attribute activity to the production VLAN (control|dante) so the
			// status page can show groups seen on those interfaces.
			s.inv.ObserveGroup(m.vlanRole, m.allowlist+"/"+m.name, m.groupIP.String(), port, srcIP)
			s.setPacket(fmt.Sprintf("%s %s -> %s %s:%d %dB", srcIP, inIf, outIf.Name, m.groupIP, port, n))

			_ = p.SetMulticastTTL(multicastTTL(m.groupIP))
			_ = p.SetMulticastInterface(outIf)
			dst := &net.UDPAddr{IP: m.groupIP, Port: port}
			if _, err := p.WriteTo(buf[:n], &ipv4.ControlMessage{IfIndex: outIf.Index}, dst); err != nil {
				s.setErr(err.Error())
				continue
			}
			atomic.AddUint64(&s.packetsOut, 1)
		}
	}
}

func localIPv4Set() map[string]bool {
	out := map[string]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifi := range ifaces {
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				out[ip4.String()] = true
			}
		}
	}
	return out
}
