package capacity

import (
	"strings"

	"github.com/steveyegge/gastown/internal/constants"
)

// PendingBead represents a bead that is scheduled and ready for dispatch evaluation.
type PendingBead struct {
	ID              string // Context bead ID (sling context)
	WorkBeadID      string // The actual work bead ID
	Title           string
	TargetRig       string
	Description     string
	Labels          []string
	CreatedBy       string              // Work bead creator (actor address)
	Assignee        string              // Work bead assignee (actor address)
	Context         *SlingContextFields // Parsed sling params from context bead
	ContextWorkDir  string              // Work dir for the DB where the context was discovered.
	ContextBeadsDir string              // Resolved .beads dir where the context was discovered.
}

// SlingContextFields holds scheduling parameters stored on a sling context bead.
// JSON-serialized as the context bead's description.
type SlingContextFields struct {
	Version          int    `json:"version"`
	WorkBeadID       string `json:"work_bead_id"`
	TargetRig        string `json:"target_rig"`
	Formula          string `json:"formula,omitempty"`
	Args             string `json:"args,omitempty"`
	Vars             string `json:"vars,omitempty"`
	EnqueuedAt       string `json:"enqueued_at"`
	Merge            string `json:"merge,omitempty"`
	Convoy           string `json:"convoy,omitempty"`
	BaseBranch       string `json:"base_branch,omitempty"`
	ResumeBranch     string `json:"resume_branch,omitempty"`
	NoMerge          bool   `json:"no_merge,omitempty"`
	ReviewOnly       bool   `json:"review_only,omitempty"`
	Account          string `json:"account,omitempty"`
	Agent            string `json:"agent,omitempty"`
	HookRawBead      bool   `json:"hook_raw_bead,omitempty"`
	Owned            bool   `json:"owned,omitempty"`
	Mode             string `json:"mode,omitempty"`
	DispatchFailures int    `json:"dispatch_failures,omitempty"`
	LastFailure      string `json:"last_failure,omitempty"`
}

// LabelSlingContext is the label used to identify sling context beads.
const LabelSlingContext = "gt:sling-context"

// Labels that mark inter-agent messaging beads. These are never polecat work
// and must not be dispatched to rig polecats.
const (
	LabelMessage      = "gt:message"
	LabelHandoff      = "gt:handoff"
	LabelMergeRequest = "gt:merge-request"
)

// IsMessagingBead reports whether the bead is an inter-agent communication
// artifact rather than dispatchable work. Used as a defensive filter in the
// dispatch pipeline: a bead carrying any of these labels must never be handed
// to a polecat (gt-el4 / gastownhall/gastown#3800).
func IsMessagingBead(labels []string) bool {
	for _, l := range labels {
		switch l {
		case LabelMessage, LabelHandoff, LabelMergeRequest:
			return true
		}
	}
	return false
}

// FilterMessagingBeads removes messaging-labeled beads from the candidate slice.
// Returns the filtered slice plus the count of removed beads. Callers should
// log the skipped beads at debug level so the gap is observable.
func FilterMessagingBeads(beads []PendingBead) ([]PendingBead, int) {
	var result []PendingBead
	removed := 0
	for _, b := range beads {
		if IsMessagingBead(b.Labels) {
			removed++
			continue
		}
		result = append(result, b)
	}
	return result, removed
}

// IsRoleOwnedWispBead reports whether a pending bead's work bead is a wisp
// created by or assigned to a role agent (witness/refinery/deacon/mayor).
// Role-owned wisps are steps of a role agent's own workflow loop (e.g. a
// witness patrol molecule) and must never be classified dispatchable
// (hq-gk229: the ready-scan dispatched witness patrol step dbt-wfs-7laya to
// a polecat wrapped in mol-polecat-work).
func IsRoleOwnedWispBead(b PendingBead) bool {
	return constants.IsRoleOwnedWisp(b.WorkBeadID, b.CreatedBy, b.Assignee)
}

// FilterRoleOwnedWisps removes role-owned wisp beads from the candidate slice.
// Returns the filtered slice plus the count of removed beads. Callers should
// log the skipped beads so the upstream misclassification is observable.
func FilterRoleOwnedWisps(beads []PendingBead) ([]PendingBead, int) {
	var result []PendingBead
	removed := 0
	for _, b := range beads {
		if IsRoleOwnedWispBead(b) {
			removed++
			continue
		}
		result = append(result, b)
	}
	return result, removed
}

