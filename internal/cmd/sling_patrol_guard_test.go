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
	// Patrol formula to a role-agent target (direct dispatch to "deacon"): allowed.
	if err := checkPatrolDispatchGuard("mol-deacon-patrol", "gt-abc", "deacon", workBead); err != nil {
		t.Errorf("patrol formula to role agent should pass, got: %v", err)
	}
}

// TestPatrolGuardMessagesRecommendPatrolNew pins the recovery command in every
// patrol refusal to `gt patrol new` (op-w51a). The old suggestion
// `gt sling <formula> deacon` fails in deferred-dispatch mode ("deferred
// dispatch requires a rig target"), and that error's own suggestion (a rig
// target) is refused by this guard — a live deacon looped between the two
// messages on 2026-07-24.
func TestPatrolGuardMessagesRecommendPatrolNew(t *testing.T) {
	patrolWisp := &beadInfo{Description: "attached_formula: mol-deacon-patrol\ndispatched_by: deacon"}
	refusals := map[string]error{
		"formula-target":    checkPatrolFormulaTarget("mol-deacon-patrol", "openclaw/polecats/furiosa"),
		"bead-resling":      checkPatrolBeadResling("op-wisp-1d8", patrolWisp),
		"deferred-dispatch": checkPatrolDeferredDispatch("mol-deacon-patrol"),
	}
	for name, err := range refusals {
		if err == nil {
			t.Errorf("%s: expected a refusal error", name)
			continue
		}
		if !strings.Contains(err.Error(), "gt patrol new") {
			t.Errorf("%s: refusal should recommend 'gt patrol new', got: %v", name, err)
		}
		if strings.Contains(err.Error(), "gt sling") {
			t.Errorf("%s: refusal must not suggest a 'gt sling' invocation (fails or is refused at dispatch), got: %v", name, err)
		}
	}
	if err := checkPatrolDeferredDispatch("mol-polecat-work"); err != nil {
		t.Errorf("non-patrol formula should pass deferred-dispatch check, got: %v", err)
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

// Role-owned wisp dispatch guard tests (hq-gk229).
//
// Incident 2026-07-23: the scheduler ready-scan dispatched witness patrol
// STEP wisp dbt-wfs-7laya ('Loop or exit for respawn', created_by
// humdbt/witness) to polecat rust wrapped in mol-polecat-work. Step wisps
// carry no patrol attached_formula of their own (the attachment lives on the
// molecule root), so the op-s473 resling guard could not fire. These tests
// pin the wisp-lineage guard: a wisp created by or assigned to a role agent
// must never be dispatched to a polecat.

func TestCheckRoleOwnedWispDispatch(t *testing.T) {
	tests := []struct {
		name    string
		beadID  string
		info    *beadInfo
		refused bool
	}{
		{
			// The hq-gk229 incident shape.
			"witness patrol step wisp",
			"dbt-wfs-7laya",
			&beadInfo{Title: "Loop or exit for respawn", CreatedBy: "humdbt/witness"},
			true,
		},
		{
			"refinery-assigned molecule wisp",
			"gt-wisp-a1",
			&beadInfo{Assignee: "gastown/refinery"},
			true,
		},
		{
			"deacon-created step wisp",
			"hq-wfs-x9",
			&beadInfo{CreatedBy: "deacon"},
			true,
		},
		{
			"polecat-owned molecule step wisp",
			"op-wisp-rv6",
			&beadInfo{Assignee: "openclaw/polecats/furiosa"},
			false,
		},
		{
			"witness-created regular task (witness files discovered work)",
			"gt-abc",
			&beadInfo{CreatedBy: "gastown/witness"},
			false,
		},
		{
			"unattributed wisp",
			"op-wisp-q2",
			&beadInfo{},
			false,
		},
		{
			"nil info",
			"dbt-wfs-7laya",
			nil,
			false,
		},
	}
	for _, tt := range tests {
		err := checkRoleOwnedWispDispatch(tt.beadID, tt.info)
		if tt.refused && err == nil {
			t.Errorf("%s: expected refusal, got nil", tt.name)
		}
		if !tt.refused && err != nil {
			t.Errorf("%s: expected pass, got: %v", tt.name, err)
		}
		if tt.refused && err != nil && !strings.Contains(err.Error(), "hq-gk229") {
			t.Errorf("%s: error should cite the incident: %v", tt.name, err)
		}
	}
}

func TestCheckPatrolDispatchGuard_RoleOwnedWisp(t *testing.T) {
	witnessStep := &beadInfo{Title: "Loop or exit for respawn", CreatedBy: "humdbt/witness"}

	// Role-owned step wisp to a polecat target: refused.
	if err := checkPatrolDispatchGuard("mol-polecat-work", "dbt-wfs-7laya", "humdbt/polecats/rust", witnessStep); err == nil {
		t.Error("role-owned step wisp to polecat target should be refused")
	}
	// Role-owned step wisp to the polecat pool address: refused.
	if err := checkPatrolDispatchGuard("mol-polecat-work", "dbt-wfs-7laya", "humdbt/polecats", witnessStep); err == nil {
		t.Error("role-owned step wisp to polecat pool should be refused")
	}
	// Same wisp to a role agent target: the wisp guard does not fire
	// (role-agent self-flows stay untouched).
	if err := checkPatrolDispatchGuard("", "dbt-wfs-7laya", "humdbt/witness", witnessStep); err != nil {
		t.Errorf("role-owned wisp to its role agent should pass the wisp guard, got: %v", err)
	}
}

// TestExecuteSling_RoleOwnedStepWispRefused verifies executeSling refuses the
// hq-gk229 incident shape: a witness patrol step wisp (no patrol
// attached_formula, created_by a witness) dispatched toward a rig polecat.
func TestExecuteSling_RoleOwnedStepWispRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"Loop or exit for respawn","status":"open","assignee":"","created_by":"humdbt/witness","description":"End of patrol cycle decision."}]`)

	params := SlingParams{
		BeadID:   "dbt-wfs-7laya",
		RigName:  "testrig",
		TownRoot: townRoot,
	}

	result, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when dispatching role-owned step wisp, got nil")
	}
	if result.ErrMsg != "role-owned wisp" {
		t.Errorf("expected ErrMsg='role-owned wisp', got %q", result.ErrMsg)
	}
	if !strings.Contains(err.Error(), "hq-gk229") {
		t.Errorf("error should cite the incident: %v", err)
	}
}

// TestExecuteSling_RoleOwnedWisp_ForceDoesNotBypass verifies --force does not
// bypass the role-owned wisp guard.
func TestExecuteSling_RoleOwnedWisp_ForceDoesNotBypass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"Loop or exit for respawn","status":"open","assignee":"","created_by":"humdbt/witness","description":""}]`)

	params := SlingParams{
		BeadID:   "dbt-wfs-7laya",
		RigName:  "testrig",
		TownRoot: townRoot,
		Force:    true,
	}

	_, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when force-dispatching role-owned wisp, got nil")
	}
	if !strings.Contains(err.Error(), "hq-gk229") {
		t.Errorf("--force should not bypass the wisp guard: %v", err)
	}
}
