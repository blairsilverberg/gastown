package cmd

// gt approval — manage the op-96zr enforceable approval-hold on a bead.
// See approval_hold.go for the gate that makes the hold binding at
// submission time (gt done / gt mq submit).

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var approvalCmd = &cobra.Command{
	Use:   "approval",
	Short: "Manage approval-holds on beads",
	Long: `Manage the needs-approval hold on a bead (op-96zr).

While a bead carries a needs-approval label, gt done and gt mq submit
HARD-REFUSE to submit that bead's branch to the merge queue. Use it to
pin work that must not merge without sign-off (e.g. a test change, a
witness/mayor hold on a pending merge).

Anyone can place a hold — witness, mayor, or a polecat holding its own
work when it detects an approval-required condition. Clearing is
restricted to the approver: polecats cannot clear holds (not even
self-set ones). Both actions are recorded as comments on the bead.`,
	Example: `  gt approval hold op-96zr -m "test change needs Blair sign-off"
  gt approval status op-96zr
  gt approval clear op-96zr -m "approved by Blair via Telegram"`,
}

var (
	approvalHoldMsg  string
	approvalClearMsg string
)

var approvalHoldCmd = &cobra.Command{
	Use:          "hold <bead-id>",
	Short:        "Place a needs-approval hold on a bead",
	Args:         cobra.ExactArgs(1),
	RunE:         runApprovalHold,
	SilenceUsage: true, // Don't print usage on operational errors (confuses agents)
}

var approvalClearCmd = &cobra.Command{
	Use:          "clear <bead-id>",
	Short:        "Clear a needs-approval hold (approver only — polecats refused)",
	Args:         cobra.ExactArgs(1),
	RunE:         runApprovalClear,
	SilenceUsage: true,
}

var approvalStatusCmd = &cobra.Command{
	Use:          "status <bead-id>",
	Short:        "Show whether a bead is on approval-hold",
	Args:         cobra.ExactArgs(1),
	RunE:         runApprovalStatus,
	SilenceUsage: true,
}

func init() {
	approvalHoldCmd.Flags().StringVarP(&approvalHoldMsg, "message", "m", "", "Reason for the hold (recorded on the bead)")
	approvalClearCmd.Flags().StringVarP(&approvalClearMsg, "message", "m", "", "Approval context (recorded on the bead)")
	approvalCmd.AddCommand(approvalHoldCmd)
	approvalCmd.AddCommand(approvalClearCmd)
	approvalCmd.AddCommand(approvalStatusCmd)
	rootCmd.AddCommand(approvalCmd)
}

// approvalActor identifies who is running the command, preferring the
// explicit BD_ACTOR (authoritative for agent sessions) over role/cwd
// detection. Empty means an unidentified human operator.
func approvalActor() string {
	if actor := os.Getenv("BD_ACTOR"); actor != "" {
		return actor
	}
	return detectSender()
}

// approvalClearAllowed decides whether the actor may clear a hold.
// Polecats are always refused — the entire point of the hold is that the
// held worker cannot release itself; everyone else (witness, mayor, crew,
// overseer, humans) may clear.
func approvalClearAllowed(actor string) bool {
	return !isPolecatActor(actor)
}