// IsApprovalHeldBead reports whether a pending bead's work bead carries a
// needs-approval hold label (op-96zr). Held beads are awaiting an approver's
// sign-off and must never be auto-dispatched: for a hold bead whose close IS
// the approval signal, a polecat completing the sling would forge the
// approval in the ledger (op-ijaw: witness hold bead op-2nqz slung 3x).
func IsApprovalHeldBead(b PendingBead) bool {
	return constants.HasApprovalHold(b.Labels)
}

// FilterApprovalHeld removes approval-held beads from the candidate slice.
// Returns the filtered slice plus the count of removed beads.
func FilterApprovalHeld(beads []PendingBead) ([]PendingBead, int) {
	var result []PendingBead
	removed := 0
	for _, b := range beads {
		if IsApprovalHeldBead(b) {
			removed++
			continue
		}
		result = append(result, b)
	}
	return result, removed
}

// IsRoleAssignedBead reports whether a pending bead's work bead is assigned
// to a singleton role agent (witness/refinery/deacon/mayor). Such beads are
// the role agent's own process work, never polecat work (op-ijaw). This is
// assignee-based, not creator-based: role agents legitimately file
// discovered work and that work must stay dispatchable (hq-gk229 precedent).
func IsRoleAssignedBead(b PendingBead) bool {
	return constants.IsRoleAgentActor(b.Assignee)
}

// FilterRoleAssigned removes role-assigned beads from the candidate slice.
// Returns the filtered slice plus the count of removed beads.
func FilterRoleAssigned(beads []PendingBead) ([]PendingBead, int) {
	var result []PendingBead
	removed := 0
	for _, b := range beads {
		if IsRoleAssignedBead(b) {
			removed++
			continue
		}
		result = append(result, b)
	}
	return result, removed
}

// DispatchPlan is the output of PlanDispatch — what to dispatch and why.
type DispatchPlan struct {
	ToDispatch []PendingBead
	Skipped    int
	Reason     string // "capacity" | "batch" | "ready" | "none"
}

// FailureAction indicates what to do after a dispatch failure.
type FailureAction int

const (
	// FailureRetry means the bead should be retried on the next cycle.
	FailureRetry FailureAction = iota
	// FailureQuarantine means the bead should be marked as permanently failed.
	FailureQuarantine
)

// ReadinessFilter is a function that filters pending beads to those ready for dispatch.
type ReadinessFilter func(pending []PendingBead) []PendingBead

// FailurePolicy is a function that determines what to do after N failures.
type FailurePolicy func(failures int) FailureAction

// AllReady is a ReadinessFilter that passes all beads through (no filtering).
func AllReady(pending []PendingBead) []PendingBead {
	return pending
}

// BlockerAware returns a ReadinessFilter that only passes beads whose WorkBeadID
// appears in the readyIDs set (i.e., beads whose work bead has no unresolved blockers).
func BlockerAware(readyIDs map[string]bool) ReadinessFilter {
	return func(pending []PendingBead) []PendingBead {
		var result []PendingBead
		for _, b := range pending {
			if readyIDs[b.WorkBeadID] {
				result = append(result, b)
			}
		}
		return result
	}
}

