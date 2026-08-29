package main

import "testing"

// A freshly provisioned unit is the most normal thing this tool ever prints,
// and both of its counter-read failures are expected states rather than faults.
// Rendering nft's raw diagnostic for either one says "something is broken"
// about a healthy box — and both are multi-line, which silently misaligns every
// tabwriter column below them.
func TestNFTErrorRow(t *testing.T) {
	const (
		missingTable = "Error: No such file or directory\nlist counters table inet combiner\n         ^^^^^^^^: exit status 1"
		notRoot      = "Operation not permitted (you must be root)\nnetlink: Error: cache initialization failed: Operation not permitted: exit status 1"
	)
	tests := []struct {
		name       string
		holding    bool
		err        string
		wantLabel  string
		wantDetail string
	}{
		{"held unit has no ruleset yet", true, missingTable, "nft_status", "no ruleset yet — applied at go-live"},
		// Permission wins over the hold: a non-root reader cannot tell whether
		// a ruleset exists, so sudo is the step that reveals which it is.
		{"held and not root", true, notRoot, "nft_status", "needs root to read counters — run: sudo combiner-status"},
		{"live and not root", false, notRoot, "nft_status", "needs root to read counters — run: sudo combiner-status"},
		{"live, genuine fault, flattened to one row", false, missingTable, "nft_error",
			"Error: No such file or directory list counters table inet combiner ^^^^^^^^: exit status 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			label, detail := nftErrorRow(tc.holding, tc.err)
			if label != tc.wantLabel || detail != tc.wantDetail {
				t.Errorf("nftErrorRow(%v, ...) = %q, %q; want %q, %q", tc.holding, label, detail, tc.wantLabel, tc.wantDetail)
			}
		})
	}
}

func TestFlattenKeepsErrorsOnOneRow(t *testing.T) {
	if got := flatten("a\nb\n   c  d\n"); got != "a b c d" {
		t.Errorf("flatten = %q, want %q", got, "a b c d")
	}
}
