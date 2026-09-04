package main

import (
	"net"
	"testing"
)

func mustCIDR(t *testing.T, s string) net.Addr {
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return &net.IPNet{IP: ip, Mask: n.Mask}
}

func TestPickIPv4(t *testing.T) {
	cases := []struct {
		addrs []net.Addr
		want  string
	}{
		{[]net.Addr{mustCIDR(t, "127.0.0.1/8")}, ""},                                            // loopback rejected
		{[]net.Addr{mustCIDR(t, "169.254.10.1/16")}, ""},                                        // link-local rejected
		{[]net.Addr{mustCIDR(t, "fe80::1/64"), mustCIDR(t, "192.168.1.18/24")}, "192.168.1.18"}, // v6 skipped
		{[]net.Addr{mustCIDR(t, "10.0.0.7/8")}, "10.0.0.7"},
	}
	for i, c := range cases {
		if got := pickIPv4(c.addrs); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestChooseLanIP(t *testing.T) {
	walk := func(ips ...string) func() string {
		return func() string {
			if len(ips) == 0 {
				return ""
			}
			return ips[0]
		}
	}
	cases := []struct {
		name  string
		route net.IP
		walk  func() string
		want  string
	}{
		// The whole point of the issue: a VPN/bridge interface sorts first,
		// but the kernel routes off-LAN traffic through the Wi-Fi address.
		{"default route beats earlier-indexed interface", net.ParseIP("192.168.1.18"), walk("10.8.0.2", "192.168.1.18"), "192.168.1.18"},
		{"no default route falls back to walk", nil, walk("10.8.0.2"), "10.8.0.2"},
		{"loopback route falls back to walk", net.ParseIP("127.0.0.1"), walk("192.168.1.18"), "192.168.1.18"},
		{"link-local route falls back to walk", net.ParseIP("169.254.10.1"), walk("192.168.1.18"), "192.168.1.18"},
		{"v6 route falls back to walk", net.ParseIP("2001:db8::1"), walk("192.168.1.18"), "192.168.1.18"},
		{"nothing anywhere is empty", nil, walk(), ""},
	}
	for _, c := range cases {
		if got := chooseLanIP(c.route, c.walk); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
