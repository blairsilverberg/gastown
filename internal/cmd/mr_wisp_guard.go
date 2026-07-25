package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/style"
)

// MR-wisp creation guard (op-krtw).
//
// Incident 2026-07-25: a polecat working the capital rig was told — in its
// dispatch and in its own completion report — that it must NOT create an MR
// wisp. `gt done` created one anyway, because the "no MR wisp" instruction
// bound the agent's intent and nothing in the machinery. The wisp carried no
// PR reference at all, and capital runs merge_strategy=pr with a human
// approval gate (pullapprove) on the PR: a PR-less MR wisp is therefore a
// path AROUND that gate. The refinery caught and contained it; nothing merged.
//
// This file makes the instruction structural. Both merge-queue entry points
// (`gt done` and `gt mq submit`) evaluate the same decision before creating an
// MR bead, and record the delivery-vehicle provenance (strategy + PR) on the
// beads they do create so the refinery can hard-validate them (see
// Engineer.validateMRDeliveryVehicle — the load-bearing half of the fix).

// mrWispDenyCode identifies why MR-wisp creation was refused. Codes are stable
// strings: they are printed, recorded on the source bead, and asserted in tests.
type mrWispDenyCode string

const (
	// mrWispDenyFlag — the operator passed --no-mr.
	mrWispDenyFlag mrWispDenyCode = "opt-out-flag"
	// mrWispDenyBead — the source bead carries no_mr: true.
	mrWispDenyBead mrWispDenyCode = "opt-out-bead"
	// mrWispDenyQueueDisabled — the rig runs no merge queue (merge_queue.enabled
	// = false). There is nothing to submit to; the branch/PR is the vehicle.
	mrWispDenyQueueDisabled mrWispDenyCode = "queue-disabled"
	// mrWispDenyNoPR — pr-strategy rig with no open PR for the branch. This is
	// the incident shape: a PR-less wisp bypasses the PR approval gate.
	mrWispDenyNoPR mrWispDenyCode = "pr-strategy-no-pr"
)

// mrWispEnv is the resolved, side-effect-free input to the MR-wisp decision.
type mrWispEnv struct {
	Rig    string
	Branch string

	// QueueConfigured is true when the rig's merge-queue settings were read.
	// When false, QueueEnabled is meaningless and the guard stays permissive:
	// most rigs ship no settings file and must keep their existing MQ flow.
	QueueConfigured bool
	QueueEnabled    bool

	// MergeStrategy is the rig's configured strategy ("pr", "direct", "").
	MergeStrategy string

	// PRNumber/PRURL describe an open PR for Branch (0/"" = none found).
	PRNumber int
	PRURL    string

	// PRLookupFailed records that the PR state could not be determined at all
	// (no gh, no network, not a GitHub remote). A pr-strategy rig still refuses
	// — unverifiable is not the same as verified-absent, but neither is a basis
	// for queueing work past a human gate — the wording just says which it was.
	PRLookupFailed bool

	// FlagOptOut is --no-mr; BeadOptOut is no_mr: true on the source bead.
	FlagOptOut bool
	BeadOptOut bool
}

// mrWispVerdict is the guard's decision.
type mrWispVerdict struct {
	Allow  bool
	Code   mrWispDenyCode
	Reason string
}

// isPRStrategy reports whether a merge strategy string means "PR is the vehicle".
func isPRStrategy(strategy string) bool {
	return strings.EqualFold(strings.TrimSpace(strategy), "pr")
}

// evaluateMRWispCreation decides whether an MR wisp may be created.
//
// The two deny families mirror the bead's two requirements: an explicit
// opt-out must be honored by the machinery (not just by the agent reading it),
// and a pr-strategy rig must never receive a wisp that has no PR behind it.
func evaluateMRWispCreation(env mrWispEnv) mrWispVerdict {
	switch {
	case env.FlagOptOut:
		return mrWispVerdict{
			Code:   mrWispDenyFlag,
			Reason: "--no-mr was passed: refusing to auto-create an MR wisp",
		}
	case env.BeadOptOut:
		return mrWispVerdict{
			Code:   mrWispDenyBead,
			Reason: "source bead carries no_mr: true: refusing to auto-create an MR wisp",
		}
	case env.QueueConfigured && !env.QueueEnabled:
		return mrWispVerdict{
			Code: mrWispDenyQueueDisabled,
			Reason: fmt.Sprintf("rig %s runs no merge queue (merge_queue.enabled=false): "+
				"the branch/PR is the delivery vehicle, not an MR wisp", env.Rig),
		}
	case isPRStrategy(env.MergeStrategy) && env.PRNumber == 0:
		state := "has no open PR"
		if env.PRLookupFailed {
			state = "has no verifiable open PR (PR lookup failed)"
		}
		return mrWispVerdict{
			Code: mrWispDenyNoPR,
			Reason: fmt.Sprintf("rig %s uses merge_strategy=pr but branch %s %s: "+
				"a PR-less MR wisp is a path around the PR approval gate", env.Rig, env.Branch, state),
		}
	}
	return mrWispVerdict{Allow: true}
}

// resolveRigMergeQueue reads a rig's merge-queue settings. configured is false
// when no settings could be read — callers must then stay permissive.
func resolveRigMergeQueue(townRoot, rigName string) (mq *config.MergeQueueConfig, configured bool) {
	settingsPath := filepath.Join(townRoot, rigName, "settings", "config.json")
	settings, err := config.LoadRigSettings(settingsPath)
	if err != nil || settings == nil || settings.MergeQueue == nil {
		return nil, false
	}
	return settings.MergeQueue, true
}

