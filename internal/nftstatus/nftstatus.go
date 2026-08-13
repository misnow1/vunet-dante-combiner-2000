package nftstatus

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Counters struct {
	DropPTP          uint64 `json:"drop_ptp"`
	DropDenyMcast    uint64 `json:"drop_deny_mcast"`
	DropForwardMcast uint64 `json:"drop_forward_mcast"`
	DropControlDante uint64 `json:"drop_control_dante"`
	DropInvalidPath  uint64 `json:"drop_invalid_path"`
	DropIPv6Forward  uint64 `json:"drop_ipv6_forward"`
	SNATToControl    uint64 `json:"snat_to_control"`
	SNATToDante      uint64 `json:"snat_to_dante"`
	Error            string `json:"error,omitempty"`
}

type nftJSON struct {
	Nftables []struct {
		Counter *struct {
			Family  string `json:"family"`
			Table   string `json:"table"`
			Name    string `json:"name"`
			Packets uint64 `json:"packets"`
		} `json:"counter"`
	} `json:"nftables"`
}

func Read() Counters {
	out, err := exec.Command("nft", "-j", "list", "counters", "table", "inet", "combiner").CombinedOutput()
	if err == nil {
		var parsed nftJSON
		if jerr := json.Unmarshal(out, &parsed); jerr == nil {
			c := Counters{}
			for _, item := range parsed.Nftables {
				if item.Counter == nil {
					continue
				}
				apply(&c, item.Counter.Name, item.Counter.Packets)
			}
			return c
		}
	}
	// Fallback: human-readable scrape
	out2, err2 := exec.Command("nft", "list", "counters", "table", "inet", "combiner").CombinedOutput()
	if err2 != nil {
		out3, err3 := exec.Command("nft", "-a", "list", "table", "inet", "combiner").CombinedOutput()
		if err3 != nil {
			return Counters{Error: strings.TrimSpace(string(out) + " " + err.Error())}
		}
		return parseText(string(out3))
	}
	return parseText(string(out2))
}

func apply(c *Counters, name string, pkts uint64) {
	switch name {
	case "drop_ptp":
		c.DropPTP = pkts
	case "drop_deny_mcast":
		c.DropDenyMcast = pkts
	case "drop_forward_mcast":
		c.DropForwardMcast = pkts
	case "drop_control_dante":
		c.DropControlDante = pkts
	case "drop_invalid_path":
		c.DropInvalidPath = pkts
	case "drop_ipv6_forward":
		c.DropIPv6Forward = pkts
	case "snat_to_control":
		c.SNATToControl = pkts
	case "snat_to_dante":
		c.SNATToDante = pkts
	}
}

var (
	counterObjRe  = regexp.MustCompile(`(?m)^\s*counter\s+(\w+)\s*\{[^}]*packets\s+(\d+)`)
	counterNameRe = regexp.MustCompile(`counter name (\w+) packets (\d+) bytes (\d+)`)
)

func parseText(text string) Counters {
	c := Counters{}
	for _, m := range counterObjRe.FindAllStringSubmatch(text, -1) {
		pkts, _ := strconv.ParseUint(m[2], 10, 64)
		apply(&c, m[1], pkts)
	}
	for _, m := range counterNameRe.FindAllStringSubmatch(text, -1) {
		pkts, _ := strconv.ParseUint(m[2], 10, 64)
		apply(&c, m[1], pkts)
	}
	return c
}
