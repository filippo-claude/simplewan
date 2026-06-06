// Package fsm tracks per-WAN health from a sliding window of probe results and
// decides which WAN should be selected, applying a recovery hold before
// switching back to a more-preferred WAN that previously failed.
//
// The health rules are fixed (not configurable):
//   - probes arrive every ProbeInterval;
//   - a WAN goes offline on DeadRun consecutive misses (a hard-down link,
//     caught in ~DeadRun*ProbeInterval), or when loss over the window reaches
//     LossThreshold (sustained high loss caught over ~Window*ProbeInterval);
//   - it comes back online once the window holds enough samples, the most
//     recent probe succeeded, and loss is back below LossThreshold.
package fsm

import "time"

const (
	// ProbeInterval is the cadence at which the daemon feeds samples.
	ProbeInterval = 2 * time.Second
	// WindowSize is the number of samples retained (~60s at ProbeInterval).
	WindowSize = 30
	// DeadRun is the consecutive-miss count that declares a dead link (~10s).
	DeadRun = 5
	// LossThreshold is the packet-loss percentage that flips a WAN, in both
	// directions (offline at >= threshold, online again below it).
	LossThreshold = 10
	// minSamplesOnline gates the initial online verdict (~10s of probing).
	minSamplesOnline = DeadRun
	// minSamplesLoss gates the loss-based offline verdict so a near-empty
	// window can't trip it; a genuinely dead link is caught by DeadRun.
	minSamplesLoss = 10
)

// WAN is one tracked upstream. Priority is lower-is-better.
type WAN struct {
	Name     string
	Priority int

	samples     []bool // ring of recent results, true = reply received
	online      bool
	healthySnce time.Time // when it most recently became online
	probation   bool      // true after we failed over away from it (gates switch-back)
}

// Online reports the current debounced state.
func (w *WAN) Online() bool { return w.online }

// LossPercent returns loss over the current window (100 if no samples yet).
func (w *WAN) LossPercent() int {
	if len(w.samples) == 0 {
		return 100
	}
	lost := 0
	for _, ok := range w.samples {
		if !ok {
			lost++
		}
	}
	return lost * 100 / len(w.samples)
}

func (w *WAN) consecutiveMisses() int {
	n := 0
	for i := len(w.samples) - 1; i >= 0; i-- {
		if w.samples[i] {
			break
		}
		n++
	}
	return n
}

func (w *WAN) push(ok bool, now time.Time) {
	w.samples = append(w.samples, ok)
	if len(w.samples) > WindowSize {
		w.samples = w.samples[len(w.samples)-WindowSize:]
	}

	n := len(w.samples)
	loss := w.LossPercent()
	misses := w.consecutiveMisses()

	if w.online {
		if misses >= DeadRun || (n >= minSamplesLoss && loss >= LossThreshold) {
			w.online = false
		}
	} else {
		if n >= minSamplesOnline && misses == 0 && loss < LossThreshold {
			w.online = true
			w.healthySnce = now
		}
	}
}

// Machine holds all WANs and the current selection.
type Machine struct {
	wans     []*WAN
	recovery time.Duration
	selected string
}

// New builds a Machine. recovery is the hold before switching back to a
// more-preferred WAN that previously failed.
func New(wans []*WAN, recovery time.Duration) *Machine {
	return &Machine{wans: wans, recovery: recovery}
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

// Observe feeds one probe result for a WAN.
func (m *Machine) Observe(name string, ok bool, now time.Time) {
	if w := m.byName(name); w != nil {
		w.push(ok, now)
	}
}

// MarkDown records that a WAN's device is absent or has no route. The WAN is
// taken offline and its window is cleared, so once it returns it is validated
// from a clean slate (fast adoption) rather than waiting for old failures to
// age out of the window. Probation is preserved.
func (m *Machine) MarkDown(name string) {
	if w := m.byName(name); w != nil {
		w.online = false
		w.samples = nil
	}
}

// Decide returns the WAN that should be selected.
//
//   - If nothing is healthy, keep the current selection (fail-open: never
//     blackhole; degrade to "no failover", never to "no connectivity").
//   - If the current selection is down, switch to the best healthy WAN
//     immediately and mark the one we leave as on-probation so a later
//     switch-back honors the recovery hold.
//   - A more-preferred WAN that is on probation must be healthy for the
//     recovery hold before we switch back to it. A more-preferred WAN that is
//     NOT on probation (e.g. the primary first coming up at boot) is adopted
//     immediately.
func (m *Machine) Decide(now time.Time) (name string, changed bool) {
	best := m.bestHealthy()
	prev := m.selected

	switch {
	case best == nil:
		// no healthy WAN; hold last selection
	case prev == "":
		m.selected = best.Name
		best.probation = false
	case prev == best.Name:
		// already on the best WAN
	default:
		cur := m.byName(prev)
		switch {
		case cur == nil || !cur.online:
			// current selection failed: switch immediately.
			if cur != nil && cur.Priority < best.Priority {
				cur.probation = true // more-preferred link that just dropped
			}
			m.selected = best.Name
			best.probation = false
		case best.Priority < cur.Priority:
			// switching back to a more-preferred WAN.
			if !best.probation || now.Sub(best.healthySnce) >= m.recovery {
				m.selected = best.Name
				best.probation = false
			}
		}
		// else cur is healthy and at least as preferred as best: keep it
	}
	return m.selected, m.selected != prev
}

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
