package polecat

// op-uhd2 / hq-khtga incident-derived tests for the hard liveness gate on
// destructive polecat reuse.
//
// Incident shape (2026-07-25 16:55Z, capital/basalt): a deferred-but-delivered
// bead was re-fed every 30s; dispatch landed on a phantom-idle polecat whose
// session was LIVE (holding for approval); reuse-prep killed the session and
// hard-reset the live clone, deleting the working branch. The gate must
// refuse reuse whenever the tmux session exists — and must NOT kill it.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

func TestReuseIdlePolecat_RefusesLiveSessionWithoutKilling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux not supported on Windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	townRoot := t.TempDir()
	rigName := "testlivegate"
	rigPath := filepath.Join(townRoot, rigName)
	polecatName := "basalt"

	polecatDir := filepath.Join(rigPath, "polecats", polecatName)
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir polecat dir: %v", err)
	}

	reg := session.NewPrefixRegistry()
	reg.Register("gt", rigName)
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })

	tm := tmux.NewTmux()
	r := &rig.Rig{Name: rigName, Path: rigPath}
	mgr := NewManager(r, git.NewGit(rigPath), tm)

	// The incident shape: a LIVE session (holding polecat awaiting approval —
	// or any session at all; the gate must not care what is running in it).
	sessMgr := NewSessionManager(tm, r)
	sessionName := sessMgr.SessionName(polecatName)
	if err := tm.NewSessionWithCommand(sessionName, townRoot, "sleep 300"); err != nil {
		t.Fatalf("create tmux session: %v", err)
	}
	t.Cleanup(func() { _ = tm.KillSessionWithProcesses(sessionName) })

	if running, err := tm.HasSession(sessionName); err != nil || !running {
		t.Fatalf("precondition: session %s should be running", sessionName)
	}

	_, reuseErr := mgr.ReuseIdlePolecat(polecatName, AddOptions{})

	if reuseErr == nil {
		t.Fatal("ReuseIdlePolecat must refuse while the tmux session exists — resetting a live clone is the hq-khtga material-damage incident")
	}
	if !errors.Is(reuseErr, ErrPolecatNeedsRecovery) {
		t.Fatalf("refusal must be ErrPolecatNeedsRecovery so the allocator picks another slot, got: %v", reuseErr)
	}
	if !strings.Contains(reuseErr.Error(), sessionName) {
		t.Errorf("refusal should name the live session: %v", reuseErr)
	}

	// The gate must be non-destructive: the live session survives untouched.
	if running, _ := tm.HasSession(sessionName); !running {
		t.Fatal("the live session must NOT be killed by a refused reuse — reuse never clears sessions (op-uhd2)")
	}
}

func TestReuseIdlePolecat_StaleHeartbeatSessionStillRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux not supported on Windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	townRoot := t.TempDir()
	rigName := "teststalegate"
	rigPath := filepath.Join(townRoot, rigName)
	polecatName := "marmite"

	polecatDir := filepath.Join(rigPath, "polecats", polecatName)
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir polecat dir: %v", err)
	}

	reg := session.NewPrefixRegistry()
	reg.Register("gt", rigName)
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })

	tm := tmux.NewTmux()
	r := &rig.Rig{Name: rigName, Path: rigPath}
	mgr := NewManager(r, git.NewGit(rigPath), tm)

	sessMgr := NewSessionManager(tm, r)
	sessionName := sessMgr.SessionName(polecatName)
	if err := tm.NewSessionWithCommand(sessionName, townRoot, "sleep 300"); err != nil {
		t.Fatalf("create tmux session: %v", err)
	}
	t.Cleanup(func() { _ = tm.KillSessionWithProcesses(sessionName) })

	// A STALE heartbeat in state "exiting" — the strongest available
	// "this slot looks abandoned" evidence. Staleness is deliberately
	// non-discriminating for the gate: basalt's holding session ALSO looked
	// stale/idle from the outside, and killing on that evidence is what
	// reset a live clone (hq-khtga instance #4). Session existence alone
	// decides.
	dir := filepath.Join(townRoot, ".runtime", "heartbeats")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-10 * time.Minute).UTC()
	data := []byte(`{"timestamp":"` + oldTime.Format(time.RFC3339Nano) + `","state":"exiting"}`)
	if err := os.WriteFile(filepath.Join(dir, sessionName+".json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	_, reuseErr := mgr.ReuseIdlePolecat(polecatName, AddOptions{})

	if !errors.Is(reuseErr, ErrPolecatNeedsRecovery) {
		t.Fatalf("expected ErrPolecatNeedsRecovery for existing session regardless of heartbeat staleness, got: %v", reuseErr)
	}

	// Non-destructive refusal: session and heartbeat survive untouched —
	// teardown belongs to the daemon reaper/witness, never to reuse-prep.
	if running, _ := tm.HasSession(sessionName); !running {
		t.Fatal("stale-heartbeat session must NOT be killed by a refused reuse (op-uhd2)")
	}
	if hb := ReadSessionHeartbeat(townRoot, sessionName); hb == nil {
		t.Error("heartbeat must not be removed by a refused reuse")
	}
}

func TestReuseIdlePolecat_NoSessionPassesLivenessGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux not supported on Windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	townRoot := t.TempDir()
	rigName := "testlivegatefree"
	rigPath := filepath.Join(townRoot, rigName)
	polecatName := "jam"

	polecatDir := filepath.Join(rigPath, "polecats", polecatName)
	if err := os.MkdirAll(polecatDir, 0755); err != nil {
		t.Fatalf("mkdir polecat dir: %v", err)
	}

	reg := session.NewPrefixRegistry()
	reg.Register("gt", rigName)
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })

	tm := tmux.NewTmux()
	r := &rig.Rig{Name: rigName, Path: rigPath}
	mgr := NewManager(r, git.NewGit(rigPath), tm)

	// No session exists: the liveness gate must pass through; the error (if
	// any) comes from later steps (no real git repo in this fixture), never
	// from the gate.
	_, reuseErr := mgr.ReuseIdlePolecat(polecatName, AddOptions{})
	if reuseErr != nil && strings.Contains(reuseErr.Error(), "refusing to reset a live polecat clone") {
		t.Fatalf("liveness gate fired with no session present: %v", reuseErr)
	}
}

func TestShouldSendReuseRefusalAlert_Debounces(t *testing.T) {
	townRoot := t.TempDir()
	now := time.Now()

	if !shouldSendReuseRefusalAlert(townRoot, "gt-basalt", now) {
		t.Fatal("first refusal must alert")
	}
	if shouldSendReuseRefusalAlert(townRoot, "gt-basalt", now.Add(30*time.Second)) {
		t.Fatal("a refusal 30s later must be debounced — the stranded feed loop fires every 30s")
	}
	if !shouldSendReuseRefusalAlert(townRoot, "gt-basalt", now.Add(reuseRefusalAlertDebounce+time.Minute)) {
		t.Fatal("refusals past the debounce window must alert again")
	}
	if !shouldSendReuseRefusalAlert(townRoot, "gt-other", now.Add(time.Minute)) {
		t.Fatal("debounce is per-session — a different slot must alert")
	}
}
