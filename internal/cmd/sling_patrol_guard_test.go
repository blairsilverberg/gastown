package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Patrol-formula dispatch guard tests (op-s473).
//
// Incident 2026-07-22: the daemon dispatch path slung a deacon patrol wisp
// (attached_formula: mol-deacon-patrol, dispatched_by: deacon) to a fresh
// polecat. These tests pin the guard that refuses patrol work on every sling
// path that can reach a polecat.

func TestSlingTargetsPolecat(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{"openclaw/polecats/furiosa", true},
		{"openclaw/polecats", true},
		{"deacon", false},
		{"witness", false},
		{"mayor", false},
		{"gastown/crew/max", false},
		{"gastown/witness", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := slingTargetsPolecat(tt.target); got != tt.want {
			t.Errorf("slingTargetsPolecat(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}

func TestBeadPatrolFormula(t *testing.T) {
	tests := []struct {
		name string
		info *beadInfo
		want string
	}{
		{"nil info", nil, ""},
		{"empty description", &beadInfo{}, ""},
		{
			"patrol wisp",
			&beadInfo{Description: "attached_formula: mol-deacon-patrol\ndispatched_by: deacon"},
			"mol-deacon-patrol",
		},
		{
			"normal polecat work",
			&beadInfo{Description: "attached_molecule: op-wisp-a6v\nattached_formula: mol-polecat-work"},
			"",
		},
		{
			"patrol mentioned mid-line only",
			&beadInfo{Description: "Wisp op-wisp-1d8 (attached_formula: mol-deacon-patrol) was mis-slung"},
			"",
		},
	}
	for _, tt := range tests {
		if got := beadPatrolFormula(tt.info); got != tt.want {
			t.Errorf("%s: beadPatrolFormula() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCheckPatrolDispatchGuard(t *testing.T) {
	patrolWisp := &beadInfo{Description: "attached_formula: mol-deacon-patrol\ndispatched_by: deacon"}
	workBead := &beadInfo{Description: "attached_formula: mol-polecat-work"}

	// Patrol formula to a polecat target: refused.
	if err := checkPatrolDispatchGuard("mol-deacon-patrol", "gt-abc", "openclaw/polecats/furiosa", workBead); err == nil {
		t.Error("patrol formula to polecat target should be refused")
	}
	// Patrol formula to the pool address: refused.
	if err := checkPatrolDispatchGuard("mol-witness-patrol", "gt-abc", "openclaw/polecats", workBead); err == nil {
		t.Error("patrol formula to polecat pool should be refused")
	}
	// Patrol wisp re-sling: refused regardless of target.
	if err := checkPatrolDispatchGuard("", "op-wisp-1d8", "deacon", patrolWisp); err == nil {
		t.Error("patrol wisp re-sling should be refused even to a role agent")
	}
	// Normal work bead to a polecat: allowed.
	if err := checkPatrolDispatchGuard("mol-polecat-work", "gt-abc", "openclaw/polecats/furiosa", workBead); err != nil {
		t.Errorf("normal work to polecat should pass, got: %v", err)
	}
	// Patrol formula to a role agent (legit flow, e.g. gt sling mol-deacon-patrol deacon): allowed.
	if err := checkPatrolDispatchGuard("mol-deacon-patrol", "gt-abc", "deacon", workBead); err != nil {
		t.Errorf("patrol formula to role agent should pass, got: %v", err)
	}
}

func patrolGuardTestTown(t *testing.T, bdShowJSON string) string {
	t.Helper()
	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	bdScript := `#!/bin/sh
case "$1" in
  show)
    printf '%s\n' '` + bdShowJSON + `'
    ;;
esac
exit 0
`
	writeBDStub(t, binDir, bdScript, "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return townRoot
}

// TestExecuteSling_PatrolFormulaRefused verifies that executeSling refuses to
// dispatch a patrol formula — scheduler/batch dispatch always lands on a rig
// polecat.
func TestExecuteSling_PatrolFormulaRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"Patrol loop","status":"open","assignee":"","description":""}]`)

	params := SlingParams{
		BeadID:      "test-patrol1",
		RigName:     "testrig",
		FormulaName: "mol-deacon-patrol",
		TownRoot:    townRoot,
	}

	result, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when dispatching patrol formula, got nil")
	}
	if result.ErrMsg != "patrol formula" {
		t.Errorf("expected ErrMsg='patrol formula', got %q", result.ErrMsg)
	}
	if !strings.Contains(err.Error(), "patrol formulas must never run on polecats") {
		t.Errorf("error should explain the patrol constraint: %v", err)
	}
}

// TestExecuteSling_PatrolWispRefused verifies that executeSling refuses a bead
// carrying patrol attachment metadata (the op-s473 incident shape: a deacon
// patrol wisp re-dispatched by the daemon).
func TestExecuteSling_PatrolWispRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"mol-deacon-patrol","status":"open","assignee":"","description":"attached_formula: mol-deacon-patrol\ndispatched_by: deacon"}]`)

	params := SlingParams{
		BeadID:   "op-wisp-test1",
		RigName:  "testrig",
		TownRoot: townRoot,
	}

	result, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when dispatching patrol wisp, got nil")
	}
	if result.ErrMsg != "patrol wisp" {
		t.Errorf("expected ErrMsg='patrol wisp', got %q", result.ErrMsg)
	}
	if !strings.Contains(err.Error(), "patrol work must never be re-dispatched") {
		t.Errorf("error should explain the patrol constraint: %v", err)
	}
}

// TestExecuteSling_PatrolWisp_ForceDoesNotBypass verifies --force does not
// bypass the patrol guard. Patrol work on a polecat is never valid.
func TestExecuteSling_PatrolWisp_ForceDoesNotBypass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"mol-deacon-patrol","status":"open","assignee":"","description":"attached_formula: mol-deacon-patrol\ndispatched_by: deacon"}]`)

	params := SlingParams{
		BeadID:      "op-wisp-test2",
		RigName:     "testrig",
		FormulaName: "mol-deacon-patrol",
		TownRoot:    townRoot,
		Force:       true,
	}

	_, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when force-dispatching patrol work, got nil")
	}
	if !strings.Contains(err.Error(), "op-s473") {
		t.Errorf("--force should not bypass patrol guard: %v", err)
	}
}

// TestRunSling_PatrolFormulaToRigRefused is the end-to-end incident test
// (op-s473): slinging a patrol formula at a rig must be refused before any
// polecat is spawned, in both direct and deferred dispatch modes.
func TestRunSling_PatrolFormulaToRigRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	for _, deferred := range []bool{false, true} {
		name := "direct"
		if deferred {
			name = "deferred"
		}
		t.Run(name, func(t *testing.T) {
			townRoot := t.TempDir()

			mayorDir := filepath.Join(townRoot, "mayor")
			if err := os.MkdirAll(mayorDir, 0o755); err != nil {
				t.Fatalf("mkdir mayor: %v", err)
			}
			if err := os.WriteFile(filepath.Join(mayorDir, "town.json"), []byte(`{"name":"test","version":2}`), 0o644); err != nil {
				t.Fatalf("write town.json: %v", err)
			}
			rigsJSON := `{"version":1,"rigs":{"testrig":{"git_url":"file:///dev/null"}}}`
			if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0o644); err != nil {
				t.Fatalf("write rigs.json: %v", err)
			}
			// IsRigName requires the rig directory to exist (rig.Manager.loadRig).
			if err := os.MkdirAll(filepath.Join(townRoot, "testrig"), 0o755); err != nil {
				t.Fatalf("mkdir testrig: %v", err)
			}
			if deferred {
				settingsDir := filepath.Join(townRoot, "settings")
				if err := os.MkdirAll(settingsDir, 0o755); err != nil {
					t.Fatalf("mkdir settings: %v", err)
				}
				settingsJSON := `{"version":1,"scheduler":{"max_polecats":10,"batch_size":3}}`
				if err := os.WriteFile(filepath.Join(townRoot, "settings", "config.json"), []byte(settingsJSON), 0o644); err != nil {
					t.Fatalf("write settings: %v", err)
				}
			}
			if err := os.MkdirAll(filepath.Join(townRoot, ".beads"), 0o755); err != nil {
				t.Fatalf("mkdir .beads: %v", err)
			}

			// Stub bd: formula lookup succeeds, bead lookup fails (standalone formula mode).
			binDir := filepath.Join(townRoot, "bin")
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				t.Fatalf("mkdir binDir: %v", err)
			}
			bdScript := `#!/bin/sh
case "$1" in
  formula) printf '%s\n' '{"name":"mol-deacon-patrol"}'; exit 0 ;;
  show)    exit 1 ;;
