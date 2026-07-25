package refinery

import (
	"context"
	"strings"
	"testing"

	beadsdk "github.com/steveyegge/beads"
	"github.com/steveyegge/gastown/internal/beads"
)

// prVehicleMRIssue builds an MR wisp bead carrying explicit delivery-vehicle
// provenance, the way gt done stamps it after op-krtw.
func prVehicleMRIssue(id, branch, target, sourceIssue, strategy string, prNumber int) *beadsdk.Issue {
	desc := beads.FormatMRFields(&beads.MRFields{
		Branch:        branch,
		Target:        target,
		SourceIssue:   sourceIssue,
		Worker:        "polecats/test",
		Rig:           "test-rig",
		MergeStrategy: strategy,
		PRNumber:      prNumber,
	})
	return prepushIssue(id, desc, "gt:merge-request")
}

type stubPRProvider struct {
	prNumber int
	findErr  error
	merged   bool
}

func (p *stubPRProvider) FindPRNumber(string) (int, error) { return p.prNumber, p.findErr }
func (p *stubPRProvider) IsPRApproved(int) (bool, error)   { return true, nil }
func (p *stubPRProvider) MergePR(int, string) (string, error) {
	p.merged = true
	return "cafebabe", nil
}

// TestDoMerge_RejectsPRLessWispDeclaringPRStrategy is the incident test
// (2026-07-25, refinery hq-wisp-pnay). A polecat's gt done auto-created an MR
// wisp with no PR reference for work on a merge_strategy=pr rig. If the
// engineer's own config has drifted to direct — exactly the case that makes
// this dangerous — the wisp would be squash-merged onto the target, around the
// PR approval gate. The wisp's own declaration must be enough to refuse it.
func TestDoMerge_RejectsPRLessWispDeclaringPRStrategy(t *testing.T) {
	workDir, _, cleanup := testGitRepo(t)
	defer cleanup()
	createFeatureBranch(t, workDir, "polecat/mica_op-abc", "app.txt", "work\n")
	before := run(t, workDir, "git", "rev-parse", "origin/main")

	store := newPrepushStore(
		prepushIssue("gt-src", ""),
		prVehicleMRIssue("gt-mr", "polecat/mica_op-abc", "main", "gt-src", "pr", 0),
	)
	e := newPrepushEngineer(t, workDir, store)
	e.config.MergeStrategy = "" // config drift: engineer believes it merges directly

	mr := &MRInfo{
		ID: "gt-mr", Branch: "polecat/mica_op-abc", Target: "main",
		SourceIssue: "gt-src", DeclaredStrategy: "pr",
	}
	result := e.doMerge(context.Background(), mr)

	if result.Success {
		t.Fatal("PR-less wisp declaring pr-strategy must not merge")
	}
	if !result.NoMerge {
		t.Errorf("expected a policy rejection (NoMerge), got %+v", result)
	}
	if !strings.Contains(result.Error, "no PR reference") {
		t.Errorf("error should name the missing PR reference, got %q", result.Error)
	}
	if got := store.closeReasons["gt-mr"]; !strings.HasPrefix(got, "rejected:") {
		t.Errorf("MR bead should be closed as rejected, got %q", got)
	}
	assertOriginMainUnchangedAndReset(t, workDir, before)
}

// TestDoMerge_RejectsPRLessWispOnPRStrategyRig covers the same shape from the
// other direction: the rig config says pr-strategy, the wisp predates the
// provenance stamp, and the provider confirms there is no PR.
func TestDoMerge_RejectsPRLessWispOnPRStrategyRig(t *testing.T) {
	workDir, _, cleanup := testGitRepo(t)
	defer cleanup()
	createFeatureBranch(t, workDir, "polecat/mica_op-def", "app.txt", "work\n")
	before := run(t, workDir, "git", "rev-parse", "origin/main")

	store := newPrepushStore(
		prepushIssue("gt-src", ""),
		prepushMRIssue("gt-mr", "polecat/mica_op-def", "main", "gt-src"),
	)
	e := newPrepushEngineer(t, workDir, store)
	e.config.MergeStrategy = "pr"
	provider := &stubPRProvider{prNumber: 0}
	e.prProvider = provider

	result := e.doMerge(context.Background(), &MRInfo{
		ID: "gt-mr", Branch: "polecat/mica_op-def", Target: "main", SourceIssue: "gt-src",
	})

	if result.Success || !result.NoMerge {
		t.Fatalf("expected policy rejection, got %+v", result)
	}
	if provider.merged {
		t.Fatal("MergePR must never be reached for a PR-less wisp")
	}
	if got := store.closeReasons["gt-mr"]; !strings.Contains(got, "no open PR") {
		t.Errorf("close reason = %q, want the no-open-PR rejection", got)
	}
	assertOriginMainUnchangedAndReset(t, workDir, before)
}

