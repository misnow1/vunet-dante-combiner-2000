package inventory_test

import (
	"testing"
	"time"

	"github.com/msnow/vunet-dante-combiner-2000/internal/inventory"
)

func TestObserveGroup(t *testing.T) {
	s := inventory.New()
	s.ObserveGroup("dante", "dante/mdns", "224.0.0.251", 5353, "10.201.2.10")
	s.ObserveGroup("dante", "dante/mdns", "224.0.0.251", 5353, "10.201.2.11")
	groups := s.ListGroups()
	if len(groups) != 1 {
		t.Fatalf("groups %d", len(groups))
	}
	g := groups[0]
	if g.VLAN != "dante" || g.Address != "224.0.0.251" || g.Port != 5353 {
		t.Fatalf("%+v", g)
	}
	if g.PacketCount != 2 {
		t.Fatalf("count %d", g.PacketCount)
	}
	if g.LastSource != "10.201.2.11" {
		t.Fatalf("source %s", g.LastSource)
	}
	if g.LastSeen.IsZero() || time.Since(g.LastSeen) > time.Minute {
		t.Fatalf("last_seen %v", g.LastSeen)
	}
}
