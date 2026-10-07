package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

type interfaceInfo struct {
	name  string
	flags net.Flags
	addrs []net.Addr
}

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

func usableIPv4(ip net.IP) string {
	return pickIPv4([]net.Addr{&net.IPAddr{IP: ip}})
}

func interfaceForIP(ip net.IP, ifaces []interfaceInfo) *interfaceInfo {
	for i := range ifaces {
		iface := &ifaces[i]
		for _, addr := range iface.addrs {
			var candidate net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				candidate = v.IP
			case *net.IPAddr:
				candidate = v.IP
			}
			if candidate != nil && candidate.Equal(ip) {
				return iface
			}
		}
	}
	return nil
}

func physicalPrivateIPv4(ifaces []interfaceInfo) string {
	for _, iface := range ifaces {
		if iface.flags&net.FlagUp == 0 || iface.flags&net.FlagLoopback != 0 || !strings.HasPrefix(iface.name, "en") {
			continue
		}
		for _, addr := range iface.addrs {
			if ip := net.ParseIP(pickIPv4([]net.Addr{addr})); ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

func walkInterfaces(ifaces []interfaceInfo) string {
	for _, iface := range ifaces {
		if iface.flags&net.FlagUp == 0 || iface.flags&net.FlagLoopback != 0 {
			continue
		}
		if ip := pickIPv4(iface.addrs); ip != "" {
			return ip
		}
	}
	return ""
}

// chooseLanIP normally prefers the default-route address (issue #13). A
// full-tunnel VPN is the exception: its default route belongs to utun*, which
// the Apple TV cannot reach from the physical LAN. In that case prefer a
// private address on an active macOS hardware interface.
func chooseLanIP(route net.IP, ifaces []interfaceInfo) string {
	if route != nil {
		if ip := usableIPv4(route); ip != "" {
			if iface := interfaceForIP(route, ifaces); iface != nil && strings.HasPrefix(iface.name, "utun") {
				if physical := physicalPrivateIPv4(ifaces); physical != "" {
					return physical
				}
			}
			return ip
		}
	}
	return walkInterfaces(ifaces)
}

func interfaces() ([]interfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	infos := make([]interfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		infos = append(infos, interfaceInfo{name: iface.Name, flags: iface.Flags, addrs: addrs})
	}
	return infos, nil
}

func validateIPOverride(override string, ifaces []interfaceInfo) (string, error) {
	ip := net.ParseIP(override)
	if usableIPv4(ip) == "" {
		return "", fmt.Errorf("invalid LAN IPv4 override %q", override)
	}
	iface := interfaceForIP(ip, ifaces)
	if iface == nil {
		return "", fmt.Errorf("LAN IPv4 override %q is not assigned to this Mac", override)
	}
	if iface.flags&net.FlagUp == 0 {
		return "", fmt.Errorf("LAN IPv4 override %q cannot be used: interface %s is down", override, iface.name)
	}
	return ip.String(), nil
}

func LanIP(override string) (string, error) {
	ifaces, err := interfaces()
	if err != nil {
		return "", err
	}
	if override != "" {
		return validateIPOverride(override, ifaces)
	}
	if ip := chooseLanIP(defaultRouteIP(), ifaces); ip != "" {
		return ip, nil
	}
	return "", errors.New("no LAN IPv4 address found; the TV pulls the stream itself, so a routable address is required")
}
