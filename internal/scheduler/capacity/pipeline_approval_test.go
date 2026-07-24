package capacity

import (
	"strings"
	"testing"
)

// Approval-hold / role-assigned dispatch filter tests (op-ijaw).
//
// Incident 2026-07-24: the ready-scan slung op-2nqz — a witness-owned
// APPROVAL-HOLD tracking bead whose close IS the approval signal — to a
// polecat three times. These tests pin the pipeline filters that keep held
// and role-assigned beads out of every dispatch plan.

func TestFilterApprovalHeld(t *testing.T) {
	beads := []PendingBead{
		{ID: "ctx-1", WorkBeadID: "op-2nqz", Labels: []string{"needs-approval:openclaw/witness:1753380020"}},
		{ID: "ctx-2", WorkBeadID: "op-normal", Labels: []string{"bug"}},
		{ID: "ctx-3", WorkBeadID: "op-bare", Labels: []string{"needs-approval"}},
	}
	filtered, removed := FilterApprovalHeld(beads)
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	if len(filtered) != 1 || filtered[0].WorkBeadID != "op-normal" {
		t.Errorf("filtered = %v, want only op-normal", filtered)
	}
}

func TestFilterRoleAssigned(t *testing.T) {
	beads := []PendingBead{
		{ID: "ctx-1", WorkBeadID: "op-2nqz", Assignee: "openclaw/witness"},
		{ID: "ctx-2", WorkBeadID: "op-normal", Assignee: ""},
		{ID: "ctx-3", WorkBeadID: "op-pc", Assignee: "openclaw/polecats/furiosa"},
		{ID: "ctx-4", WorkBeadID: "op-mayor", Assignee: "mayor"},
		// Witness-CREATED discovered work with no role assignee stays dispatchable.
		{ID: "ctx-5", WorkBeadID: "op-disc", CreatedBy: "openclaw/witness"},
	}
	filtered, removed := FilterRoleAssigned(beads)
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (witness + mayor assignees)", removed)
	}
	want := map[string]bool{"op-normal": true, "op-pc": true, "op-disc": true}
	for _, b := range filtered {
		if !want[b.WorkBeadID] {
			t.Errorf("unexpected bead in filtered set: %s", b.WorkBeadID)
		}
	}
	if len(filtered) != 3 {
		t.Errorf("len(filtered) = %d, want 3", len(filtered))
	}
}

// TestPlanDispatch_FiltersApprovalHeld pins the incident shape: a held
// witness process bead in the ready pool with free capacity must NOT be
// dispatched, while a normal task alongside it IS.
func TestPlanDispatch_FiltersApprovalHeld(t *testing.T) {
	ready := []PendingBead{
		{ID: "ctx-hold", WorkBeadID: "op-2nqz", TargetRig: "openclaw",
			Labels: []string{"needs-approval:openclaw/witness:1753380020"}},
		{ID: "ctx-work", WorkBeadID: "op-real", TargetRig: "openclaw"},
	}
	plan := PlanDispatch(5, 3, ready)
	if len(plan.ToDispatch) != 1 || plan.ToDispatch[0].WorkBeadID != "op-real" {
		t.Fatalf("ToDispatch = %v, want only op-real", plan.ToDispatch)
	}
	if plan.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", plan.Skipped)
	}
	if !strings.Contains(plan.Reason, "approval-hold-filtered") {
		t.Errorf("Reason = %q, want approval-hold-filtered suffix", plan.Reason)
	}
}

func TestPlanDispatch_FiltersRoleAssigned(t *testing.T) {
	ready := []PendingBead{
		{ID: "ctx-role", WorkBeadID: "op-2nqz", TargetRig: "openclaw", Assignee: "openclaw/witness"},
		{ID: "ctx-work", WorkBeadID: "op-real", TargetRig: "openclaw"},
	}
	plan := PlanDispatch(5, 3, ready)
	if len(plan.ToDispatch) != 1 || plan.ToDispatch[0].WorkBeadID != "op-real" {
		t.Fatalf("ToDispatch = %v, want only op-real", plan.ToDispatch)
	}
	if !strings.Contains(plan.Reason, "role-assigned-filtered") {
		t.Errorf("Reason = %q, want role-assigned-filtered suffix", plan.Reason)
	}
}

// TestPlanDispatch_OnlyFilteredBeads: when every ready bead is held or
// role-assigned, the plan dispatches nothing and reports why.
func TestPlanDispatch_OnlyFilteredBeads(t *testing.T) {
	ready := []PendingBead{
		{ID: "ctx-hold", WorkBeadID: "op-2nqz", Labels: []string{"needs-approval"}},
		{ID: "ctx-role", WorkBeadID: "op-proc", Assignee: "openclaw/refinery"},
	}
	plan := PlanDispatch(5, 3, ready)
	if len(plan.ToDispatch) != 0 {
		t.Fatalf("ToDispatch = %v, want empty", plan.ToDispatch)
	}
	if plan.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", plan.Skipped)
	}
	if !strings.Contains(plan.Reason, "approval-hold-filtered") || !strings.Contains(plan.Reason, "role-assigned-filtered") {
		t.Errorf("Reason = %q, want both filter suffixes", plan.Reason)
	}
}
