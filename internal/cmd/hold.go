package cmd

// gt hold — session-level HOLD for polecats awaiting human approval
// (op-uhd2 / hq-khtga).
//
// The op-96zr needs-approval label blocks the SUBMISSION paths of gt done,
// but nothing represented "awaiting human approval" as a session state:
// four incidents on 2026-07-25 showed holding/interactive polecats firing
// the same exit-complete path as genuine completion (phantom POLECAT_DONE,
// bead auto-close), which fed the dispatcher a phantom-idle slot and got a
// live clone reset.
//
// gt hold marks the polecat itself as HOLDING:
//   - agent bead agent_state = "holding" — blocks EVERY gt done exit path
//     (not just submission), protects the session from cleanup/reaping
//     (AgentState.ProtectsFromCleanup), keeps the slot out of the reuse
//     pool, and renders as "holding" (never "idle") in status output.
//   - the hooked bead gets the op-96zr needs-approval label (if absent), so
//     the merge-queue submission gate and dispatch guards also engage.
//
// Release is the op-96zr approval relay: `gt approval clear <bead>`
// (witness/mayor/human — polecats cannot clear their own holds), which
// removes the label AND resets the assignee's agent_state to working.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	holdSelf bool
	holdMsg  string
)

var holdCmd = &cobra.Command{
	Use:   "hold",
	Short: "Park this polecat awaiting human approval (blocks gt done)",
	Long: `Mark this polecat session as HOLDING for human approval (op-uhd2).

While holding:
  - gt done refuses EVERY exit path (completed, deferred, escalated)
  - the session is protected from idle reaping and destructive reuse
  - pool/status output shows "holding" — never "idle"
  - the hooked bead carries the op-96zr needs-approval label, so the
    merge queue also refuses the branch

Use this the moment you decide work needs sign-off (e.g. a test change
awaiting approval) so a session restart or stop-hook cannot exit you
as COMPLETED and orphan the approval obligation.

Release: an approver runs 'gt approval clear <bead-id>' — this clears the
label and the holding state, then nudges you to re-run gt done.`,
	Example: `  gt hold -m "test change needs Blair sign-off"
  gt hold --self`,
	RunE:         runHold,
	SilenceUsage: true,
}

func init() {
	holdCmd.Flags().BoolVar(&holdSelf, "self", false, "Explicitly mark this as a self-hold (default; flag exists for clarity)")
	holdCmd.Flags().StringVarP(&holdMsg, "message", "m", "", "Reason for the hold (recorded on the hooked bead)")
	rootCmd.AddCommand(holdCmd)
}

func runHold(cmd *cobra.Command, args []string) error {
	_ = holdSelf // bare `gt hold` and `gt hold --self` are the same action

	townRoot, cwd, err := workspace.FindFromCwdWithFallback()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	roleInfo, err := GetRoleWithContext(cwd, townRoot)
	if err != nil {
		return fmt.Errorf("cannot determine role: %w", err)
	}
	if roleInfo.Role != RolePolecat {
		return fmt.Errorf("gt hold is a polecat session state (you are %s)\nTo hold a BEAD from outside, use: gt approval hold <bead-id>", roleInfo.Role)
	}

	ctx := RoleContext{
		Role:     roleInfo.Role,
		Rig:      roleInfo.Rig,
		Polecat:  roleInfo.Polecat,
		TownRoot: townRoot,
		WorkDir:  cwd,
	}
	agentBeadID := getAgentBeadID(ctx)
	if agentBeadID == "" {
		return fmt.Errorf("cannot resolve agent bead for %s/%s", roleInfo.Rig, roleInfo.Polecat)
	}

	actor := approvalActor()
	if actor == "" {
		actor = roleInfo.ActorString()
	}

	// Find the hooked bead so the op-96zr label engages the submission gates.
	hookedBead := ""
	if hookIssue, ambiguous := selectAssignedIssue("", findAssignedBeadsForAgent(cwd, actor)); hookIssue != "" {
		hookedBead = hookIssue
	} else if ambiguous {
		style.PrintWarning("multiple active assignments — holding the session without a bead label; place one explicitly with gt approval hold <bead-id>")
	}

	// 1. Session-level hold: agent_state = holding.
	agentBd := beads.New(cwd).ForAgentBead()
	ensureAgentBeadExists(agentBd, agentBeadID, ctx)
	if err := agentBd.UpdateAgentState(agentBeadID, string(beads.AgentStateHolding)); err != nil {
		return fmt.Errorf("setting agent_state=holding on %s: %w", agentBeadID, err)
	}

	// Keep the heartbeat in "working" so the idle reaper never classifies the
	// held session as an exiting/idle zombie.
	if sessionName := os.Getenv("GT_SESSION"); sessionName != "" && townRoot != "" {
		polecat.TouchSessionHeartbeatWithState(townRoot, sessionName, polecat.HeartbeatWorking, "gt hold: awaiting approval", hookedBead)
	}

	// 2. Bead-level hold: op-96zr needs-approval label on the hooked bead.
	if hookedBead != "" {
		bd := beads.New(cwd)
		if issue, showErr := bd.Show(hookedBead); showErr == nil {
			if len(approvalHoldLabels(issue)) == 0 {
				label := fmt.Sprintf("%s%s:%d", approvalHoldLabelPrefix, actor, time.Now().Unix())
				if updErr := bd.Update(hookedBead, beads.UpdateOptions{AddLabels: []string{label}}); updErr != nil {
					style.PrintWarning("session hold set, but couldn't label %s: %v", hookedBead, updErr)
				}
			}
			comment := fmt.Sprintf("HOLD: %s entered session hold (agent_state=holding) — gt done blocked until an approver runs `gt approval clear %s`", actor, hookedBead)
			if holdMsg != "" {
				comment += "\nReason: " + holdMsg
			}
			if _, cErr := bd.Run("comments", "add", hookedBead, comment); cErr != nil {
				style.PrintWarning("couldn't record hold comment on %s: %v", hookedBead, cErr)
			}
		} else {
			style.PrintWarning("session hold set, but couldn't read hooked bead %s: %v", hookedBead, showErr)
		}
	}

	fmt.Printf("%s %s is now HOLDING", style.Bold.Render("✓"), actor)
	if hookedBead != "" {
		fmt.Printf(" on %s", hookedBead)
	}
	fmt.Println()
	fmt.Printf("  gt done is blocked; the session is protected from reaping and reuse.\n")
	if hookedBead != "" {
		fmt.Printf("  Release: an approver runs `gt approval clear %s`\n", hookedBead)
	}
	return nil
}

