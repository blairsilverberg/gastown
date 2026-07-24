package cmd

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/constants"
)

// Approval-hold / role-assigned dispatch guards (op-ijaw).
//
// Incident 2026-07-24: the scheduler ready-scan slung op-2nqz — a
// witness-owned APPROVAL-HOLD tracking bead for a retroactive test-change
// approval (AA-925) — to a polecat three times; the third sling spawned a
// polecat. The hold bead's close IS the approval signal ("On approval: close
// this bead"), so a polecat completing the sling would have closed it and
// forged the approval in the ledger — the exact anti-pattern the op-96zr
// `gt done` gate was built to block. The bead predated op-96zr and carried
// no needs-approval label, so nothing at dispatch time refused it.
//
// Two guards close the gap, mirroring the op-s473 / hq-gk229 layering:
//
//   - checkApprovalHeldDispatch: a bead carrying any needs-approval label
//     (bare or needs-approval:<setter>:<unix-ts>) is awaiting an approver's
//     sign-off and must never be dispatched to a polecat. The hold is
//     released by the approver (`gt approval clear`), not by dispatching
//     the bead to someone who will close it.
//
//   - checkRoleAssignedBeadDispatch: a bead ASSIGNED to a singleton role
//     agent (witness/refinery/deacon/mayor) is that agent's own process
//     work, never polecat work. Note this is assignee-based, not
//     creator-based: role agents legitimately file discovered work via
//     `bd create` and that work must stay dispatchable (hq-gk229
//     precedent). Reassigning role-agent work to a polecat is done
//     explicitly (clear the assignee first), never by the scheduler.
//
// Neither guard is bypassed by --force — dispatching held or role-assigned
// work to a polecat is never valid without an explicit human step first.

// checkApprovalHeldDispatch rejects dispatching a bead that carries a
// needs-approval hold label to a polecat.
func checkApprovalHeldDispatch(beadID string, info *beadInfo) error {
	if info == nil || !constants.HasApprovalHold(info.Labels) {
		return nil
	}
	return fmt.Errorf("refusing to sling bead %s: it carries a needs-approval hold and is awaiting an approver's sign-off (op-ijaw)\n"+
		"A polecat completing this bead would forge the approval. The hold is cleared by the approver:\n"+
		"  gt approval clear %s        (witness/mayor — then re-sling if the work is real)",
		beadID, beadID)
}

// checkRoleAssignedBeadDispatch rejects dispatching a bead assigned to a
// role agent (witness/refinery/deacon/mayor) to a polecat.
func checkRoleAssignedBeadDispatch(beadID string, info *beadInfo) error {
	if info == nil || !constants.IsRoleAgentActor(info.Assignee) {
		return nil
	}
	return fmt.Errorf("refusing to sling bead %s: it is assigned to role agent %s — role-agent process beads are never polecat work (op-ijaw)\n"+
		"If this really is polecat work, clear the assignee first: bd update %s --assignee=\"\"",
		beadID, info.Assignee, beadID)
}
