package cmd

// op-96zr enforceable approval-hold: a `needs-approval` label on the hooked
// (source) bead is a hard submission gate. While the label is present,
// `gt done` and `gt mq submit` refuse to submit the branch to the merge
// queue (or push it directly to the default branch), so a witness/mayor
// hold — or a polecat's own "this needs sign-off" self-hold, e.g. on a test
// change — has an enforcement point instead of relying on the agent noticing
// an instruction mid-turn (the AA-925 failure).
//
// The hold lives on the SOURCE bead (not the agent bead) so it survives
// polecat reassignment and is visible to anyone inspecting the issue.
// Label forms:
//   needs-approval                       — bare (set by hand via bd)
//   needs-approval:<setter>:<unix-ts>    — set via `gt approval hold`
//
// Clearing requires the approver: `gt approval clear` refuses polecat
// actors. Submitting past a hold requires the explicit
// --override-approval-hold flag, which is recorded on the bead as a
// permanent comment (the activity log) and nudges the holder and mayor.
//
// Failure policy: if the source bead cannot be read the gate FAILS OPEN
// with a loud warning — MR-bead creation needs beads anyway, so a down
// beads store cannot produce a merged-but-unapproved MR.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/style"
)

// approvalHoldLabel is the bare hold label; approvalHoldLabelPrefix matches
// the metadata-carrying form needs-approval:<setter>:<unix-ts>. Canonical
// definitions live in internal/constants so the dispatch pipeline (which
// cannot import cmd) shares the same predicate (op-ijaw).
const (
	approvalHoldLabel       = constants.ApprovalHoldLabel
	approvalHoldLabelPrefix = constants.ApprovalHoldLabelPrefix
)

// approvalHoldLabels returns the needs-approval labels present on the issue.
func approvalHoldLabels(issue *beads.Issue) []string {
	if issue == nil {
		return nil
	}
	var held []string
	for _, label := range issue.Labels {
		if label == approvalHoldLabel || strings.HasPrefix(label, approvalHoldLabelPrefix) {
			held = append(held, label)
		}
	}
	return held
}

// parseApprovalHoldLabel extracts the setter and set-time from a hold label.
// Returns zero values for parts the label doesn't carry.
// The setter itself may contain ':'-free path segments only (actor addresses
// are rig/role/name), so setter = everything between the prefix and the last
// ':'-delimited numeric timestamp.
func parseApprovalHoldLabel(label string) (setter string, setAt time.Time) {
	rest, ok := strings.CutPrefix(label, approvalHoldLabelPrefix)
	if !ok || rest == "" {
		return "", time.Time{}
	}
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		if ts, err := strconv.ParseInt(rest[idx+1:], 10, 64); err == nil {
			return rest[:idx], time.Unix(ts, 0)
		}
	}
	return rest, time.Time{}
}

// describeApprovalHold renders one hold label for humans: "witness (set 2026-07-23T03:40Z)".
func describeApprovalHold(label string) string {
	setter, setAt := parseApprovalHoldLabel(label)
	switch {
	case setter != "" && !setAt.IsZero():
		return fmt.Sprintf("%s (set %s)", setter, setAt.UTC().Format(time.RFC3339))
	case setter != "":
		return setter
	default:
		return "unknown (bare needs-approval label)"
	}
}

// approvalHoldDeps carries the gate inputs plus injectable side effects so
// the decision logic is unit-testable without beads/nudge subprocesses
// (mirrors doneCIGateDeps for the AA-851 CI gate).
type approvalHoldDeps struct {
	// loadIssue returns the source issue; nil issue + nil error means
	// "unavailable" and the gate fails open.
	loadIssue func() (*beads.Issue, error)
	issueID   string
	agent     string
	branch    string
	override  bool
	out       io.Writer

	// recordOverride persists the override to the bead's comment trail.
	recordOverride func(holders []string)
	// nudge sends a best-effort gt nudge to addr.
	nudge func(addr, msg string)
}

