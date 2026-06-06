// Command simplewand is a minimal two-WAN failover daemon for OpenWrt.
//
// It pings a single target out of each WAN (bound to that WAN's device), tracks
// health with a sliding window, and reorders the IPv4 default routes in the
// main table so the preferred healthy WAN wins. It never blackholes traffic: if
// no WAN looks healthy it leaves routing untouched, and if the daemon dies the
// last-good routes simply remain in place.
package main

import (
	"context"
	"flag"
	"log"
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

// version is set at build time via -X main.version (see the OpenWrt Makefile).
var version = "dev"

const (
	// pingTimeout bounds a single echo; must be < fsm.ProbeInterval.
	pingTimeout = 1500 * time.Millisecond
	// demoteOffset is added to push a demoted WAN above the selected one.
	demoteOffset = 1000
	// default base metrics when netifd has none configured (primary < backup).
	defaultPrimaryMetric = 10
	defaultBackupMetric  = 20
)

type wan struct {
	name    string // netifd interface
	primary bool
	base    int // configured netifd metric
	device  string
	rttms   int64

	// diagnostics: latch one-shot log lines so a persistent condition does
	// not repeat on every probe.
	lastDevice string
	resolveErr bool
	linkErr    bool
	noRoute    bool
}

type controller struct {
	mu sync.Mutex

	cfg     *config.Config
	wans    []*wan
	machine *fsm.Machine
	pinger  *ping.Pinger
	seq     uint16

	selected     string
	lastSwitch   time.Time
	lastSwitchTo string
}

func newController(cfg *config.Config) *controller {
	c := &controller{cfg: cfg, pinger: ping.New()}

	pBase, pSet := route.IfaceMetric(cfg.Primary, defaultPrimaryMetric)
	bBase, bSet := route.IfaceMetric(cfg.Backup, defaultBackupMetric)
	if !pSet || !bSet {
		log.Printf("warning: set the netifd route metrics (network.%s.metric < network.%s.metric) "+
			"so the resting routing state matches; otherwise the daemon must keep correcting it",
			cfg.Primary, cfg.Backup)
	} else if pBase >= bBase {
		log.Printf("warning: primary %q metric (%d) is not lower than backup %q metric (%d); "+
			"the primary will not be preferred at rest", cfg.Primary, pBase, cfg.Backup, bBase)
	}
	primary := &wan{name: cfg.Primary, primary: true, base: pBase}
	backup := &wan{name: cfg.Backup, base: bBase}
	c.wans = []*wan{primary, backup}

	c.machine = fsm.New([]*fsm.WAN{
		{Name: primary.name, Priority: 1},
		{Name: backup.name, Priority: 2},
	}, time.Duration(cfg.RecoveryTime)*time.Second)
	return c
}

// desiredMetric computes the target metric for each WAN given the selection.
// The selected WAN keeps its base metric (matching netifd, so no churn); a WAN
// that would otherwise outrank the selected one is demoted above it.
func (c *controller) desiredMetric(selected string) map[string]int {
	selBase := 0
	for _, w := range c.wans {
		if w.name == selected {
			selBase = w.base
		}
	}
	out := map[string]int{}
	for _, w := range c.wans {
		if selected == "" || w.name == selected || w.base > selBase {
			out[w.name] = w.base
		} else {
			out[w.name] = selBase + demoteOffset
		}
	}
	return out
}

func (c *controller) enforceLocked() {
	if c.selected == "" {
		// We have not validated any WAN yet (startup, or just after a reload).
		// Leave the routing table exactly as it is: whatever netifd or a prior
		// daemon instance left in place is already a working state, and resetting
		// to base metrics here would flip traffic onto the primary before we know
		// it is healthy — undoing an in-effect failover across a restart.
		return
	}
	desired := c.desiredMetric(c.selected)
	for _, w := range c.wans {
		if w.device == "" {
			continue
		}
		idx, err := route.LinkIndex(w.device)
		if err != nil {
			continue // device not present: WAN is down, nothing to reorder
		}
		if _, err := route.EnsureMetric(idx, desired[w.name]); err != nil {
			log.Printf("interface %s (%s): enforce metric %d: %v",
				w.name, w.device, desired[w.name], err)
		}
	}
}

func (c *controller) enforce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enforceLocked()
}

