package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// installCapacityAgentListStub installs a bd stub whose agent-bead listing is
// controlled by the caller: agentIDs are the beads it will report.
// Every other bd list (the active-work query) answers with an empty array.
func installCapacityAgentListStub(t *testing.T, agentIDs ...string) {
	t.Helper()

	rows := make([]string, 0, len(agentIDs))
	for _, id := range agentIDs {
		rows = append(rows, fmt.Sprintf(`{"id":"%s","title":"agent","status":"open","issue_type":"agent","labels":["gt:agent"],"description":"cleanup_status: clean\nagent_state: idle\n"}`, id))
	}
	agentJSON := "[" + strings.Join(rows, ",") + "]"

	binDir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
AGENT_JSON=%q
case "$*" in
  *version*) echo "bd 1.0.5"; exit 0 ;;
esac
case "$*" in
  *gt:agent*) printf '%%s\n' "$AGENT_JSON"; exit 0 ;;
  *"mol wisp list"*) printf '{"wisps":[]}\n'; exit 0 ;;
  *list*) printf '[]\n'; exit 0 ;;
esac
exit 0
`, agentJSON)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestCapacitySnapshotCountsMissingAgentBeads covers op-etpz fix #2.
//
// A polecat directory whose agent bead cannot be read produces nil fields,
// which forces cleanup_status="" -> CleanupUnknown -> !IsSafe() ->
// recovery-blocked. Before the comma-ok that outcome was indistinguishable from
// a genuinely broken seat: the map lookup missed, nothing errored, and the
// snapshot reported a confident number built from data it never had.
//
// Both verdicts run in one test so neither arm can pass on a dead probe.
func TestCapacitySnapshotCountsMissingAgentBeads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a Unix shell stub for bd")
	}

	t.Run("missing agent bead is counted and reported", func(t *testing.T) {
		townRoot := setupPolecatCapacityRig(t, 4)
		if err := os.MkdirAll(filepath.Join(townRoot, "gastown", "polecats", "furiosa"), 0755); err != nil {
			t.Fatalf("mkdir polecat: %v", err)
		}
		installCapacityAgentListStub(t) // no agent beads at all

		snapshot, err := polecatCapacitySnapshotForTown(townRoot)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if snapshot.MissingAgentBeads != 1 {
			t.Fatalf("MissingAgentBeads = %d, want 1 (snapshot = %+v)", snapshot.MissingAgentBeads, snapshot)
		}
		// The seat lands in recovery-blocked and consumes a slot — this is the
		// harm the counter now makes visible rather than the counter's cause.
		if snapshot.RecoveryBlocked != 1 {
			t.Fatalf("RecoveryBlocked = %d, want 1 (snapshot = %+v)", snapshot.RecoveryBlocked, snapshot)
		}
	})

	t.Run("readable agent bead is not counted", func(t *testing.T) {
		townRoot := setupPolecatCapacityRig(t, 4)
		if err := os.MkdirAll(filepath.Join(townRoot, "gastown", "polecats", "furiosa"), 0755); err != nil {
			t.Fatalf("mkdir polecat: %v", err)
		}
		agentID := beads.PolecatBeadIDWithPrefix(beads.GetPrefixForRig(townRoot, "gastown"), "gastown", "furiosa")
		installCapacityAgentListStub(t, agentID)

		snapshot, err := polecatCapacitySnapshotForTown(townRoot)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if snapshot.MissingAgentBeads != 0 {
			t.Fatalf("MissingAgentBeads = %d, want 0 (snapshot = %+v)", snapshot.MissingAgentBeads, snapshot)
		}
		if snapshot.ReusableIdle != 1 {
			t.Fatalf("ReusableIdle = %d, want 1 — a readable clean idle seat must reach the reusable arm (snapshot = %+v)", snapshot.ReusableIdle, snapshot)
		}
	})
}

// TestPolecatCapacityAdmissionErrorNamesUnreadableAgentBeads asserts a denial
// says when it was computed from incomplete input, so nobody reads it as a real
// capacity ceiling.
func TestPolecatCapacityAdmissionErrorNamesUnreadableAgentBeads(t *testing.T) {
	withMissing := &polecatCapacityAdmissionError{
		Snapshot: polecatCapacitySnapshot{Max: 4, RecoveryBlocked: 4, MissingAgentBeads: 3, capacityUsed: 4},
		Reason:   "configured scheduler.max_polecats capacity is full",
	}
	if got := withMissing.Error(); !strings.Contains(got, "3 polecat agent bead(s) were not found") {
		t.Fatalf("admission error does not name the unreadable beads: %s", got)
	}

	// Adverse arm: a denial computed from complete input must not carry the
	// warning, or the warning stops meaning anything.
	clean := &polecatCapacityAdmissionError{
		Snapshot: polecatCapacitySnapshot{Max: 4, Working: 4, capacityUsed: 4},
		Reason:   "configured scheduler.max_polecats capacity is full",
	}
	if got := clean.Error(); strings.Contains(got, "were not found") {
		t.Fatalf("admission error carries the warning with no missing beads: %s", got)
	}
}
