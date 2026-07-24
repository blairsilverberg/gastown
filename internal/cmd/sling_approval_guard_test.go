package cmd

import (
	"runtime"
	"strings"
	"testing"
)

// Approval-hold / role-assigned dispatch guard tests (op-ijaw).
//
// Incident 2026-07-24: the scheduler ready-scan slung op-2nqz — a
// witness-owned APPROVAL-HOLD tracking bead for the AA-925 retroactive
// test-change approval, whose close IS the approval signal — to a polecat
// three times; the third spawned polecat ace. These tests pin the guards
// that refuse held and role-assigned beads on every sling path that can
// reach a polecat.

func TestCheckApprovalHeldDispatch(t *testing.T) {
	tests := []struct {
		name    string
		beadID  string
		info    *beadInfo
		refused bool
	}{
		{
			// The op-ijaw incident shape (post-backfill: hold armed via gt approval hold).
			"witness hold bead with metadata label",
			"op-2nqz",
			&beadInfo{Title: "APPROVAL HOLD: AA-925 retroactive test-change approval",
				Labels: []string{"needs-approval:openclaw/witness:1753380020"}},
			true,
		},
		{
			"bare needs-approval label",
			"op-held",
			&beadInfo{Labels: []string{"needs-approval"}},
			true,
		},
		{
			"normal work bead",
			"op-work",
			&beadInfo{Labels: []string{"bug", "HumAssist"}},
			false,
		},
		{
			"no labels",
			"op-plain",
			&beadInfo{},
			false,
		},
		{
			"nil info",
			"op-2nqz",
			nil,
			false,
		},
	}
	for _, tt := range tests {
		err := checkApprovalHeldDispatch(tt.beadID, tt.info)
		if tt.refused && err == nil {
			t.Errorf("%s: expected refusal, got nil", tt.name)
		}
		if !tt.refused && err != nil {
			t.Errorf("%s: expected pass, got: %v", tt.name, err)
		}
		if tt.refused && err != nil && !strings.Contains(err.Error(), "op-ijaw") {
			t.Errorf("%s: error should cite the incident: %v", tt.name, err)
		}
	}
}

func TestCheckRoleAssignedBeadDispatch(t *testing.T) {
	tests := []struct {
		name    string
		beadID  string
		info    *beadInfo
		refused bool
	}{
		{
			"witness-assigned process bead",
			"op-2nqz",
			&beadInfo{Assignee: "openclaw/witness"},
			true,
		},
		{
			"refinery-assigned bead",
			"op-mr1",
			&beadInfo{Assignee: "gastown/refinery"},
			true,
		},
		{
			"mayor-assigned bead",
			"hq-x1",
			&beadInfo{Assignee: "mayor"},
			true,
		},
		{
			// Creator-based dispatch stays allowed: witnesses file discovered work.
			"witness-created unassigned discovered work",
			"op-disc",
			&beadInfo{CreatedBy: "openclaw/witness"},
			false,
		},
		{
			"polecat-assigned bead",
			"op-pc1",
			&beadInfo{Assignee: "openclaw/polecats/furiosa"},
			false,
		},
		{
			"unassigned bead",
			"op-free",
			&beadInfo{},
			false,
		},
		{
			"nil info",
			"op-2nqz",
			nil,
			false,
		},
	}
	for _, tt := range tests {
		err := checkRoleAssignedBeadDispatch(tt.beadID, tt.info)
		if tt.refused && err == nil {
			t.Errorf("%s: expected refusal, got nil", tt.name)
		}
		if !tt.refused && err != nil {
			t.Errorf("%s: expected pass, got: %v", tt.name, err)
		}
		if tt.refused && err != nil && !strings.Contains(err.Error(), "op-ijaw") {
			t.Errorf("%s: error should cite the incident: %v", tt.name, err)
		}
	}
}

