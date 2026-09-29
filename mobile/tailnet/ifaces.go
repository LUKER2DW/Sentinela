package tailnet

import (
	"net"
	"strconv"
	"strings"
	"sync"

	"tailscale.com/net/netmon"
)

// Android 11+ forbids apps the netlink calls behind net.Interfaces, so the
// app describes its interfaces from Java (NetworkInterface) and Tailscale's
// network monitor reads them from here, as the Tailscale Android app does.

var (
	ifMu   sync.Mutex
	ifDesc string
)

func init() { netmon.RegisterInterfaceGetter(interfaces) }

// SetInterfaces replaces the interface list. One interface per line:
//
//	name index mtu flags addr/prefix addr/prefix ...
//
// flags is a comma list of up, broadcast, loopback, pointtopoint, multicast.
// Call it before Start and whenever the network changes.
func SetInterfaces(desc string) {
	ifMu.Lock()
	ifDesc = desc
	ifMu.Unlock()
}

func interfaces() ([]netmon.Interface, error) {
	ifMu.Lock()
	desc := ifDesc
	ifMu.Unlock()
	if desc == "" {
		ifs, err := net.Interfaces()
		if err != nil {
			return nil, err
		}
		out := make([]netmon.Interface, len(ifs))
		for i := range ifs {
			out[i] = netmon.Interface{Interface: &ifs[i]}
		}
		return out, nil
	}
	return parseInterfaces(desc), nil
}

func parseInterfaces(desc string) []netmon.Interface {
	var out []netmon.Interface
	for _, line := range strings.Split(desc, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		idx, err1 := strconv.Atoi(f[1])
		mtu, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			continue
		}
		ni := &net.Interface{Index: idx, MTU: mtu, Name: f[0]}
		for _, fl := range strings.Split(f[3], ",") {
			switch fl {
			case "up":
				ni.Flags |= net.FlagUp | net.FlagRunning
			case "broadcast":
				ni.Flags |= net.FlagBroadcast
			case "loopback":
				ni.Flags |= net.FlagLoopback
			case "pointtopoint":
				ni.Flags |= net.FlagPointToPoint
			case "multicast":
				ni.Flags |= net.FlagMulticast
			}
		}
		addrs := []net.Addr{}
		for _, a := range f[4:] {
			if ip, n, err := net.ParseCIDR(a); err == nil {
				addrs = append(addrs, &net.IPNet{IP: ip, Mask: n.Mask})
			}
		}
		out = append(out, netmon.Interface{Interface: ni, AltAddrs: addrs})
	}
	return out
}