// enforce runs the gate. A nil return means submission may proceed; a
// non-nil error aborts with the polecat still assigned and nothing queued.
func (d *approvalHoldDeps) enforce() error {
	issue, err := d.loadIssue()
	if err != nil || issue == nil {
		// Fail open, loudly: the submission path needs beads for MR creation,
		// so a broken store cannot silently merge held work anyway.
		fmt.Fprintf(d.out, "%s approval-hold gate: could not read issue %s (%v) — FAILING OPEN (op-96zr)\n",
			style.Warning.Render("⚠"), d.issueID, err)
		return nil
	}

	held := approvalHoldLabels(issue)
	if len(held) == 0 {
		return nil
	}

	var holders []string
	var descriptions []string
	for _, label := range held {
		descriptions = append(descriptions, describeApprovalHold(label))
		if setter, _ := parseApprovalHoldLabel(label); setter != "" {
			holders = append(holders, setter)
		}
	}
	holdList := strings.Join(descriptions, "; ")

	if d.override {
		fmt.Fprintf(d.out, "%s APPROVAL-HOLD OVERRIDE: submitting %s past needs-approval (held by: %s)\n",
			style.Warning.Render("⚠"), d.issueID, holdList)
		fmt.Fprintf(d.out, "  The override is recorded on %s; the hold label stays until the approver clears it.\n", d.issueID)
		if d.recordOverride != nil {
			d.recordOverride(holders)
		}
		overrideMsg := fmt.Sprintf("APPROVAL-HOLD OVERRIDE: %s submitted %s (branch %s) past needs-approval via --override-approval-hold",
			d.agent, d.issueID, d.branch)
		for _, holder := range holders {
			d.nudge(holder, overrideMsg)
		}
		d.nudge("mayor/", overrideMsg)
		return nil
	}

	// Hard refusal. Nudge the holder(s) so they know the work is ready for
	// review — the polecat hitting the gate is the "please approve" signal.
	for _, holder := range holders {
		d.nudge(holder, fmt.Sprintf("APPROVAL_HOLD: %s hit your needs-approval hold on %s (branch %s) — review and `gt approval clear %s` to release",
			d.agent, d.issueID, d.branch, d.issueID))
	}
	return fmt.Errorf("APPROVAL_HOLD: issue %s carries needs-approval — refusing to submit to the merge queue.\n"+
		"  Held by: %s\n"+
		"You stay assigned to this work. The hold is cleared by the approver:\n"+
		"  gt approval clear %s        (witness/mayor — polecats cannot clear holds)\n"+
		"If the approver has explicitly authorized submission, re-run with --override-approval-hold\n"+
		"(the override is recorded on the bead and the holder + mayor are notified)",
		d.issueID, holdList, d.issueID)
}

// enforceApprovalHold wires the gate with real side effects. issue may be a
// pre-loaded source issue (to avoid a second bd.Show); pass nil to load.
// Called from the submission paths of gt done and gt mq submit, before any
// MR-bead creation or direct push to the default branch.
func enforceApprovalHold(bd *beads.Beads, issue *beads.Issue, issueID, agent, branch string, override bool) error {
	if issue == nil && issueID == "" {
		// No source issue to inspect (e.g. direct-strategy done on an
		// issue-less branch) — nothing to hold on.
		return nil
	}
	deps := &approvalHoldDeps{
		loadIssue: func() (*beads.Issue, error) {
			if issue != nil {
				return issue, nil
			}
			return bd.Show(issueID)
		},
		issueID:  issueID,
		agent:    agent,
		branch:   branch,
		override: override,
		out:      os.Stdout,
		recordOverride: func(holders []string) {
			msg := fmt.Sprintf("APPROVAL-HOLD OVERRIDE: %s submitted branch %s past needs-approval (held by: %s) via --override-approval-hold at %s",
				agent, branch, strings.Join(holders, ", "), time.Now().UTC().Format(time.RFC3339))
			if len(holders) == 0 {
				msg = fmt.Sprintf("APPROVAL-HOLD OVERRIDE: %s submitted branch %s past a bare needs-approval label via --override-approval-hold at %s",
					agent, branch, time.Now().UTC().Format(time.RFC3339))
			}
			if _, err := bd.Run("comments", "add", issueID, msg); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: couldn't record approval-hold override on %s: %v\n", issueID, err)
			}
		},
		nudge: nudgeBestEffort,
	}
	return deps.enforce()
}

// nudgeBestEffort sends a gt nudge to an agent address. Best-effort: nudges
// are ephemeral by design, so failures are logged and never fatal.
func nudgeBestEffort(addr, msg string) {
	if addr == "" {
		return
	}
	cmd := exec.Command("gt", "nudge", addr, msg)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to nudge %s: %v\n", addr, err)
	}
}
