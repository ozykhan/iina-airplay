package main

import (
	"net"
	"testing"
)

func mustCIDR(t *testing.T, s string) net.Addr {
	ip, _, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return &net.IPAddr{IP: ip}
}

func TestPickIPv4(t *testing.T) {
	cases := []struct {
		addrs []net.Addr
		want  string
	}{
		{[]net.Addr{mustCIDR(t, "127.0.0.1/8")}, ""},                                  // loopback rejected
		{[]net.Addr{mustCIDR(t, "169.254.10.1/16")}, ""},                              // link-local rejected
		{[]net.Addr{mustCIDR(t, "fe80::1/64"), mustCIDR(t, "192.168.1.18/24")}, "192.168.1.18"}, // v6 skipped
		{[]net.Addr{mustCIDR(t, "10.0.0.7/8")}, "10.0.0.7"},
	}
	for i, c := range cases {
		if got := pickIPv4(c.addrs); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
}
