package tui

import (
	"slices"
	"testing"
)

// TestKeyMap_Bindings is a regression guard: every binding has the
// expected key strings so a typo in keys.go can't silently break a key
// the user relies on. Bound keys are listed verbatim here — vim keys
// come first (primary), with the arrow / home / end aliases following
// (kept bound but hidden from in-app help).
func TestKeyMap_Bindings(t *testing.T) {
	k := newKeyMap()
	cases := []struct {
		name string
		keys []string
		got  []string
	}{
		{"Filter", []string{"/"}, k.Filter.Keys()},
		{"SortCycle", []string{"s"}, k.SortCycle.Keys()},
		{"SortReverse", []string{"S"}, k.SortReverse.Keys()},
		{"GroupCycle", []string{"tab"}, k.GroupCycle.Keys()},
		{"JumpTop", []string{"g", "home"}, k.JumpTop.Keys()},
		{"JumpBottom", []string{"G", "end"}, k.JumpBottom.Keys()},
		{"HalfUp", []string{"ctrl+u"}, k.HalfUp.Keys()},
		{"HalfDown", []string{"ctrl+d"}, k.HalfDown.Keys()},
		{"Up", []string{"k", "up"}, k.Up.Keys()},
		{"Down", []string{"j", "down"}, k.Down.Keys()},
		{"Refresh", []string{"r"}, k.Refresh.Keys()},
		{"Enter", []string{"enter"}, k.Enter.Keys()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Equal(tc.keys, tc.got) {
				t.Errorf("%s keys = %v; want %v", tc.name, tc.got, tc.keys)
			}
		})
	}
}
