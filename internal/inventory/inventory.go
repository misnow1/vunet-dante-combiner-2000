package inventory

import (
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

type Store struct {
	mu    sync.RWMutex
	hosts map[string]*Host // key: vlan|ip
}

func New() *Store {
	return &Store{hosts: make(map[string]*Host)}
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

func (s *Store) Prune(olderThan time.Duration) {
	cutoff := time.Now().UTC().Add(-olderThan)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, h := range s.hosts {
		if h.LastSeen.Before(cutoff) {
			delete(s.hosts, k)
		}
	}
}