esac
exit 0
`
			writeBDStub(t, binDir, bdScript, "")
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			rigDir := filepath.Join(townRoot, "mayor", "rig")
			if err := os.MkdirAll(rigDir, 0o755); err != nil {
				t.Fatalf("mkdir rig: %v", err)
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			t.Cleanup(func() { _ = os.Chdir(cwd) })
			if err := os.Chdir(rigDir); err != nil {
				t.Fatalf("chdir: %v", err)
			}

			t.Setenv(EnvGTRole, "mayor")
			t.Setenv("GT_POLECAT", "")
			t.Setenv("GT_CREW", "")
			t.Setenv("TMUX_PANE", "")
			t.Setenv("GT_TEST_NO_NUDGE", "1")
			t.Setenv("GT_TEST_SKIP_HOOK_VERIFY", "1")

			prevDryRun := slingDryRun
			prevNoConvoy := slingNoConvoy
			prevVars := slingVars
			prevOnTarget := slingOnTarget
			t.Cleanup(func() {
				slingDryRun = prevDryRun
				slingNoConvoy = prevNoConvoy
				slingVars = prevVars
				slingOnTarget = prevOnTarget
			})
			slingDryRun = false // the guard must fire before any spawn, even without dry-run
			slingNoConvoy = true
			slingVars = nil
			slingOnTarget = ""

			err = runSling(nil, []string{"mol-deacon-patrol", "testrig"})
			if err == nil {
				t.Fatal("expected patrol formula sling to a rig to be refused, got nil")
			}
			if !strings.Contains(err.Error(), "patrol formulas must never run on polecats") {
				t.Errorf("expected patrol guard error, got: %v", err)
			}
		})
	}
}
