// Command simplewand is a minimal two-WAN failover daemon for OpenWrt.
//
// It pings a single target out of each WAN (bound to that WAN's device), tracks
// health with hysteresis, and reorders the IPv4 default routes in the main
// table so the preferred healthy WAN wins. It never blackholes traffic: if no
// WAN looks healthy it leaves routing untouched, and if the daemon dies the
// last-good routes simply remain in place.
package main

import (
	"context"
	"flag"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/filippo-claude/simplewan/internal/config"
	"github.com/filippo-claude/simplewan/internal/fsm"
	"github.com/filippo-claude/simplewan/internal/notify"
	"github.com/filippo-claude/simplewan/internal/ping"
	"github.com/filippo-claude/simplewan/internal/route"
	"github.com/filippo-claude/simplewan/internal/status"
)

type wan struct {
	cfg    config.Iface
	device string
	lastOK bool
	loss   int
	rttms  int64
}

type controller struct {
	mu sync.Mutex

	cfg     *config.Config
	wans    []*wan
	machine *fsm.Machine
	pinger  *ping.Pinger

	selected     string
	lastSwitch   time.Time
	lastSwitchTo string
}

func newController(cfg *config.Config) *controller {
	c := &controller{cfg: cfg, pinger: ping.New()}
	var fwans []*fsm.WAN
	for _, it := range cfg.Ifaces {
		w := &wan{cfg: it}
		dev := it.Device
		if dev == "" {
			if d, err := route.ResolveDevice(it.IfName); err != nil {
				log.Printf("interface %s: cannot resolve device yet: %v", it.Name, err)
			} else {
				dev = d
			}
		}
		w.device = dev
		c.wans = append(c.wans, w)
		fwans = append(fwans, &fsm.WAN{Name: it.Name, Priority: it.Priority})
	}
	c.machine = fsm.New(fwans, cfg.Up, cfg.Down, time.Duration(cfg.RecoveryTime)*time.Second)
	return c
}

// desiredMetric computes the target metric for each WAN given the selection.
// The selected WAN (and any WAN already worse than it) keeps its base metric;
// any WAN that would otherwise be preferred over the selected one is demoted.
func (c *controller) desiredMetric(selected string) map[string]int {
	selMetric := math.MaxInt
	if selected != "" {
		for _, w := range c.wans {
			if w.cfg.Name == selected {
				selMetric = w.cfg.Metric
			}
		}
	}
	out := map[string]int{}
	for _, w := range c.wans {
		if selected == "" || w.cfg.Name == selected || w.cfg.Metric > selMetric {
			out[w.cfg.Name] = w.cfg.Metric
		} else {
			out[w.cfg.Name] = w.cfg.Metric + c.cfg.DemoteOffset
		}
	}
	return out
}

// enforce applies the desired metrics. It is idempotent, so it is safe to call
// both on each tick and from the route-change reconciler.
func (c *controller) enforce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enforceLocked()
}

func (c *controller) enforceLocked() {
	desired := c.desiredMetric(c.selected)
	for _, w := range c.wans {
		if w.device == "" {
			continue
		}
		idx, err := route.LinkIndex(w.device)
		if err != nil {
			continue // device not present: WAN is down, nothing to reorder
		}
		if _, err := route.EnsureMetric(idx, desired[w.cfg.Name]); err != nil {
			log.Printf("interface %s (%s): enforce metric %d: %v",
				w.cfg.Name, w.device, desired[w.cfg.Name], err)
		}
	}
}

func (c *controller) probe(w *wan) bool {
	if w.device == "" {
		if d, err := route.ResolveDevice(w.cfg.IfName); err == nil {
			w.device = d
		} else {
			return false
		}
	}
	res := c.pinger.Check(w.device, c.cfg.PingTarget, c.cfg.Count, time.Duration(c.cfg.Timeout)*time.Second)
	w.loss = res.LossPercent()
	w.rttms = res.RTT.Milliseconds()
	w.lastOK = res.Received > 0
	return w.lastOK
}

func (c *controller) tick(ctx context.Context) {
	now := time.Now()

	c.mu.Lock()
	for _, w := range c.wans {
		ok := c.probe(w)
		c.machine.Observe(w.cfg.Name, ok, now)
	}
	selected, changed := c.machine.Decide(now)
	prev := c.selected
	c.selected = selected
	if changed {
		c.lastSwitch = now
		c.lastSwitchTo = selected
	}
	c.enforceLocked()
	c.mu.Unlock()

	if changed {
		c.onSwitch(ctx, prev, selected)
	}
	c.writeStatus()
}

