package netinfo

import (
	"net"
	"os"
	"strings"
)

type IfaceStatus struct {
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Up        bool     `json:"up"`
	HasAddr   bool     `json:"has_addr"`
	Addresses []string `json:"addresses"`
	Error     string   `json:"error,omitempty"`
}

func Describe(role, name string) IfaceStatus {
	st := IfaceStatus{Name: name, Role: role}
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Up = ifi.Flags&net.FlagUp != 0
	addrs, err := ifi.Addrs()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	for _, a := range addrs {
		st.Addresses = append(st.Addresses, a.String())
		st.HasAddr = true
	}
	return st
}

func DHCPLeases(paths ...string) []string {
	var lines []string
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lines = append(lines, line)
		}
	}
	return lines
}
