package inventory

import (
	"strconv"
	"sync"
	"time"
)

type Host struct {
	IP          string    `json:"ip"`
	VLAN        string    `json:"vlan"`
	LastGroup   string    `json:"last_group"`
	LastSeen    time.Time `json:"last_seen"`
	PacketCount uint64    `json:"packet_count"`
	RoleHint    string    `json:"role_hint,omitempty"` // device|controller|unknown
}

// Group is a multicast destination observed on a VLAN (discovery/control traffic).
type Group struct {
	VLAN        string    `json:"vlan"`
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Port        int       `json:"port"`
	LastSource  string    `json:"last_source,omitempty"`
	LastSeen    time.Time `json:"last_seen"`
	PacketCount uint64    `json:"packet_count"`
}

type Store struct {
	mu     sync.RWMutex
	hosts  map[string]*Host  // key: vlan|ip
	groups map[string]*Group // key: vlan|address|port
}

func New() *Store {
	return &Store{
		hosts:  make(map[string]*Host),
		groups: make(map[string]*Group),
	}
}

func (s *Store) Observe(ip, vlan, group string) {
	if ip == "" || ip == "0.0.0.0" {
		return
	}
	key := vlan + "|" + ip
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hosts[key]
	if !ok {
		h = &Host{IP: ip, VLAN: vlan, RoleHint: "unknown"}
		s.hosts[key] = h
	}
	h.LastGroup = group
	h.LastSeen = now
	h.PacketCount++
}

func (s *Store) ObserveGroup(vlan, name, address string, port int, source string) {
	if vlan == "" || address == "" || port < 1 {
		return
	}
	key := vlan + "|" + address + "|" + strconv.Itoa(port)
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[key]
	if !ok {
		g = &Group{VLAN: vlan, Name: name, Address: address, Port: port}
		s.groups[key] = g
	}
	if name != "" {
		g.Name = name
	}
	g.LastSource = source
	g.LastSeen = now
	g.PacketCount++
}

func (s *Store) List() []Host {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Host, 0, len(s.hosts))
	for _, h := range s.hosts {
		cp := *h
		out = append(out, cp)
	}
	return out
}

func (s *Store) ListGroups() []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Group, 0, len(s.groups))
	for _, g := range s.groups {
		cp := *g
		out = append(out, cp)
	}
	return out
}

func (s *Store) Prune(olderThan time.Duration) {
	cutoff := time.Now().UTC().Add(-olderThan)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, h := range s.hosts {
		if h.LastSeen.Before(cutoff) {
			delete(s.hosts, k)
		}
	}
	for k, g := range s.groups {
		if g.LastSeen.Before(cutoff) {
			delete(s.groups, k)
		}
	}
}