func (c *controller) onSwitch(ctx context.Context, prev, selected string) {
	log.Printf("selection changed: %q -> %q", prev, selected)

	if c.cfg.FlushConntrack {
		if err := route.FlushConntrack(); err != nil {
			log.Printf("flush conntrack: %v", err)
		}
	}

	n := notify.Config{
		Enabled:       c.cfg.Notify.Enabled,
		Token:         c.cfg.Notify.Token,
		From:          c.cfg.Notify.From,
		To:            c.cfg.Notify.To,
		SubjectPrefix: c.cfg.Notify.SubjectPrefix,
	}
	subject, body := switchMessage(prev, selected)
	go func() {
		if err := n.Send(ctx, subject, body); err != nil {
			log.Printf("notify: %v", err)
		}
	}()
}

func switchMessage(prev, selected string) (subject, body string) {
	ts := time.Now().Format(time.RFC1123)
	if prev == "" {
		subject = "WAN selected: " + selected
		body = "simplewan selected upstream " + selected + " at " + ts + "."
		return
	}
	subject = "WAN failover: " + prev + " -> " + selected
	body = "simplewan switched upstream from " + prev + " to " + selected +
		" at " + ts + " (previous upstream unhealthy or a more-preferred one recovered)."
	return
}

func (c *controller) writeStatus() {
	c.mu.Lock()
	defer c.mu.Unlock()
	desired := c.desiredMetric(c.selected)
	doc := &status.Doc{
		Enabled:      c.cfg.Enabled,
		PingTarget:   c.cfg.PingTarget,
		Selected:     c.selected,
		LastSwitchTo: c.lastSwitchTo,
	}
	if !c.lastSwitch.IsZero() {
		doc.LastSwitch = c.lastSwitch.Unix()
	}
	for _, w := range c.wans {
		hasRoute := false
		if idx, err := route.LinkIndex(w.device); err == nil {
			hasRoute = route.HasDefault(idx)
		}
		fw := c.machine.WANs()
		online := false
		for _, f := range fw {
			if f.Name == w.cfg.Name {
				online = f.Online()
			}
		}
		doc.Ifaces = append(doc.Ifaces, status.Iface{
			Name:     w.cfg.Name,
			IfName:   w.cfg.IfName,
			Device:   w.device,
			Priority: w.cfg.Priority,
			Online:   online,
			Selected: w.cfg.Name == c.selected,
			HasRoute: hasRoute,
			Metric:   desired[w.cfg.Name],
			LossPct:  w.loss,
			RTTms:    w.rttms,
		})
	}
	if err := status.Write(doc); err != nil {
		log.Printf("write status: %v", err)
	}
}

func main() {
	cfgPath := flag.String("config", config.DefaultPath, "path to UCI config")
	flag.Parse()
	log.SetFlags(0)
	log.SetPrefix("simplewand: ")

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if !cfg.Enabled {
		log.Printf("disabled in config; exiting")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newController(cfg)

	// Reconciler: re-assert our intent whenever the kernel's default routes
	// change (e.g. netifd re-adds a route after a DHCP/PPPoE renewal).
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	defer close(done)
	if err := route.Subscribe(wake, done); err != nil {
		log.Printf("route monitor unavailable, relying on periodic enforcement: %v", err)
	}
	go func() {
		for {
			select {
			case <-wake:
				c.enforce()
			case <-ctx.Done():
				return
			}
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	ticker := time.NewTicker(time.Duration(cfg.Interval) * time.Second)
	defer ticker.Stop()

	log.Printf("started: target=%s interval=%ds interfaces=%d", cfg.PingTarget, cfg.Interval, len(cfg.Ifaces))
	c.tick(ctx)
	for {
		select {
		case <-ticker.C:
			c.tick(ctx)
		case s := <-sig:
			switch s {
			case syscall.SIGHUP:
				newCfg, err := config.Load(*cfgPath)
				if err != nil {
					log.Printf("reload: %v (keeping current config)", err)
					continue
				}
				nc := newController(newCfg)
				c.mu.Lock()
				// Adopt the new config and freshly-built state; routes are
				// left as-is (the new tick will reconverge).
				c.cfg = nc.cfg
				c.wans = nc.wans
				c.machine = nc.machine
				c.selected = ""
				c.lastSwitch = time.Time{}
				c.lastSwitchTo = ""
				c.mu.Unlock()
				log.Printf("reloaded config")
			default:
				log.Printf("signal %s: exiting (routes left in place)", s)
				return
			}
		}
	}
}
