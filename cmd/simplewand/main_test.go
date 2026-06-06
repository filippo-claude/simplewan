package main

import "testing"

// TestDesiredMetric pins down the demote/keep logic: the selected WAN keeps its
// base metric (matching netifd, so no churn) and any WAN that would otherwise
// outrank it is pushed above it by demoteOffset.
func TestDesiredMetric(t *testing.T) {
	mk := func(pBase, bBase int) *controller {
		return &controller{wans: []*wan{
			{name: "wan", primary: true, base: pBase},
			{name: "wan2", base: bBase},
		}}
	}
	cases := []struct {
		name         string
		pBase, bBase int
		selected     string
		want         map[string]int
	}{
		{
			name:  "primary selected keeps base ordering",
			pBase: 10, bBase: 20, selected: "wan",
			want: map[string]int{"wan": 10, "wan2": 20},
		},
		{
			name:  "backup selected demotes primary above it",
			pBase: 10, bBase: 20, selected: "wan2",
			want: map[string]int{"wan": 20 + demoteOffset, "wan2": 20},
		},
		{
			name:  "nothing selected leaves both at base",
			pBase: 10, bBase: 20, selected: "",
			want: map[string]int{"wan": 10, "wan2": 20},
		},
		{
			name:  "misconfigured primary>backup still wins when selected",
			pBase: 30, bBase: 20, selected: "wan",
			want: map[string]int{"wan": 30, "wan2": 30 + demoteOffset},
		},
		{
			name:  "equal base metrics: selected wins, other demoted",
			pBase: 20, bBase: 20, selected: "wan",
			want: map[string]int{"wan": 20, "wan2": 20 + demoteOffset},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mk(tc.pBase, tc.bBase).desiredMetric(tc.selected)
			if len(got) != len(tc.want) {
				t.Fatalf("desiredMetric(%q) = %v, want %v", tc.selected, got, tc.want)
			}
			for name, want := range tc.want {
				if got[name] != want {
					t.Errorf("desiredMetric(%q)[%q] = %d, want %d", tc.selected, name, got[name], want)
				}
			}
		})
	}
}
