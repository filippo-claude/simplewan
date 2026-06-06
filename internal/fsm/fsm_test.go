package fsm

import (
	"testing"
	"time"
)

var t0 = time.Unix(1000, 0)

func feed(w *WAN, ok bool, n int) {
	for i := 0; i < n; i++ {
		w.push(ok, t0)
	}
}

func TestOnlineAfterCleanSamples(t *testing.T) {
	w := &WAN{Name: "p", Priority: 1}
	feed(w, true, minSamplesOnline)
	if !w.Online() {
		t.Fatalf("should be online after %d clean samples", minSamplesOnline)
	}
}

func TestDeadLinkGoesOfflineFast(t *testing.T) {
	w := &WAN{Name: "p", Priority: 1}
	feed(w, true, WindowSize) // online
	feed(w, false, DeadRun)
	if w.Online() {
		t.Fatalf("should be offline after %d consecutive misses", DeadRun)
	}
}

func TestHighLossGoesOffline(t *testing.T) {
	w := &WAN{Name: "p", Priority: 1}
	feed(w, true, WindowSize) // online, 0%% loss
	// 3 lost out of 30 == 10%%, fewer than DeadRun in a row, so it's the
	// loss path (not the dead path) that must trip.
	feed(w, false, 3)
	if w.consecutiveMisses() >= DeadRun {
		t.Fatalf("test setup wrong: looks like a dead link")
	}
	if w.Online() {
		t.Fatalf("should be offline at >= %d%%%% loss", LossThreshold)
	}
}

func newMachine() *Machine {
	return New([]*WAN{
		{Name: "primary", Priority: 1},
		{Name: "backup", Priority: 2},
	}, 60*time.Second)
}

func observe(m *Machine, name string, ok bool, n int) {
	for i := 0; i < n; i++ {
		m.Observe(name, ok, t0)
	}
}

func TestColdBootAdoptsPrimaryWithoutHold(t *testing.T) {
	m := newMachine()
	// Boot: primary device absent, backup up.
	m.MarkDown("primary")
	observe(m, "backup", true, minSamplesOnline)
	if sel, _ := m.Decide(t0); sel != "backup" {
		t.Fatalf("expected backup at boot, got %q", sel)
	}
	// Primary finishes negotiating and validates; adopt it immediately
	// (no recovery hold, because we never failed over from it).
	observe(m, "primary", true, minSamplesOnline)
	observe(m, "backup", true, 1)
	if sel, changed := m.Decide(t0); sel != "primary" || !changed {
		t.Fatalf("expected immediate primary adoption, got (%q,%v)", sel, changed)
	}
}

func TestFailoverIsImmediate(t *testing.T) {
	m := newMachine()
	observe(m, "primary", true, minSamplesOnline)
	observe(m, "backup", true, minSamplesOnline)
	m.Decide(t0) // -> primary
	observe(m, "primary", false, DeadRun)
	observe(m, "backup", true, 1)
	if sel, changed := m.Decide(t0); sel != "backup" || !changed {
		t.Fatalf("expected immediate failover to backup, got (%q,%v)", sel, changed)
	}
}

func TestRecoveryHoldAfterRealFailover(t *testing.T) {
	m := newMachine()
	observe(m, "primary", true, minSamplesOnline)
	observe(m, "backup", true, minSamplesOnline)
	m.Decide(t0) // -> primary
	observe(m, "primary", false, DeadRun)
	m.Decide(t0) // -> backup, primary on probation

	// Primary recovers (device dropped then returned -> clean window).
	m.MarkDown("primary")
	t1 := t0.Add(10 * time.Second)
	for i := 0; i < minSamplesOnline; i++ {
		m.Observe("primary", true, t1)
	}
	observe(m, "backup", true, 1)
	if sel, _ := m.Decide(t1); sel != "backup" {
		t.Fatalf("switched back before recovery hold: %q", sel)
	}
	if sel, _ := m.Decide(t1.Add(59 * time.Second)); sel != "backup" {
		t.Fatalf("switched back too early: %q", sel)
	}
	if sel, changed := m.Decide(t1.Add(60 * time.Second)); sel != "primary" || !changed {
		t.Fatalf("did not switch back after hold: (%q,%v)", sel, changed)
	}
}

func TestNoneHealthyHoldsSelection(t *testing.T) {
	m := newMachine()
	observe(m, "primary", true, minSamplesOnline)
	observe(m, "backup", true, minSamplesOnline)
	m.Decide(t0) // -> primary
	m.MarkDown("primary")
	m.MarkDown("backup")
	if sel, changed := m.Decide(t0); sel != "primary" || changed {
		t.Fatalf("got (%q,%v), want held (primary,false)", sel, changed)
	}
}
