package cmd

// op-uhd2 / hq-khtga incident-derived tests: deferred beads must never be
// feed-eligible. cap-ww8 (completed work, MR merged, status stuck at
// deferred) was counted "ready" by the stranded scan and re-fed to the rig
// every 30s for 2 days — every dispatch died downstream (executeSling's
// deferred gate; capacity dispatch only slings status=open), so the feed
// loop never converged, and one landing on a phantom-idle polecat reset a
// live clone.

import "testing"

func TestIsReadyIssue_DeferredNeverReady(t *testing.T) {
	tests := []struct {
		name string
		in   trackedIssueInfo
	}{
		{
			// The exact cap-ww8 shape: deferred, unassigned (its polecat
			// finished and moved on), unblocked.
			name: "deferred unassigned issue (cap-ww8 shape)",
			in: trackedIssueInfo{
				Status:  "deferred",
				Blocked: false,
			},
		},
		{
			name: "deferred with dead assignee",
			in: trackedIssueInfo{
				Status:   "deferred",
				Blocked:  false,
				Assignee: "capital/polecats/basalt",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if isReadyIssue(tc.in, nil) {
				t.Fatalf("deferred issue counted ready — this re-creates the 30s convoy feed loop (op-uhd2)")
			}
		})
	}
}
