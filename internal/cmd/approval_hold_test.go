package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
)

// holdRecorder captures the side effects of an approvalHoldDeps.enforce run.
type holdRecorder struct {
	overrideHolders []string
	overrideCalls   int
	nudges          map[string][]string
}

func holdDeps(issue *beads.Issue, loadErr error, override bool, rec *holdRecorder) (*approvalHoldDeps, *bytes.Buffer) {
	rec.nudges = map[string][]string{}
	out := &bytes.Buffer{}
	return &approvalHoldDeps{
		loadIssue: func() (*beads.Issue, error) { return issue, loadErr },
		issueID:   "op-96zr",
		agent:     "openclaw/polecats/slit",
		branch:    "polecat/slit/op-96zr+x",
		override:  override,
		out:       out,
		recordOverride: func(holders []string) {
			rec.overrideCalls++
			rec.overrideHolders = holders
		},
		nudge: func(addr, msg string) { rec.nudges[addr] = append(rec.nudges[addr], msg) },
	}, out
}

func heldIssue(labels ...string) *beads.Issue {
	return &beads.Issue{ID: "op-96zr", Labels: labels}
}

func TestApprovalHoldLabels(t *testing.T) {
	for _, tt := range []struct {
		name   string
		labels []string
		want   int
	}{
		{"no labels", nil, 0},
		{"unrelated labels only", []string{"gt:agent", "needs-ci-green:5:123"}, 0},
		{"bare hold", []string{approvalHoldLabel}, 1},
		{"metadata hold", []string{"needs-approval:openclaw/witness:1753200000"}, 1},
		{"mixed", []string{"other", "needs-approval", "needs-approval:mayor/:1753200000"}, 2},
		{"prefix must match exactly", []string{"needs-approval-ish"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := approvalHoldLabels(heldIssue(tt.labels...))
			if len(got) != tt.want {
				t.Errorf("approvalHoldLabels(%v) = %v, want %d labels", tt.labels, got, tt.want)
			}
		})
	}
	if got := approvalHoldLabels(nil); got != nil {
		t.Errorf("approvalHoldLabels(nil) = %v, want nil", got)
	}
}

func TestParseApprovalHoldLabel(t *testing.T) {
	setter, setAt := parseApprovalHoldLabel("needs-approval:openclaw/witness:1753200000")
	if setter != "openclaw/witness" {
		t.Errorf("setter = %q, want openclaw/witness", setter)
	}
	if setAt.Unix() != 1753200000 {
		t.Errorf("setAt = %v, want unix 1753200000", setAt)
	}

	// Setter without a timestamp suffix keeps the whole remainder as setter.
	setter, setAt = parseApprovalHoldLabel("needs-approval:mayor")
	if setter != "mayor" || !setAt.IsZero() {
		t.Errorf("got (%q, %v), want (mayor, zero time)", setter, setAt)
	}

	// Bare label carries no metadata.
	setter, setAt = parseApprovalHoldLabel(approvalHoldLabel)
	if setter != "" || !setAt.IsZero() {
		t.Errorf("bare label: got (%q, %v), want empty", setter, setAt)
	}
}

func TestDescribeApprovalHold(t *testing.T) {
	desc := describeApprovalHold("needs-approval:openclaw/witness:1753200000")
	if !strings.Contains(desc, "openclaw/witness") || !strings.Contains(desc, time.Unix(1753200000, 0).UTC().Format("2006")) {
		t.Errorf("describeApprovalHold = %q, want setter and year", desc)
	}
	if desc := describeApprovalHold(approvalHoldLabel); !strings.Contains(desc, "bare") {
		t.Errorf("bare label description = %q, want mention of bare label", desc)
	}
}

func TestApprovalHoldNotHeldPasses(t *testing.T) {
	rec := &holdRecorder{}
	deps, _ := holdDeps(heldIssue("gt:agent", "some-other-label"), nil, false, rec)
	if err := deps.enforce(); err != nil {
		t.Fatalf("enforce() on unheld issue = %v, want nil", err)
	}
	if len(rec.nudges) != 0 || rec.overrideCalls != 0 {
		t.Errorf("unheld issue produced side effects: %+v", rec)
	}
}

