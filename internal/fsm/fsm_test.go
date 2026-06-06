package fsm

import (
	"testing"
	"time"
)

func newTwo() *Machine {
	return New([]*WAN{
		{Name: "primary", Priority: 1},
		{Name: "backup", Priority: 2},
	}, 2, 2, 60*time.Second)
}

func observeN(m *Machine, name string, ok bool, n int, t time.Time) {
	for i := 0; i < n; i++ {
		m.Observe(name, ok, t)
	}
}

func TestPrefersPrimaryWhenBothHealthy(t *testing.T) {
	m := newTwo()
	t0 := time.Unix(1000, 0)
	observeN(m, "primary", true, 2, t0)
	observeN(m, "backup", true, 2, t0)
	sel, changed := m.Decide(t0)
	if sel != "primary" || !changed {
		t.Fatalf("got (%q,%v), want (primary,true)", sel, changed)
	}
}

func TestImmediateFailover(t *testing.T) {
	m := newTwo()
	t0 := time.Unix(1000, 0)
	observeN(m, "primary", true, 2, t0)
	observeN(m, "backup", true, 2, t0)
	m.Decide(t0)

	// primary fails enough to go offline; failover must be immediate.
	observeN(m, "primary", false, 2, t0)
	observeN(m, "backup", true, 1, t0)
	sel, changed := m.Decide(t0)
	if sel != "backup" || !changed {
		t.Fatalf("got (%q,%v), want (backup,true)", sel, changed)
	}
}

func TestRecoveryHoldBeforeSwitchBack(t *testing.T) {
	m := newTwo()
	t0 := time.Unix(1000, 0)
	observeN(m, "primary", true, 2, t0)
	observeN(m, "backup", true, 2, t0)
	m.Decide(t0)
	observeN(m, "primary", false, 2, t0)
	m.Decide(t0) // -> backup

	// primary comes back at t1; should NOT switch back until recovery elapses.
	t1 := t0.Add(10 * time.Second)
	observeN(m, "primary", true, 2, t1)
	observeN(m, "backup", true, 1, t1)
	if sel, _ := m.Decide(t1); sel != "backup" {
		t.Fatalf("switched back too early: %q", sel)
	}
	if sel, _ := m.Decide(t1.Add(59 * time.Second)); sel != "backup" {
		t.Fatalf("switched back before recovery hold: %q", sel)
	}
	if sel, changed := m.Decide(t1.Add(60 * time.Second)); sel != "primary" || !changed {
		t.Fatalf("did not switch back after recovery hold: (%q,%v)", sel, changed)
	}
}

func TestNoneHealthyHoldsSelection(t *testing.T) {
	m := newTwo()
	t0 := time.Unix(1000, 0)
	observeN(m, "primary", true, 2, t0)
	observeN(m, "backup", true, 2, t0)
	m.Decide(t0) // -> primary

	// both go offline; the daemon must hold the last selection (fail-open).
	observeN(m, "primary", false, 2, t0)
	observeN(m, "backup", false, 2, t0)
	sel, changed := m.Decide(t0)
	if sel != "primary" || changed {
		t.Fatalf("got (%q,%v), want (primary,false) held", sel, changed)
	}
}
