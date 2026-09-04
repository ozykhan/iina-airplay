package main

import (
	"errors"
	"net"
)

func pickIPv4(addrs []net.Addr) string {
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		ip4 := ip.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
			continue
		}
		return ip4.String()
	}
	return ""
}

// defaultRouteIP asks the kernel which local address it would use to reach
// an off-LAN host. Nothing is transmitted: a UDP "connect" only resolves the
// route, and 192.0.2.1 is TEST-NET-1, reserved and never routable, so even a
// stray write could not reach anything. Returns nil when there is no default
// route at all (Wi-Fi to a TV-only network, say).
func defaultRouteIP() net.IP {
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return nil
	}
	defer c.Close()
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil
	}
	return addr.IP
}

// chooseLanIP prefers the default-route address and falls back to walk. The
// route is what an Apple TV on the same LAN can actually reach; the walk is
// interface-index order, which lets a VPN tunnel, Docker bridge or second
// NIC win purely by sorting first (issue #13).
func chooseLanIP(route net.IP, walk func() string) string {
	if route != nil {
		if ip := pickIPv4([]net.Addr{&net.IPAddr{IP: route}}); ip != "" {
			return ip
		}
	}
	return walk()
}

// walkInterfaces returns the first up, non-loopback interface's usable IPv4,
// in interface-index order — the pre-#13 behaviour, kept as the fallback.
func walkInterfaces() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		if ip := pickIPv4(addrs); ip != "" {
			return ip
		}
	}
	return ""
}

func LanIP() (string, error) {
	if ip := chooseLanIP(defaultRouteIP(), walkInterfaces); ip != "" {
		return ip, nil
	}
	return "", errors.New("no LAN IPv4 address found; the TV pulls the stream itself, so a routable address is required")
}