func TestApprovalHoldLoadErrorFailsOpen(t *testing.T) {
	rec := &holdRecorder{}
	deps, out := holdDeps(nil, errors.New("dolt down"), false, rec)
	if err := deps.enforce(); err != nil {
		t.Fatalf("enforce() with load error = %v, want nil (fail open)", err)
	}
	if !strings.Contains(out.String(), "FAILING OPEN") {
		t.Errorf("fail-open must be loud; output was %q", out.String())
	}
}

func TestApprovalHoldRefusesSubmission(t *testing.T) {
	rec := &holdRecorder{}
	deps, _ := holdDeps(heldIssue("needs-approval:openclaw/witness:1753200000"), nil, false, rec)
	err := deps.enforce()
	if err == nil {
		t.Fatal("enforce() on held issue = nil, want hard refusal")
	}
	msg := err.Error()
	for _, want := range []string{"APPROVAL_HOLD", "op-96zr", "openclaw/witness", "gt approval clear", "--override-approval-hold"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message missing %q:\n%s", want, msg)
		}
	}
	if rec.overrideCalls != 0 {
		t.Error("refusal must not record an override")
	}
	// The holder gets nudged that the work is ready for review.
	if len(rec.nudges["openclaw/witness"]) != 1 {
		t.Errorf("holder nudges = %v, want exactly one to openclaw/witness", rec.nudges)
	}
}

func TestApprovalHoldBareLabelRefusesWithoutNudge(t *testing.T) {
	rec := &holdRecorder{}
	deps, _ := holdDeps(heldIssue(approvalHoldLabel), nil, false, rec)
	err := deps.enforce()
	if err == nil {
		t.Fatal("enforce() on bare-held issue = nil, want hard refusal")
	}
	if !strings.Contains(err.Error(), "APPROVAL_HOLD") {
		t.Errorf("refusal message = %q, want APPROVAL_HOLD", err.Error())
	}
	if len(rec.nudges) != 0 {
		t.Errorf("bare label has no holder to nudge, got %v", rec.nudges)
	}
}

func TestApprovalHoldOverrideProceedsAndRecords(t *testing.T) {
	rec := &holdRecorder{}
	deps, out := holdDeps(heldIssue("needs-approval:openclaw/witness:1753200000"), nil, true, rec)
	if err := deps.enforce(); err != nil {
		t.Fatalf("enforce() with override = %v, want nil", err)
	}
	if rec.overrideCalls != 1 {
		t.Fatalf("overrideCalls = %d, want 1 (the activity-log record)", rec.overrideCalls)
	}
	if len(rec.overrideHolders) != 1 || rec.overrideHolders[0] != "openclaw/witness" {
		t.Errorf("override holders = %v, want [openclaw/witness]", rec.overrideHolders)
	}
	if len(rec.nudges["openclaw/witness"]) != 1 || len(rec.nudges["mayor/"]) != 1 {
		t.Errorf("override must nudge holder and mayor, got %v", rec.nudges)
	}
	if !strings.Contains(out.String(), "OVERRIDE") {
		t.Errorf("override must be loud on stdout; output was %q", out.String())
	}
	// Override does NOT clear the label — the approver still owns it.
	if got := approvalHoldLabels(heldIssue("needs-approval:openclaw/witness:1753200000")); len(got) != 1 {
		t.Errorf("hold label should survive an override run")
	}
}

func TestEnforceApprovalHoldSkipsWithoutIssue(t *testing.T) {
	// Issue-less direct-strategy branch: nothing to hold on, and beads must
	// not be touched (nil bd would panic if it were).
	if err := enforceApprovalHold(nil, nil, "", "agent", "branch", false); err != nil {
		t.Fatalf("enforceApprovalHold with no issue = %v, want nil", err)
	}
}

func TestApprovalClearAllowed(t *testing.T) {
	for actor, want := range map[string]bool{
		"openclaw/polecats/slit": false, // held workers never release themselves
		"openclaw/witness":       true,
		"mayor/":                 true,
		"gastown/crew/joe":       true,
		"overseer":               true,
		"":                       true, // unidentified human operator
	} {
		if got := approvalClearAllowed(actor); got != want {
			t.Errorf("approvalClearAllowed(%q) = %v, want %v", actor, got, want)
		}
	}
}