// findOpenPRForBranch returns the open PR number and URL for a branch, or
// (0, "") when there is none or the lookup fails. Lookup failures are reported
// separately so callers can distinguish "no PR" from "could not tell".
func findOpenPRForBranch(g *git.Git, branch string) (number int, url string, err error) {
	if g == nil || branch == "" {
		return 0, "", nil
	}
	number, url, err = g.FindOpenPR(branch)
	if err != nil {
		return 0, "", err
	}
	return number, url, nil
}

// resolveMRWispEnv assembles the guard input from the rig settings, the source
// bead, and the branch's PR state. prLookupErr is non-nil when the PR lookup
// itself failed (no `gh`, no network, not a GitHub remote); callers log it.
func resolveMRWispEnv(townRoot, rigName, branch string, g *git.Git, sourceIssue *beads.Issue, flagOptOut bool) (mrWispEnv, error) {
	env := mrWispEnv{
		Rig:        rigName,
		Branch:     branch,
		FlagOptOut: flagOptOut,
	}

	if mq, configured := resolveRigMergeQueue(townRoot, rigName); configured {
		env.QueueConfigured = true
		env.QueueEnabled = mq.Enabled
		env.MergeStrategy = mq.MergeStrategy
	}

	if af := beads.ParseAttachmentFields(sourceIssue); af != nil {
		env.BeadOptOut = af.NoMR
	}

	// The PR lookup only matters for pr-strategy rigs; skip the `gh` round-trip
	// otherwise so direct rigs keep their current gt done latency.
	var prLookupErr error
	if isPRStrategy(env.MergeStrategy) {
		env.PRNumber, env.PRURL, prLookupErr = findOpenPRForBranch(g, branch)
		env.PRLookupFailed = prLookupErr != nil
	}
	return env, prLookupErr
}

// mrDeliveryProvenance renders the MR-bead lines that record which delivery
// vehicle a submission was made under. The refinery refuses to merge a wisp
// that declares merge_strategy=pr without a PR reference, so stamping these
// is what makes the refinery-side validation possible.
func mrDeliveryProvenance(env mrWispEnv) string {
	var b strings.Builder
	if strategy := strings.TrimSpace(env.MergeStrategy); strategy != "" {
		fmt.Fprintf(&b, "\nmerge_strategy: %s", strategy)
	}
	if env.PRNumber > 0 {
		fmt.Fprintf(&b, "\npr: %d", env.PRNumber)
	}
	if env.PRURL != "" {
		fmt.Fprintf(&b, "\npr_url: %s", env.PRURL)
	}
	return b.String()
}

// mrWispSkipNote renders the audit note recorded on the source bead (and mailed
// to the dispatcher) when MR-wisp creation is refused.
func mrWispSkipNote(branch, code, reason string, env mrWispEnv) string {
	var b strings.Builder
	fmt.Fprintf(&b, "No MR wisp created (%s): %s\nBranch: %s", code, reason, branch)
	if env.PRURL != "" {
		fmt.Fprintf(&b, "\nPR: %s", env.PRURL)
	} else if env.PRNumber > 0 {
		fmt.Fprintf(&b, "\nPR: #%d", env.PRNumber)
	}
	return b.String()
}

// notifyMRWispSkipped records the refusal on the source bead and tells the
// dispatcher the work is ready for review. Refusing to queue work must never
// mean the work goes unannounced — that is how a contained incident turns into
// silently stranded work.
func notifyMRWispSkipped(bd *beads.Beads, townRoot string, sourceIssue *beads.Issue, issueID, branch string, env mrWispEnv, code, reason string) {
	note := mrWispSkipNote(branch, code, reason, env)

	if bd != nil && issueID != "" {
		if _, err := bd.Run("comments", "add", issueID, note); err != nil {
			style.PrintWarning("could not record MR-wisp refusal on %s: %v", issueID, err)
		}
	}

	dispatcher := ""
	if af := beads.ParseAttachmentFields(sourceIssue); af != nil {
		dispatcher = af.DispatchedBy
	}
	if dispatcher == "" {
		return
	}
	router := mail.NewRouter(townRoot)
	defer router.WaitPendingNotifications()
	msg := &mail.Message{
		To:      dispatcher,
		From:    detectSender(),
		Subject: fmt.Sprintf("READY_FOR_REVIEW: %s", issueID),
		Body:    fmt.Sprintf("%s\nIssue: %s\nReady for review — merge queue intentionally skipped.", note, issueID),
	}
	if err := router.Send(msg); err != nil {
		style.PrintWarning("could not notify dispatcher %s: %v", dispatcher, err)
	}
}

// shouldDeferSourceCloseOnDone reports whether gt done must leave the source
// bead open after submitting work.
//
// op-uhd2 defect #6: closing the source bead at MR-CREATION time is premature
// whenever the delivery vehicle is a PR — the PR is still open, the polecat may
// still be shepherding CI, and a closed bead paired with a live agent is what
// drove the phantom-done -> re-idle -> clone-reset chain. On pr-strategy rigs
// the refinery closes the source issue when the PR actually merges
// (Engineer.HandleMRInfoSuccess), so deferring loses nothing and the bead stops
// lying about work that has not landed.
//
// Direct-merge rigs are unchanged: there the gt done push IS the landing.
func shouldDeferSourceCloseOnDone(exitType, mergeStrategy string, mrCreated bool) bool {
	if exitType != ExitCompleted || !mrCreated {
		return false
	}
	return isPRStrategy(mergeStrategy)
}