func approvalBeads() (*beads.Beads, error) {
	_, cwd, err := workspace.FindFromCwdWithFallback()
	if err != nil {
		return nil, fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	return beads.New(cwd), nil
}

func runApprovalHold(cmd *cobra.Command, args []string) error {
	beadID := args[0]
	bd, err := approvalBeads()
	if err != nil {
		return err
	}
	issue, err := bd.Show(beadID)
	if err != nil {
		return fmt.Errorf("reading %s: %w", beadID, err)
	}
	if existing := approvalHoldLabels(issue); len(existing) > 0 {
		return fmt.Errorf("%s is already on approval-hold (held by: %s)\nClear it first with `gt approval clear %s` (approver only)",
			beadID, describeApprovalHold(existing[0]), beadID)
	}

	actor := approvalActor()
	label := fmt.Sprintf("%s%s:%d", approvalHoldLabelPrefix, actor, time.Now().Unix())
	if err := bd.Update(beadID, beads.UpdateOptions{AddLabels: []string{label}}); err != nil {
		return fmt.Errorf("setting hold on %s: %w", beadID, err)
	}

	comment := fmt.Sprintf("APPROVAL-HOLD set by %s — gt done / gt mq submit will refuse to submit this bead's branch until an approver runs `gt approval clear %s`", actor, beadID)
	if approvalHoldMsg != "" {
		comment += "\nReason: " + approvalHoldMsg
	}
	if _, err := bd.Run("comments", "add", beadID, comment); err != nil {
		style.PrintWarning("hold set, but couldn't record comment on %s: %v", beadID, err)
	}

	fmt.Printf("%s Approval-hold placed on %s by %s\n", style.Bold.Render("✓"), beadID, actor)
	fmt.Printf("  gt done and gt mq submit will refuse this bead until the hold is cleared.\n")
	return nil
}

func runApprovalClear(cmd *cobra.Command, args []string) error {
	beadID := args[0]
	actor := approvalActor()
	if !approvalClearAllowed(actor) {
		return fmt.Errorf("approval-holds are cleared by the approver (witness/mayor), not polecats (you are %s)\n"+
			"If your work has been approved, ask the approver to run `gt approval clear %s`.\n"+
			"To submit past the hold with explicit authorization, use `gt done --override-approval-hold` (recorded on the bead)",
			actor, beadID)
	}

	bd, err := approvalBeads()
	if err != nil {
		return err
	}
	issue, err := bd.Show(beadID)
	if err != nil {
		return fmt.Errorf("reading %s: %w", beadID, err)
	}
	held := approvalHoldLabels(issue)
	if len(held) == 0 {
		fmt.Printf("%s is not on approval-hold — nothing to clear\n", beadID)
		return nil
	}

	if err := bd.Update(beadID, beads.UpdateOptions{RemoveLabels: held}); err != nil {
		return fmt.Errorf("clearing hold on %s: %w", beadID, err)
	}

	comment := fmt.Sprintf("APPROVAL-HOLD cleared by %s (was held by: %s) — submission unblocked", actor, describeApprovalHold(held[0]))
	if approvalClearMsg != "" {
		comment += "\nContext: " + approvalClearMsg
	}
	if _, err := bd.Run("comments", "add", beadID, comment); err != nil {
		style.PrintWarning("hold cleared, but couldn't record comment on %s: %v", beadID, err)
	}

	// Release the assignee's session-level HOLD (op-uhd2): while
	// agent_state=holding, every gt done exit path refuses, so clearing the
	// bead label alone would leave the worker permanently parked.
	if assignee := strings.TrimSpace(issue.Assignee); assignee != "" {
		if townRoot, cwd, wErr := workspace.FindFromCwdWithFallback(); wErr == nil {
			releaseHoldingAgentState(townRoot, cwd, assignee)
		}
	}

	// Let the held worker know it can re-run gt done.
	if assignee := strings.TrimSpace(issue.Assignee); assignee != "" {
		nudgeBestEffort(assignee, fmt.Sprintf("APPROVAL-HOLD on %s cleared by %s — re-run `gt done` to submit", beadID, actor))
	}

	fmt.Printf("%s Approval-hold cleared on %s by %s\n", style.Bold.Render("✓"), beadID, actor)
	return nil
}

func runApprovalStatus(cmd *cobra.Command, args []string) error {
	beadID := args[0]
	bd, err := approvalBeads()
	if err != nil {
		return err
	}
	issue, err := bd.Show(beadID)
	if err != nil {
		return fmt.Errorf("reading %s: %w", beadID, err)
	}
	held := approvalHoldLabels(issue)
	if len(held) == 0 {
		fmt.Printf("%s: no approval-hold — submission unblocked\n", beadID)
		return nil
	}
	fmt.Printf("%s %s is ON APPROVAL-HOLD\n", style.Warning.Render("⚠"), beadID)
	for _, label := range held {
		fmt.Printf("  held by: %s\n", describeApprovalHold(label))
	}
	fmt.Printf("  gt done / gt mq submit will refuse to submit until an approver runs `gt approval clear %s`\n", beadID)
	return nil
}
