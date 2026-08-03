package beads

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// installMockBDStoreScopedAgentList installs a bd stub that answers agent-bead
// queries differently depending on which store it was pointed at:
//
//	BEADS_DIR == <townRoot>/.beads   ->  one agent bead   (agent beads live in town)
//	anything else (a rig store)      ->  zero agent beads (the real-world state)
//
// It also records every invocation's BEADS_DIR so a test can assert which store
// was reached, not merely what came back.
func installMockBDStoreScopedAgentList(t *testing.T, townBeadsDir, agentID, logPath string) {
	t.Helper()

	binDir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
TOWN_BEADS_DIR=%q
AGENT_ID=%q
LOG=%q
printf 'beads_dir=%%s args=%%s\n' "${BEADS_DIR:-<unset>}" "$*" >> "$LOG"
cmd=""
for arg in "$@"; do
  case "$arg" in
    --*) ;;
    *) cmd="$arg"; break ;;
  esac
done
case "$cmd" in
  version)
    echo "bd 1.0.5"
    exit 0
    ;;
  list)
    if [ "${BEADS_DIR:-}" = "$TOWN_BEADS_DIR" ]; then
      printf '[{"id":"%%s","title":"agent","status":"open","issue_type":"agent","labels":["gt:agent"]}]\n' "$AGENT_ID"
    else
      printf '[]\n'
    fi
    exit 0
    ;;
  mol)
    printf '{"wisps":[]}\n'
    exit 0
    ;;
esac
exit 0
`, townBeadsDir, agentID, logPath)

	bdPath := filepath.Join(binDir, "bd")
	if err := os.WriteFile(bdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write mock bd: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// setupAgentListRedirectTown builds a town containing one rig, returning the
// resolved town root and the rig's worker directory.
func setupAgentListRedirectTown(t *testing.T) (townRoot, townBeadsDir, rigDir, rigBeadsDir string) {
	t.Helper()

	townRoot, _ = filepath.EvalSymlinks(t.TempDir())
	townBeadsDir = filepath.Join(townRoot, ".beads")
	rigDir = filepath.Join(townRoot, "gastown", "mayor", "rig")
	rigBeadsDir = filepath.Join(rigDir, ".beads")
	for _, dir := range []string{filepath.Join(townRoot, "mayor"), townBeadsDir, rigBeadsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	if err := WriteRoutes(townBeadsDir, []Route{{Prefix: "hq-", Path: "."}, {Prefix: "gt-", Path: "gastown/mayor/rig"}}); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	return townRoot, townBeadsDir, rigDir, rigBeadsDir
}

// TestListAgentBeadsRedirectsToTownStore is the regression test for op-etpz.
//
// Both verdicts are produced in one run so the harness controls itself:
//   - the redirecting arm (a rig-scoped wrapper, as the capacity path builds
//     with beads.New(rigPath)) must reach the TOWN store and see the agent bead
//   - the adverse arm (a wrapper pinned to the rig store) must see zero
//
// Before the fix both arms returned zero, and the empty result carried no
// error, so every caller read "this agent does not exist".
func TestListAgentBeadsRedirectsToTownStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a Unix shell stub for bd")
	}

	townRoot, townBeadsDir, rigDir, rigBeadsDir := setupAgentListRedirectTown(t)
	agentID := "gt-gastown-polecat-furiosa"
	logPath := filepath.Join(t.TempDir(), "bd.log")
	installMockBDStoreScopedAgentList(t, townBeadsDir, agentID, logPath)

	// Adverse arm first: pinned to the rig store, so no redirect can occur.
	// This proves the stub is able to answer "no agent beads here".
	rigOnly := &Beads{workDir: rigDir, beadsDir: rigBeadsDir, noRoute: true}
	rigAgents, err := rigOnly.ListAgentBeads()
	if err != nil {
		t.Fatalf("adverse arm ListAgentBeads: %v", err)
	}
	if len(rigAgents) != 0 {
		t.Fatalf("adverse arm (rig store) returned %d agent beads, want 0 — the stub cannot express the rig-store state", len(rigAgents))
	}

	// Redirecting arm: exactly how internal/cmd/polecat_capacity.go builds it.
	rigScoped := New(rigDir)
	agents, err := rigScoped.ListAgentBeads()
	if err != nil {
		t.Fatalf("ListAgentBeads: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("ListAgentBeads from a rig-scoped wrapper returned %d agent beads, want 1 (town redirect missing)", len(agents))
	}
	if _, ok := agents[agentID]; !ok {
		t.Fatalf("ListAgentBeads did not return %s; got %v", agentID, agents)
	}
	if got := rigScoped.getTownRoot(); got != townRoot {
		t.Fatalf("getTownRoot() = %q, want %q", got, townRoot)
	}
}

// TestListAgentBeadsFromWispsRedirectsToTownStore covers the second accessor.
// The wisp list is the fallback existence source, so a rig-scoped call that
// silently returns nothing hides agent beads that exist only as wisps.
func TestListAgentBeadsFromWispsRedirectsToTownStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a Unix shell stub for bd")
	}

	_, townBeadsDir, rigDir, _ := setupAgentListRedirectTown(t)
	agentID := "gt-gastown-polecat-furiosa"
	logPath := filepath.Join(t.TempDir(), "bd.log")

	binDir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
TOWN_BEADS_DIR=%q
AGENT_ID=%q
LOG=%q
printf 'beads_dir=%%s args=%%s\n' "${BEADS_DIR:-<unset>}" "$*" >> "$LOG"
cmd=""
for arg in "$@"; do
  case "$arg" in
    --*) ;;
    *) cmd="$arg"; break ;;
  esac
done
case "$cmd" in
  version) echo "bd 1.0.5"; exit 0 ;;
  list) printf '[]\n'; exit 0 ;;
  mol)
    if [ "${BEADS_DIR:-}" = "$TOWN_BEADS_DIR" ]; then
      printf '{"wisps":[{"id":"%%s","status":"open","issue_type":"agent","labels":["gt:agent"]}]}\n' "$AGENT_ID"
    else
      printf '{"wisps":[]}\n'
    fi
    exit 0
    ;;
esac
exit 0
`, townBeadsDir, agentID, logPath)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(script), 0755); err != nil {
		t.Fatalf("write mock bd: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	wisps, err := New(rigDir).ListAgentBeadsFromWisps()
	if err != nil {
		t.Fatalf("ListAgentBeadsFromWisps: %v", err)
	}
	if len(wisps) != 1 {
		t.Fatalf("ListAgentBeadsFromWisps from a rig-scoped wrapper returned %d wisps, want 1 (town redirect missing)", len(wisps))
	}
	if _, ok := wisps[agentID]; !ok {
		t.Fatalf("ListAgentBeadsFromWisps did not return %s; got %v", agentID, wisps)
	}
}