// PlanDispatch computes which beads to dispatch given capacity constraints.
// availableCapacity: free slots (positive = that many slots, <= 0 = no capacity).
// batchSize: max beads per cycle.
// ready: beads that passed readiness filtering.
//
// Messaging-labeled beads (gt:message / gt:handoff / gt:merge-request) are
// filtered out defensively before any capacity math runs. They are inter-agent
// communication artifacts and never dispatchable work; if any survived earlier
// filtering they must not reach a polecat (gt-el4).
//
// Role-owned wisps (witness/refinery/deacon/mayor workflow steps) are filtered
// the same way: they are a role agent's own patrol-loop machinery, never
// polecat work (hq-gk229).
//
// Approval-held beads (needs-approval label) and beads assigned to a role
// agent are filtered too: held beads await an approver's sign-off — a polecat
// completing one would forge the approval — and role-assigned beads are the
// role agent's own process work (op-ijaw).
func PlanDispatch(availableCapacity, batchSize int, ready []PendingBead) DispatchPlan {
	ready, msgSkipped := FilterMessagingBeads(ready)
	ready, wispSkipped := FilterRoleOwnedWisps(ready)
	ready, holdSkipped := FilterApprovalHeld(ready)
	ready, roleSkipped := FilterRoleAssigned(ready)
	filteredSuffix := ""
	if msgSkipped > 0 {
		filteredSuffix += "+messaging-filtered"
	}
	if wispSkipped > 0 {
		filteredSuffix += "+role-wisp-filtered"
	}
	if holdSkipped > 0 {
		filteredSuffix += "+approval-hold-filtered"
	}
	if roleSkipped > 0 {
		filteredSuffix += "+role-assigned-filtered"
	}
	filtered := msgSkipped + wispSkipped + holdSkipped + roleSkipped

	if len(ready) == 0 {
		if filtered > 0 {
			return DispatchPlan{Skipped: filtered, Reason: strings.TrimPrefix(filteredSuffix, "+")}
		}
		return DispatchPlan{Reason: "none"}
	}

	if availableCapacity <= 0 {
		return DispatchPlan{
			Skipped: len(ready) + filtered,
			Reason:  "capacity",
		}
	}

	// Dispatch up to the smallest of capacity, batchSize, and readyBeads count
	toDispatch := batchSize
	if availableCapacity < toDispatch {
		toDispatch = availableCapacity
	}
	if len(ready) < toDispatch {
		toDispatch = len(ready)
	}

	reason := "batch"
	if availableCapacity < batchSize && availableCapacity < len(ready) {
		reason = "capacity"
	}
	if len(ready) < batchSize && len(ready) < availableCapacity {
		reason = "ready"
	}

	skipped := len(ready) - toDispatch + filtered
	reason = reason + filteredSuffix

	return DispatchPlan{
		ToDispatch: ready[:toDispatch],
		Skipped:    skipped,
		Reason:     reason,
	}
}

// NoRetryPolicy returns a FailurePolicy that always quarantines on first failure.
func NoRetryPolicy() FailurePolicy {
	return func(failures int) FailureAction {
		return FailureQuarantine
	}
}

// CircuitBreakerPolicy returns a FailurePolicy that retries up to maxFailures
// times, then quarantines.
func CircuitBreakerPolicy(maxFailures int) FailurePolicy {
	return func(failures int) FailureAction {
		if failures >= maxFailures {
			return FailureQuarantine
		}
		return FailureRetry
	}
}

// FilterCircuitBroken removes beads that have exceeded the maximum dispatch
// failures threshold. Returns the filtered list and the count of removed beads.
func FilterCircuitBroken(beads []PendingBead, maxFailures int) ([]PendingBead, int) {
	var result []PendingBead
	removed := 0
	for _, b := range beads {
		if b.Context != nil && b.Context.DispatchFailures >= maxFailures {
			removed++
			continue
		}
		result = append(result, b)
	}
	return result, removed
}

// DispatchParams captures what the scheduler needs to tell the dispatcher.
// Mirrors the relevant fields from cmd.SlingParams but is scheduler-owned.
type DispatchParams struct {
	BeadID       string
	FormulaName  string
	RigName      string
	Args         string
	Vars         []string
	Merge        string
	BaseBranch   string
	ResumeBranch string
	Account      string
	Agent        string
	Mode         string
	NoMerge      bool
	ReviewOnly   bool
	HookRawBead  bool
}

// ReconstructFromContext builds DispatchParams from sling context fields.
func ReconstructFromContext(ctx *SlingContextFields) DispatchParams {
	p := DispatchParams{
		BeadID:       ctx.WorkBeadID,
		RigName:      ctx.TargetRig,
		FormulaName:  ctx.Formula,
		Args:         ctx.Args,
		Merge:        ctx.Merge,
		BaseBranch:   ctx.BaseBranch,
		ResumeBranch: ctx.ResumeBranch,
		Account:      ctx.Account,
		Agent:        ctx.Agent,
		Mode:         ctx.Mode,
		NoMerge:      ctx.NoMerge,
		ReviewOnly:   ctx.ReviewOnly,
		HookRawBead:  ctx.HookRawBead,
	}
	if ctx.Vars != "" {
		p.Vars = splitVars(ctx.Vars)
	}
	return p
}

// splitVars splits a newline-separated vars string into individual key=value pairs.
func splitVars(vars string) []string {
	if vars == "" {
		return nil
	}
	var result []string
	for _, line := range strings.Split(vars, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
