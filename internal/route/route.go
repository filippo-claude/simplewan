// Package route enforces which WAN is preferred by adjusting IPv4 default-route
// metrics in the main table, and watches the kernel so it can re-assert its
// intent after netifd churns the routes (DHCP/PPPoE renewals).
//
// Safety invariant: this package only ever *reorders* existing default routes.
// It never installs a blackhole/unreachable route and never leaves the table
// without a default route. Metric changes are applied make-before-break (add
// the desired metric, then prune the others) so there is never a moment with
// zero default routes for a live WAN.
package route

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// IfaceMetric returns the configured netifd route metric for an interface
// (uci network.<iface>.metric), or dflt if it is unset or unparseable. This is
// the base metric the daemon keeps the interface at when it is selected, so the
// resting state matches what netifd installs and needs no correction.
func IfaceMetric(iface string, dflt int) int {
	out, err := exec.Command("uci", "-q", "get", fmt.Sprintf("network.%s.metric", iface)).Output()
	if err != nil {
		return dflt
	}
	if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
		return v
	}
	return dflt
}

// ResolveDevice returns the L3 device for a netifd interface via ubus.
// It is only used when the config does not pin a device explicitly.
func ResolveDevice(ifname string) (string, error) {
	out, err := exec.Command("ubus", "call", fmt.Sprintf("network.interface.%s", ifname), "status").Output()
	if err != nil {
		return "", fmt.Errorf("ubus status %s: %w", ifname, err)
	}
	// Minimal extraction of "l3_device": "<dev>" without pulling a JSON dep
	// for one field. The value is a bare device name with no escaping.
	const key = "\"l3_device\""
	i := strings.Index(string(out), key)
	if i < 0 {
		return "", fmt.Errorf("no l3_device for %s", ifname)
	}
	rest := string(out)[i+len(key):]
	if j := strings.IndexByte(rest, ':'); j >= 0 {
		rest = rest[j+1:]
	}
	a := strings.IndexByte(rest, '"')
	if a < 0 {
		return "", fmt.Errorf("malformed l3_device for %s", ifname)
	}
	rest = rest[a+1:]
	b := strings.IndexByte(rest, '"')
	if b < 0 {
		return "", fmt.Errorf("malformed l3_device for %s", ifname)
	}
	return rest[:b], nil
}

func defaultRoutesByLink() (map[int][]netlink.Route, error) {
	routes, err := netlink.RouteList(nil, unix.AF_INET)
	if err != nil {
		return nil, err
	}
	byLink := map[int][]netlink.Route{}
	for _, r := range routes {
		if r.Dst != nil { // default route has a nil destination
			continue
		}
		if r.Table != 0 && r.Table != unix.RT_TABLE_MAIN {
			continue
		}
		byLink[r.LinkIndex] = append(byLink[r.LinkIndex], r)
	}
	return byLink, nil
}

// EnsureMetric makes the device's default route exist at exactly metric `want`.
//
// It returns (changed, error). If the device currently has no default route
// (link down) it does nothing — there is nothing to reorder, and the WAN is
// offline anyway.
func EnsureMetric(linkIndex int, want int) (bool, error) {
	byLink, err := defaultRoutesByLink()
	if err != nil {
		return false, err
	}
	existing := byLink[linkIndex]
	if len(existing) == 0 {
		return false, nil
	}

	hasWant := false
	for _, r := range existing {
		if r.Priority == want {
			hasWant = true
			break
		}
	}

	changed := false
	// Make-before-break: add the desired metric first.
	if !hasWant {
		nr := existing[0]
		nr.Priority = want
		nr.ILinkIndex = 0
		if err := netlink.RouteAdd(&nr); err != nil {
			return false, fmt.Errorf("add default metric %d on link %d: %w", want, linkIndex, err)
		}
		changed = true
	}
	// Then prune any default routes on this link at other metrics.
	for _, r := range existing {
		if r.Priority == want {
			continue
		}
		rc := r
		if err := netlink.RouteDel(&rc); err != nil {
			return changed, fmt.Errorf("del default metric %d on link %d: %w", r.Priority, linkIndex, err)
		}
		changed = true
	}
	return changed, nil
}

// LinkIndex returns the kernel ifindex for a device name.
func LinkIndex(dev string) (int, error) {
	l, err := netlink.LinkByName(dev)
	if err != nil {
		return 0, err
	}
	return l.Attrs().Index, nil
}

// HasDefault reports whether the device currently has any IPv4 default route.
func HasDefault(linkIndex int) bool {
	byLink, err := defaultRoutesByLink()
	if err != nil {
		return false
	}
	return len(byLink[linkIndex]) > 0
}

// Subscribe delivers a token on ch whenever an IPv4 route changes. The caller
// re-runs its (idempotent) enforcement on each token. Closing done stops it.
func Subscribe(ch chan<- struct{}, done <-chan struct{}) error {
	updates := make(chan netlink.RouteUpdate, 16)
	if err := netlink.RouteSubscribe(updates, done); err != nil {
		return err
	}
	go func() {
		for u := range updates {
			if u.Route.Dst != nil {
				continue // only care about default-route churn
			}
			select {
			case ch <- struct{}{}:
			default: // a wake-up is already pending; coalesce
			}
		}
	}()
	return nil
}

// FlushConntrack drops the conntrack table so existing flows re-establish over
// the newly selected WAN. Best-effort; missing module is not an error.
func FlushConntrack() error {
	const f = "/proc/net/nf_conntrack"
	if _, err := os.Stat(f); err != nil {
		return nil
	}
	return os.WriteFile(f, []byte("f"), 0)
}
