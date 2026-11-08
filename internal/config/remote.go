package config

import (
	"net"
	"strconv"
)

// PublicPorts are the ports the host firewall opens to the internet for the
// port-forwarded listener (ADR-0019): its HTTPS port, the HTTP-01 port when
// set, and WebRTC media (8555, tcp and udp). Empty when the public listener
// is off. scripts/firewall.sh asks for them (`sentinela public-ports`), so a
// port chosen in the dashboard is the one opened.
func (c *Config) PublicPorts() []string {
	p := c.Remote.Public
	if p.Domain == "" {
		return nil
	}
	var out []string
	add := func(listen, proto string) {
		if _, port, err := net.SplitHostPort(listen); err == nil {
			if n, err := strconv.Atoi(port); err == nil && n > 0 && n < 65536 {
				out = append(out, port+"/"+proto)
			}
		}
	}
	add(p.Listen, "tcp")
	add(p.HTTP01Listen, "tcp")
	out = append(out, "8555/tcp", "8555/udp")
	return out
}