// releaseHoldingAgentState resets a holding polecat's agent_state to working.
// Called from `gt approval clear` (the release relay) so clearing the bead
// label also releases the session-level hold. Best-effort: warnings only.
func releaseHoldingAgentState(townRoot, cwd, assignee string) {
	parts := strings.Split(strings.TrimSpace(assignee), "/")
	if len(parts) != 3 || parts[1] != "polecats" {
		return
	}
	rigName, polecatName := parts[0], parts[2]
	prefix := beads.GetPrefixForRig(townRoot, rigName)
	if prefix == "" {
		return
	}
	agentBeadID := beads.PolecatBeadIDWithPrefix(prefix, rigName, polecatName)
	agentBd := beads.New(cwd).ForAgentBead()
	issue, err := agentBd.Show(agentBeadID)
	if err != nil || issue == nil {
		return
	}
	fields := beads.ParseAgentFields(issue.Description)
	if fields == nil || fields.AgentState != string(beads.AgentStateHolding) {
		return
	}
	if err := agentBd.UpdateAgentState(agentBeadID, string(beads.AgentStateWorking)); err != nil {
		style.PrintWarning("cleared the bead hold, but couldn't release %s's holding state: %v", assignee, err)
		return
	}
	fmt.Printf("  Session HOLD released on %s (agent_state holding → working)\n", assignee)
}

// enforceSessionHold is the op-uhd2 gt done gate: while the polecat is
// HOLDING, refuse EVERY exit path (completed, deferred, escalated) before any
// completion breadcrumb (done-intent label, heartbeat "exiting", push, close,
// POLECAT_DONE) is produced. The only bypass is --override-approval-hold,
// which represents explicit approver authorization and also clears the
// holding state via clearHolding. Pure decision — unit-testable.
func enforceSessionHold(isHolding, override bool, exitType string, clearHolding func() error) error {
	if !isHolding {
		return nil
	}
	if override {
		style.PrintWarning("overriding session HOLD (agent_state=holding) via --override-approval-hold")
		if clearHolding != nil {
			if err := clearHolding(); err != nil {
				style.PrintWarning("could not clear holding state: %v", err)
			}
		}
		return nil
	}
	return fmt.Errorf("HOLD: this polecat is holding for approval (agent_state=holding) — refusing gt done (%s exit)\n"+
		"You stay assigned and your session stays alive. Do NOT retry gt done while holding.\n"+
		"Release paths:\n"+
		"  gt approval clear <bead-id>        (approver — clears the hold, then re-run gt done)\n"+
		"  gt done --override-approval-hold   (only with explicit approver authorization; recorded)", exitType)
}

// enterHoldingForApprovalRefusal is the "auto-detected pending-approval"
// path (op-uhd2): when the op-96zr approval-hold gate refuses gt done, the
// polecat is by definition awaiting approval — record that as the session
// state so the slot renders as "holding", survives reaping, and cannot be
// destructively reused while parked. Best-effort: the gate's refusal stands
// regardless.
func enterHoldingForApprovalRefusal(cwd, townRoot, agentBeadID, issueID string) {
	if agentBeadID == "" {
		return
	}
	agentBd := beads.New(cwd).ForAgentBead()
	if err := agentBd.UpdateAgentState(agentBeadID, string(beads.AgentStateHolding)); err != nil {
		style.PrintWarning("could not enter holding state after approval-hold refusal: %v", err)
		return
	}
	// gt done stamped the heartbeat "exiting" earlier in its flow; put it
	// back to "working" so the idle reaper doesn't kill the held session.
	if sessionName := os.Getenv("GT_SESSION"); sessionName != "" && townRoot != "" {
		polecat.TouchSessionHeartbeatWithState(townRoot, sessionName, polecat.HeartbeatWorking, "holding: approval-hold refused gt done", issueID)
	}
	fmt.Printf("%s Session is now HOLDING (auto-detected pending approval) — protected from reaping and reuse until `gt approval clear`\n", style.Bold.Render("→"))
}

// agentStateIsHolding reports whether the agent bead is in the op-uhd2
// session HOLD. Fails open (false) when the bead can't be read — gt done's
// own approval-hold gate still protects submission, and a broken beads store
// shouldn't wedge every exit path.
func agentStateIsHolding(agentBd *beads.Beads, agentBeadID string) bool {
	if agentBeadID == "" {
		return false
	}
	issue, err := agentBd.Show(agentBeadID)
	if err != nil || issue == nil {
		return false
	}
	fields := beads.ParseAgentFields(issue.Description)
	return fields != nil && fields.AgentState == string(beads.AgentStateHolding)
}