func TestCheckPatrolDispatchGuard_ApprovalHeld(t *testing.T) {
	heldBead := &beadInfo{Title: "APPROVAL HOLD: AA-925",
		Labels: []string{"needs-approval:openclaw/witness:1753380020"}}

	// Held bead to a polecat target: refused.
	if err := checkPatrolDispatchGuard("mol-polecat-work", "op-2nqz", "openclaw/polecats/ace", heldBead); err == nil {
		t.Error("approval-held bead to polecat target should be refused")
	}
	// Held bead to the polecat pool: refused.
	if err := checkPatrolDispatchGuard("mol-polecat-work", "op-2nqz", "openclaw/polecats", heldBead); err == nil {
		t.Error("approval-held bead to polecat pool should be refused")
	}
	// Held bead to a role agent target: the hold guard does not fire —
	// role-agent flows (e.g. witness working its own hold bead) stay untouched.
	if err := checkPatrolDispatchGuard("", "op-2nqz", "openclaw/witness", heldBead); err != nil {
		t.Errorf("approval-held bead to role agent should pass this guard, got: %v", err)
	}
}

func TestCheckPatrolDispatchGuard_RoleAssigned(t *testing.T) {
	witnessBead := &beadInfo{Assignee: "openclaw/witness"}

	if err := checkPatrolDispatchGuard("mol-polecat-work", "op-2nqz", "openclaw/polecats/ace", witnessBead); err == nil {
		t.Error("witness-assigned bead to polecat target should be refused")
	}
	if err := checkPatrolDispatchGuard("", "op-2nqz", "openclaw/witness", witnessBead); err != nil {
		t.Errorf("witness-assigned bead to the witness should pass this guard, got: %v", err)
	}
}

// TestExecuteSling_ApprovalHeldBeadRefused verifies executeSling refuses the
// op-ijaw incident shape: a needs-approval hold bead dispatched toward a rig
// polecat by the scheduler.
func TestExecuteSling_ApprovalHeldBeadRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"APPROVAL HOLD: AA-925 retroactive test-change approval","status":"open","assignee":"","labels":["needs-approval:openclaw/witness:1753380020"],"description":"On approval: close this bead."}]`)

	params := SlingParams{
		BeadID:   "op-2nqz",
		RigName:  "testrig",
		TownRoot: townRoot,
	}

	result, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when dispatching approval-held bead, got nil")
	}
	if result.ErrMsg != "approval hold" {
		t.Errorf("expected ErrMsg='approval hold', got %q", result.ErrMsg)
	}
	if !strings.Contains(err.Error(), "needs-approval") {
		t.Errorf("error should name the hold label: %v", err)
	}
}

// TestExecuteSling_ApprovalHeld_ForceDoesNotBypass verifies --force does not
// bypass the approval-hold guard: releasing a hold is the approver's act,
// never the dispatcher's.
func TestExecuteSling_ApprovalHeld_ForceDoesNotBypass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"APPROVAL HOLD","status":"open","assignee":"","labels":["needs-approval"],"description":""}]`)

	params := SlingParams{
		BeadID:   "op-2nqz",
		RigName:  "testrig",
		TownRoot: townRoot,
		Force:    true,
	}

	_, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when force-dispatching approval-held bead, got nil")
	}
	if !strings.Contains(err.Error(), "op-ijaw") {
		t.Errorf("--force should not bypass the approval-hold guard: %v", err)
	}
}

// TestExecuteSling_RoleAssignedBeadRefused verifies executeSling refuses a
// bead assigned to a role agent — the second op-ijaw guard (op-2nqz was
// reassigned to openclaw/witness as part of the incident response, and must
// never be re-dispatched to a polecat while so assigned).
func TestExecuteSling_RoleAssignedBeadRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	townRoot := patrolGuardTestTown(t,
		`[{"title":"APPROVAL HOLD: AA-925","status":"open","assignee":"openclaw/witness","description":""}]`)

	params := SlingParams{
		BeadID:   "op-2nqz",
		RigName:  "testrig",
		TownRoot: townRoot,
	}

	result, err := executeSling(params)
	if err == nil {
		t.Fatal("expected error when dispatching role-assigned bead, got nil")
	}
	if result.ErrMsg != "role-assigned bead" {
		t.Errorf("expected ErrMsg='role-assigned bead', got %q", result.ErrMsg)
	}
	if !strings.Contains(err.Error(), "openclaw/witness") {
		t.Errorf("error should name the role assignee: %v", err)
	}
}
