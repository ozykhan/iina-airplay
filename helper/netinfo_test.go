package main

import (
	"net"
	"strings"
	"testing"
)

func mustCIDR(t *testing.T, s string) net.Addr {
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return &net.IPNet{IP: ip, Mask: n.Mask}
}

func testInterface(t *testing.T, name, cidr string) interfaceInfo {
	return interfaceInfo{name: name, flags: net.FlagUp, addrs: []net.Addr{mustCIDR(t, cidr)}}
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
	cases := []struct {
		name   string
		route  net.IP
		ifaces []interfaceInfo
		want   string
	}{
		{
			"default route beats earlier-indexed interface",
			net.ParseIP("192.168.1.18"),
			[]interfaceInfo{testInterface(t, "bridge100", "10.8.0.2/24"), testInterface(t, "en0", "192.168.1.18/24")},
			"192.168.1.18",
		},
		{
			"utun default route prefers physical private address",
			net.ParseIP("10.9.0.2"),
			[]interfaceInfo{testInterface(t, "utun0", "10.9.0.2/32"), testInterface(t, "en0", "192.168.50.10/24")},
			"192.168.50.10",
		},
		{
			"utun route ignores public physical address",
			net.ParseIP("10.9.0.2"),
			[]interfaceInfo{testInterface(t, "utun0", "10.9.0.2/32"), testInterface(t, "en0", "203.0.113.2/24")},
			"10.9.0.2",
		},
		{
			"non-utun private route stays preferred",
			net.ParseIP("10.8.0.2"),
			[]interfaceInfo{testInterface(t, "bridge100", "10.8.0.2/24"), testInterface(t, "en0", "192.168.1.18/24")},
			"10.8.0.2",
		},
		{"no default route falls back to walk", nil, []interfaceInfo{testInterface(t, "en0", "192.168.1.18/24")}, "192.168.1.18"},
		{"loopback route falls back to walk", net.ParseIP("127.0.0.1"), []interfaceInfo{testInterface(t, "en0", "192.168.1.18/24")}, "192.168.1.18"},
		{"link-local route falls back to walk", net.ParseIP("169.254.10.1"), []interfaceInfo{testInterface(t, "en0", "192.168.1.18/24")}, "192.168.1.18"},
		{"v6 route falls back to walk", net.ParseIP("2001:db8::1"), []interfaceInfo{testInterface(t, "en0", "192.168.1.18/24")}, "192.168.1.18"},
		{"nothing anywhere is empty", nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := chooseLanIP(c.route, c.ifaces); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestValidateIPOverride(t *testing.T) {
	ifaces := []interfaceInfo{testInterface(t, "en0", "192.168.50.10/24")}
	if got, err := validateIPOverride("192.168.50.10", ifaces); err != nil || got != "192.168.50.10" {
		t.Fatalf("valid override: got %q, err %v", got, err)
	}
	for _, override := range []string{"not-an-ip", "127.0.0.1", "192.168.50.11"} {
		if _, err := validateIPOverride(override, ifaces); err == nil || !strings.Contains(err.Error(), "override") {
			t.Errorf("override %q: expected validation error, got %v", override, err)
		}
	}
}

func TestValidateIPOverrideRejectsDownInterface(t *testing.T) {
	iface := testInterface(t, "en0", "192.168.50.10/24")
	iface.flags &^= net.FlagUp
	got, err := validateIPOverride("192.168.50.10", []interfaceInfo{iface})
	if err == nil || !strings.Contains(err.Error(), "interface en0 is down") {
		t.Fatalf("expected a down-interface error, got IP %q, err %v", got, err)
	}
	if got != "" {
		t.Errorf("rejected override returned IP %q", got)
	}
}
