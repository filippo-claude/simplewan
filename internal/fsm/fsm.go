// Package fsm tracks per-WAN health with hysteresis and decides which WAN
// should be selected, applying a recovery hold before switching back to a
// more-preferred link.
package fsm

import "time"

// WAN is one tracked upstream. Priority is lower-is-better.
type WAN struct {
	Name     string
	Priority int

	online      bool
	upStreak    int
	downStreak  int
	healthySnce time.Time // when it most recently became online
}

// Online reports the current debounced state.
func (w *WAN) Online() bool { return w.online }

// Machine holds all WANs and the current selection.
type Machine struct {
	wans     []*WAN
	up       int // good checks needed offline->online
	down     int // failed checks needed online->offline
	recovery time.Duration

	selected string // Name of the currently selected WAN ("" until first decision)
}

// New builds a Machine. up/down are check counts; recovery is the hold time
// before switching back to a more-preferred WAN.
func New(wans []*WAN, up, down int, recovery time.Duration) *Machine {
	if up < 1 {
		up = 1
	}
	if down < 1 {
		down = 1
	}
	return &Machine{wans: wans, up: up, down: down, recovery: recovery}
}

// WANs exposes the tracked WANs (stable order).
func (m *Machine) WANs() []*WAN { return m.wans }

// Selected returns the currently selected WAN name.
func (m *Machine) Selected() string { return m.selected }

func (m *Machine) byName(name string) *WAN {
	for _, w := range m.wans {
		if w.Name == name {
			return w
		}
	}
	return nil
}

// Observe feeds one check result for a WAN and updates its debounced state.
func (m *Machine) Observe(name string, ok bool, now time.Time) {
	w := m.byName(name)
	if w == nil {
		return
	}
	if ok {
		w.downStreak = 0
		w.upStreak++
		if !w.online && w.upStreak >= m.up {
			w.online = true
			w.healthySnce = now
		}
	} else {
		w.upStreak = 0
		w.downStreak++
		if w.online && w.downStreak >= m.down {
			w.online = false
		}
	}
}

// Decide returns the WAN that should be selected, honoring the recovery hold.
//
// Rules, in order:
//   - If nothing is healthy, keep the current selection (fail-open: never
//     blackhole; degrade to "no failover", never to "no connectivity").
//   - If the current selection is down, switch to the best healthy WAN
//     immediately (failover must not wait).
//   - Otherwise only switch to a strictly more-preferred WAN once it has been
//     healthy for the recovery hold (anti-flap on switch-back).
func (m *Machine) Decide(now time.Time) (name string, changed bool) {
	best := m.bestHealthy()
	prev := m.selected

	switch {
	case best == nil:
		// no healthy WAN; hold last selection
	case prev == "":
		m.selected = best.Name
	default:
		cur := m.byName(prev)
		switch {
		case cur == nil || !cur.online:
			m.selected = best.Name // current gone/unhealthy: immediate failover
		case best.Priority < cur.Priority:
			// switching back to a more-preferred WAN: require recovery hold
			if now.Sub(best.healthySnce) >= m.recovery {
				m.selected = best.Name
			}
		}
		// if cur is healthy and at least as preferred as best, keep it
	}
	return m.selected, m.selected != prev
}

// bestHealthy returns the lowest-priority online WAN, or nil.
func (m *Machine) bestHealthy() *WAN {
	var best *WAN
	for _, w := range m.wans {
		if !w.online {
			continue
		}
		if best == nil || w.Priority < best.Priority {
			best = w
		}
	}
	return best
}