// probe pings the WAN. It returns false without recording a sample when the
// device is absent or has no default route (the WAN is hard-down); the caller
// then marks it down so a clean window greets it when it returns. Each
// transition is logged once, so a WAN stuck offline is diagnosable from logread.
func (c *controller) probe(w *wan) (sampled, ok bool) {
	if w.device == "" {
		d, err := route.ResolveDevice(w.name)
		if err != nil {
			if !w.resolveErr {
				log.Printf("interface %s: cannot resolve its L3 device (is it up?): %v", w.name, err)
				w.resolveErr = true
			}
			return false, false
		}
		w.resolveErr = false
		if w.lastDevice != d {
			log.Printf("interface %s: using device %s", w.name, d)
			w.lastDevice = d
		}
		w.device = d
	}
	idx, err := route.LinkIndex(w.device)
	if err != nil {
		if !w.linkErr {
			log.Printf("interface %s: device %s not present: %v", w.name, w.device, err)
			w.linkErr = true
		}
		w.device = "" // re-resolve next tick in case it changed
		return false, false
	}
	w.linkErr = false
	if !route.HasDefault(idx) {
		if !w.noRoute {
			log.Printf("interface %s (%s): no IPv4 default route in the main table; treating as down", w.name, w.device)
			w.noRoute = true
		}
		w.device = "" // re-resolve next tick in case it changed
		return false, false
	}
	w.noRoute = false
	rtt, err := c.pinger.Once(w.device, c.cfg.PingTarget, c.seq, pingTimeout)
	if err == nil {
		w.rttms = rtt.Milliseconds()
	}
	return true, err == nil
}

func (c *controller) tick(ctx context.Context) {
	now := time.Now()

	c.mu.Lock()
	c.seq++
	for _, w := range c.wans {
		if sampled, ok := c.probe(w); sampled {
			c.machine.Observe(w.name, ok, now)
		} else {
			c.machine.MarkDown(w.name)
		}
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
		return "WAN selected: " + selected,
			"simplewan selected upstream " + selected + " at " + ts + "."
	}
	return "WAN failover: " + prev + " -> " + selected,
		"simplewan switched upstream from " + prev + " to " + selected +
			" at " + ts + " (previous upstream unhealthy, or a more-preferred one recovered)."
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
	fws := c.machine.WANs()
	for _, w := range c.wans {
		var online bool
		var loss int = 100
		for _, f := range fws {
			if f.Name == w.name {
				online = f.Online()
				loss = f.LossPercent()
			}
		}
		// Report the metric actually in the kernel, falling back to the metric
		// we would impose if the WAN currently has no default route. The two
		// agree once enforcement has converged; before that (e.g. just after a
		// restart, while selected is still empty) this shows the real state
		// rather than a metric we have not applied.
		metric := desired[w.name]
		hasRoute := false
		if idx, err := route.LinkIndex(w.device); err == nil {
			if m, ok := route.DefaultMetric(idx); ok {
				metric, hasRoute = m, true
			}
		}
		doc.Ifaces = append(doc.Ifaces, status.Iface{
			Name:     w.name,
			IfName:   w.name,
			Device:   w.device,
			Primary:  w.primary,
			Online:   online,
			Selected: w.name == c.selected,
			HasRoute: hasRoute,
			Metric:   metric,
			LossPct:  loss,
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
	// change (e.g. netifd re-adds a route after a DHCP/PPPoE renewal, or as
	// soon as the primary's PPPoE link comes up at boot).
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
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	ticker := time.NewTicker(fsm.ProbeInterval)
	defer ticker.Stop()

	log.Printf("started (version %s): target=%s primary=%s backup=%s recovery=%ds",
		version, cfg.PingTarget, cfg.Primary, cfg.Backup, cfg.RecoveryTime)
	c.tick(ctx)
	for {
		select {
		case <-ticker.C:
			c.tick(ctx)
		case s := <-sig:
			// A config change reaches us as a full restart from procd (the init
			// script gates on `enabled` and leaves the routes in place across
			// the restart), so there is no in-process reload to do: any signal
			// we are asked to handle means stop.
			log.Printf("signal %s: exiting (routes left in place)", s)
			return
		}
	}
}