// TestProcessBatch_RefusesPRLessWisp: the batch path stacks squash-merges and
// pushes straight to the target, so it is the most direct route around a PR
// approval gate. One PR-less pr-strategy wisp must stop the whole batch before
// anything is pushed.
func TestProcessBatch_RefusesPRLessWisp(t *testing.T) {
	workDir, _, cleanup := testGitRepo(t)
	defer cleanup()
	createFeatureBranch(t, workDir, "polecat/nux_op-a", "a.txt", "a\n")
	createFeatureBranch(t, workDir, "polecat/mica_op-b", "b.txt", "b\n")
	before := run(t, workDir, "git", "rev-parse", "origin/main")

	store := newPrepushStore(
		prepushIssue("gt-src-a", ""),
		prepushIssue("gt-src-b", ""),
		prVehicleMRIssue("gt-mr-a", "polecat/nux_op-a", "main", "gt-src-a", "direct", 0),
		prVehicleMRIssue("gt-mr-b", "polecat/mica_op-b", "main", "gt-src-b", "pr", 0),
	)
	e := newPrepushEngineer(t, workDir, store)
	e.testAllowSyntheticMRs = false

	batch := []*MRInfo{
		{ID: "gt-mr-a", Branch: "polecat/nux_op-a", Target: "main", SourceIssue: "gt-src-a", DeclaredStrategy: "direct"},
		{ID: "gt-mr-b", Branch: "polecat/mica_op-b", Target: "main", SourceIssue: "gt-src-b", DeclaredStrategy: "pr"},
	}
	result := e.ProcessBatch(context.Background(), batch, "main", DefaultBatchConfig())

	if len(result.Merged) != 0 {
		t.Fatalf("nothing should merge when a batch member is refused, merged %d", len(result.Merged))
	}
	if got := store.closeReasons["gt-mr-b"]; !strings.Contains(got, "no PR reference") {
		t.Errorf("offending MR close reason = %q, want the PR-reference rejection", got)
	}
	if _, closed := store.closeReasons["gt-mr-a"]; closed {
		t.Error("the innocent batch member must not be closed")
	}
	assertOriginMainUnchangedAndReset(t, workDir, before)
}

// TestValidateMRDeliveryVehicle_AllowsLegitimateSubmissions guards against the
// fix over-reaching: direct rigs, PR-backed wisps, and unverifiable states must
// all still flow.
func TestValidateMRDeliveryVehicle_AllowsLegitimateSubmissions(t *testing.T) {
	workDir, g, cleanup := testGitRepo(t)
	defer cleanup()

	tests := []struct {
		name      string
		configure func(e *Engineer)
		mr        *MRInfo
	}{
		{
			name:      "direct-strategy wisp with no PR",
			configure: func(e *Engineer) { e.config.MergeStrategy = "" },
			mr:        &MRInfo{ID: "gt-mr", Branch: "feat/x", Target: "main"},
		},
		{
			name:      "pr-strategy wisp carrying its PR number",
			configure: func(e *Engineer) { e.config.MergeStrategy = "pr" },
			mr:        &MRInfo{ID: "gt-mr", Branch: "feat/x", Target: "main", DeclaredStrategy: "pr", PRNumber: 21606},
		},
		{
			name:      "pr-strategy wisp carrying only its PR URL",
			configure: func(e *Engineer) { e.config.MergeStrategy = "pr" },
			mr:        &MRInfo{ID: "gt-mr", Branch: "feat/x", Target: "main", DeclaredStrategy: "pr", PRURL: "https://github.com/acme/capital/pull/7"},
		},
		{
			name:      "pr-strategy rig with no provider wired stays unverifiable, not rejected",
			configure: func(e *Engineer) { e.config.MergeStrategy = "pr"; e.prProvider = nil },
			mr:        &MRInfo{ID: "gt-mr", Branch: "feat/x", Target: "main"},
		},
		{
			name: "pr-strategy rig whose provider errors is not rejected",
			configure: func(e *Engineer) {
				e.config.MergeStrategy = "pr"
				e.prProvider = &stubPRProvider{findErr: context.DeadlineExceeded}
			},
			mr: &MRInfo{ID: "gt-mr", Branch: "feat/x", Target: "main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEngineer(t, workDir, g)
			tt.configure(e)
			if result := e.validateMRDeliveryVehicle(tt.mr); !result.Success {
				t.Fatalf("expected pass-through, got %+v", result)
			}
		})
	}
}

// TestValidateMRDeliveryVehicle_BackfillsDiscoveredPR: when a pre-stamp wisp
// does have a PR, the discovered number is recorded so later steps and logs
// name the PR that carries the work.
func TestValidateMRDeliveryVehicle_BackfillsDiscoveredPR(t *testing.T) {
	workDir, g, cleanup := testGitRepo(t)
	defer cleanup()

	e := newTestEngineer(t, workDir, g)
	e.config.MergeStrategy = "pr"
	e.prProvider = &stubPRProvider{prNumber: 4242}

	mr := &MRInfo{ID: "gt-mr", Branch: "feat/x", Target: "main"}
	if result := e.validateMRDeliveryVehicle(mr); !result.Success {
		t.Fatalf("expected pass-through, got %+v", result)
	}
	if mr.PRNumber != 4242 {
		t.Errorf("PRNumber = %d, want 4242 backfilled from the provider", mr.PRNumber)
	}
}

func TestMRHasPRReference(t *testing.T) {
	if mrHasPRReference(nil) {
		t.Error("nil MR has no PR reference")
	}
	if mrHasPRReference(&MRInfo{}) {
		t.Error("empty MR has no PR reference")
	}
	if mrHasPRReference(&MRInfo{PRURL: "   "}) {
		t.Error("blank PR URL is not a reference")
	}
	if !mrHasPRReference(&MRInfo{PRNumber: 1}) {
		t.Error("PR number is a reference")
	}
	if !mrHasPRReference(&MRInfo{PRURL: "https://github.com/acme/capital/pull/1"}) {
		t.Error("PR URL is a reference")
	}
}
