package cmd

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/constants"
)

// Patrol-formula dispatch guard (op-s473).
//
// Patrol formulas (mol-deacon-patrol, mol-witness-patrol, mol-refinery-patrol,
// and any future *patrol* variant) are role-scoped monitoring loops. They must
// NEVER be dispatched to a polecat: a polecat running the deacon loop could
// dispatch dogs, act on gates, and sling gated work.
//
// Incident 2026-07-22: the daemon dispatch path re-slung a deacon patrol wisp
// (attached_formula: mol-deacon-patrol, dispatched_by: deacon) to a fresh
// polecat minutes after a deacon session spawn. The guard blocks every sling
// path that can reach a polecat, regardless of which actor stamped the
// dispatch.
//
// These checks are NOT bypassed by --force — patrol work on a polecat is never
// valid. The legitimate patrol flow is the role agent creating its own patrol
// wisp with `gt patrol new` (role auto-detected from GT_ROLE). Refusal text
// must recommend exactly that: the old suggestion `gt sling <formula> deacon`
// fails in deferred-dispatch mode ("deferred dispatch requires a rig target"),
// whose error in turn suggests a rig target the patrol guard refuses — a loop
// that stuck a live deacon on 2026-07-24 (op-w51a).

// slingTargetsPolecat reports whether a sling target string routes work to a
// rig polecat: an explicit <rig>/polecats[/<name>] address, or a bare rig name
// (rig targets always select or spawn a polecat).
func slingTargetsPolecat(target string) bool {
	parts := strings.Split(target, "/")
	if len(parts) >= 2 && parts[1] == "polecats" {
		return true
	}
	_, isRig := IsRigName(target)
	return isRig
}

// beadPatrolFormula returns the patrol formula recorded in the bead's
// attachment metadata (attached_formula), or "" if none. A bead carrying a
// patrol attachment is a patrol wisp — re-slinging it re-animates the patrol
// loop on the new target.
func beadPatrolFormula(info *beadInfo) string {
	if info == nil || info.Description == "" {
		return ""
	}
	attach := beads.ParseAttachmentFields(&beads.Issue{Description: info.Description})
	if attach == nil {
		return ""
	}
	if constants.IsPatrolFormula(attach.AttachedFormula) {
		return attach.AttachedFormula
	}
	return ""
}

// checkPatrolFormulaTarget rejects slinging a patrol formula to a polecat
// target. Returns nil for non-patrol formulas.
func checkPatrolFormulaTarget(formulaName, target string) error {
	if !constants.IsPatrolFormula(formulaName) {
		return nil
	}
	return fmt.Errorf("refusing to sling patrol formula %s to %s: patrol formulas must never run on polecats (op-s473)\nPatrol loops belong on their role agent — as that agent, run: gt patrol new",
		formulaName, target)
}

// checkPatrolBeadResling rejects re-slinging a bead that carries patrol
// attachment metadata. Patrol wisps are created fresh by their role agent
// (`gt patrol new`); an existing patrol wisp must never be re-dispatched —
// to a polecat or anywhere else.
func checkPatrolBeadResling(beadID string, info *beadInfo) error {
	attached := beadPatrolFormula(info)
	if attached == "" {
		return nil
	}
	return fmt.Errorf("refusing to sling bead %s: it carries patrol formula %s (attached_formula) and patrol work must never be re-dispatched (op-s473)\nPatrol loops are recreated by their role agent — as that agent, run: gt patrol new",
		beadID, attached)
}

// checkPatrolDeferredDispatch rejects a patrol formula in deferred-dispatch
// mode with the canonical recovery command. Without this, the generic
// "deferred dispatch requires a rig target: gt sling <name> <rig>" error
// recommends a rig invocation that checkPatrolFormulaTarget then refuses
// (op-w51a). Returns nil for non-patrol names.
func checkPatrolDeferredDispatch(name string) error {
	if !constants.IsPatrolFormula(name) {
		return nil
	}
	return fmt.Errorf("cannot schedule patrol formula %s: patrol loops run on their role agent, never on rig polecats (op-s473)\nAs the role agent, run: gt patrol new", name)
}

// checkRoleOwnedWispDispatch rejects dispatching a wisp created by or
// assigned to a role agent (witness/refinery/deacon/mayor) to a polecat.
//
// Patrol STEP wisps (*-wfs-* / *-wisp-*) carry no attached_formula of their
// own — the patrol attachment lives on the molecule root — so
// checkPatrolBeadResling cannot see them. Incident hq-gk229 (2026-07-23):
// the scheduler ready-scan classified witness patrol step dbt-wfs-7laya
// ('Loop or exit for respawn', created_by humdbt/witness) as dispatchable
// and slung it to a polecat wrapped in mol-polecat-work. Role-owned wisps
// are the role agent's own workflow machinery, never polecat work.
// Not bypassed by --force.
func checkRoleOwnedWispDispatch(beadID string, info *beadInfo) error {
	if info == nil {
		return nil
	}
	if !constants.IsRoleOwnedWisp(beadID, info.CreatedBy, info.Assignee) {
		return nil
	}
	owner := info.CreatedBy
	if !constants.IsRoleAgentActor(owner) {
		owner = info.Assignee
	}
	return fmt.Errorf("refusing to sling bead %s: it is a wisp owned by role agent %s (role-agent workflow steps must never be dispatched to polecats, hq-gk229)",
		beadID, owner)
}

// checkPatrolDispatchGuard combines the patrol checks for bead-dispatch
// paths: the explicit formula and role-owned-wisp check (when the target is
// a polecat/rig) and the bead's own patrol attachment (any target).
func checkPatrolDispatchGuard(formulaName, beadID, target string, info *beadInfo) error {
	if slingTargetsPolecat(target) {
		if err := checkPatrolFormulaTarget(formulaName, target); err != nil {
			return err
		}
		if err := checkRoleOwnedWispDispatch(beadID, info); err != nil {
			return err
		}
	}
	return checkPatrolBeadResling(beadID, info)
}
