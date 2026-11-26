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

// ProxyPort is the port of the tunnel / reverse proxy listener
// (remote.proxy.listen), 0 when it is off. scripts/setup-cloudflared.sh
// asks for it (`sentinela proxy-port`): on loopback, the Cloudflare Tunnel's
// connector may reach that port and no other.
func (c *Config) ProxyPort() int {
	p := c.Remote.Proxy
	if p.URL == "" {
		return 0
	}
	_, port, err := net.SplitHostPort(p.Listen)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return 0
	}
	return n
}
