package route

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

// TestIsDefaultRoute guards the detection of IPv4 default routes. The library
// reports them with a non-nil 0.0.0.0/0 Dst (not nil), so the check must key on
// the mask length, not Dst == nil.
func TestIsDefaultRoute(t *testing.T) {
	cases := []struct {
		name string
		dst  *net.IPNet
		want bool
	}{
		{"nil dst", nil, true},
		{"0.0.0.0/0", &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}, true},
		{"192.168.1.0/24", &net.IPNet{IP: net.IPv4(192, 168, 1, 0), Mask: net.CIDRMask(24, 32)}, false},
		{"10.0.0.0/8", &net.IPNet{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)}, false},
		{"1.1.1.1/32", &net.IPNet{IP: net.IPv4(1, 1, 1, 1), Mask: net.CIDRMask(32, 32)}, false},
	}
	for _, c := range cases {
		if got := isDefaultRoute(&netlink.Route{Dst: c.dst}); got != c.want {
			t.Errorf("%s: isDefaultRoute = %v, want %v", c.name, got, c.want)
		}
	}
}
