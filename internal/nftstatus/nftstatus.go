package nftstatus

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/msnow/vunet-dante-combiner-2000/internal/nftexec"
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
	// Resolve nft explicitly: it lives in /usr/sbin, which a non-root login
	// PATH omits. Reporting the lookup failure matters because the zero value
	// of Counters is indistinguishable from "every counter is 0" — silently
	// returning that during an outage actively misleads the operator.
	if !nftexec.Available() {
		return Counters{Error: nftexec.ErrNotFound.Error()}
	}
	out, err := run("-j", "list", "counters", "table", "inet", "combiner")
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
	out2, err2 := run("list", "counters", "table", "inet", "combiner")
	if err2 != nil {
		out3, err3 := run("-a", "list", "table", "inet", "combiner")
		if err3 != nil {
			// err is nil when the -j call succeeded but its JSON did not
			// parse, so build the message from whichever error exists.
			return Counters{Error: joinErr(string(out), err, err3)}
		}
		return parseText(string(out3))
	}
	return parseText(string(out2))
}

// joinErr builds a non-empty diagnostic from whichever of the nft attempts
// actually failed, tolerating a nil first error.
func joinErr(out string, errs ...error) string {
	parts := []string{strings.TrimSpace(out)}
	for _, e := range errs {
		if e != nil {
			parts = append(parts, e.Error())
		}
	}
	// nft repeats the same "exit status 1" for each failed attempt; collapse
	// duplicates so the operator sees the useful line, not an echo of it.
	var kept []string
	seen := map[string]bool{}
	for _, p := range parts {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		return "nft failed with no diagnostic output"
	}
	return strings.Join(kept, ": ")
}

func run(args ...string) ([]byte, error) {
	cmd, err := nftexec.Command(args...)
	if err != nil {
		return nil, err
	}
	return cmd.CombinedOutput()
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
