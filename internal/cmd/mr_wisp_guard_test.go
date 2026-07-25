package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// TestEvaluateMRWispCreation_IncidentShape pins the 2026-07-25 incident: a
// polecat on the capital rig (merge_queue disabled, merge_strategy=pr) ran
// gt done and an MR wisp with no PR reference reached the queue anyway. On a
// pr-strategy rig that wisp is a path around the PR approval gate.
func TestEvaluateMRWispCreation_IncidentShape(t *testing.T) {
	capital := mrWispEnv{
		Rig:             "capital",
		Branch:          "polecat/mica_op-abc",
		QueueConfigured: true,
		QueueEnabled:    false,
		MergeStrategy:   "pr",
	}

	verdict := evaluateMRWispCreation(capital)
	if verdict.Allow {
		t.Fatal("capital-shaped rig must not auto-create an MR wisp")
	}
	if verdict.Code != mrWispDenyQueueDisabled {
		t.Errorf("code = %q, want %q", verdict.Code, mrWispDenyQueueDisabled)
	}
	if !strings.Contains(verdict.Reason, "capital") {
		t.Errorf("reason should name the rig, got %q", verdict.Reason)
	}
}

func TestEvaluateMRWispCreation(t *testing.T) {
	tests := []struct {
		name     string
		env      mrWispEnv
		wantDeny mrWispDenyCode // "" means allow
	}{
		{
			name: "default rig with no settings keeps its merge queue",
			env:  mrWispEnv{Rig: "openclaw", Branch: "polecat/nux_op-1"},
		},
		{
			name: "direct-strategy rig with an enabled queue submits normally",
			env: mrWispEnv{
				Rig: "openclaw", Branch: "polecat/nux_op-1",
				QueueConfigured: true, QueueEnabled: true, MergeStrategy: "direct",
			},
		},
		{
			name: "pr-strategy rig with an open PR submits with the PR recorded",
			env: mrWispEnv{
				Rig: "capital", Branch: "polecat/mica_op-2",
				QueueConfigured: true, QueueEnabled: true, MergeStrategy: "pr",
				PRNumber: 21606, PRURL: "https://github.com/acme/capital/pull/21606",
			},
		},
		{
			name: "pr-strategy rig without a PR is refused",
			env: mrWispEnv{
				Rig: "capital", Branch: "polecat/mica_op-2",
				QueueConfigured: true, QueueEnabled: true, MergeStrategy: "pr",
			},
			wantDeny: mrWispDenyNoPR,
		},
		{
			name: "PR strategy match is case-insensitive",
			env: mrWispEnv{
				Rig: "capital", Branch: "polecat/mica_op-2",
				QueueConfigured: true, QueueEnabled: true, MergeStrategy: "PR",
			},
			wantDeny: mrWispDenyNoPR,
		},
		{
			name: "--no-mr is honored by the machinery, not just the agent",
			env: mrWispEnv{
				Rig: "openclaw", Branch: "polecat/nux_op-1",
				QueueConfigured: true, QueueEnabled: true, FlagOptOut: true,
			},
			wantDeny: mrWispDenyFlag,
		},
		{
			name: "no_mr on the source bead is honored",
			env: mrWispEnv{
				Rig: "openclaw", Branch: "polecat/nux_op-1",
				QueueConfigured: true, QueueEnabled: true, BeadOptOut: true,
			},
			wantDeny: mrWispDenyBead,
		},
		{
			name: "pr-strategy rig whose PR lookup failed is refused, and says so",
			env: mrWispEnv{
				Rig: "capital", Branch: "polecat/mica_op-2",
				QueueConfigured: true, QueueEnabled: true, MergeStrategy: "pr",
				PRLookupFailed: true,
			},
			wantDeny: mrWispDenyNoPR,
		},
		{
			name: "flag opt-out wins over everything else",
			env: mrWispEnv{
				Rig: "capital", Branch: "polecat/mica_op-2",
				QueueConfigured: true, QueueEnabled: false, MergeStrategy: "pr",
				FlagOptOut: true, PRNumber: 5,
			},
			wantDeny: mrWispDenyFlag,
		},
		{
			name: "disabled queue is refused even when a PR exists",
			env: mrWispEnv{
				Rig: "capital", Branch: "polecat/mica_op-2",
				QueueConfigured: true, QueueEnabled: false, MergeStrategy: "pr",
				PRNumber: 21606,
			},
			wantDeny: mrWispDenyQueueDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := evaluateMRWispCreation(tt.env)
			if tt.wantDeny == "" {
				if !verdict.Allow {
					t.Fatalf("expected allow, got deny %q: %s", verdict.Code, verdict.Reason)
				}
				return
			}
			if verdict.Allow {
				t.Fatalf("expected deny %q, got allow", tt.wantDeny)
			}
			if verdict.Code != tt.wantDeny {
				t.Errorf("code = %q, want %q", verdict.Code, tt.wantDeny)
			}
			if strings.TrimSpace(verdict.Reason) == "" {
				t.Error("deny verdict must explain itself")
			}
			if tt.env.PRLookupFailed && !strings.Contains(verdict.Reason, "lookup failed") {
				t.Errorf("unverifiable PR state must be worded as such, got %q", verdict.Reason)
			}
		})
	}
}

func TestResolveRigMergeQueue(t *testing.T) {
	townRoot := t.TempDir()

	// Rig with no settings file at all: the guard must stay permissive.
	if _, configured := resolveRigMergeQueue(townRoot, "openclaw"); configured {
		t.Error("missing settings must report unconfigured")
	}

	// Capital-shaped settings.
	settingsDir := filepath.Join(townRoot, "capital", "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"type":    "rig-settings",
		"version": 1,
		"merge_queue": map[string]any{
			"enabled":        false,
			"merge_strategy": "pr",
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(filepath.Join(settingsDir, "config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	mq, configured := resolveRigMergeQueue(townRoot, "capital")
	if !configured {
		t.Fatal("capital settings should be read")
	}
	if mq.Enabled {
		t.Error("merge_queue.enabled should be false")
	}
	if mq.MergeStrategy != "pr" {
		t.Errorf("merge_strategy = %q, want pr", mq.MergeStrategy)
	}
}

// TestResolveMRWispEnv_BeadOptOut checks that the machine-readable opt-out on
// the source bead reaches the decision — the missing link in the incident,
// where "no MR wisp" existed only as prose in the dispatch.
func TestResolveMRWispEnv_BeadOptOut(t *testing.T) {
	townRoot := t.TempDir()
	issue := &beads.Issue{ID: "op-krtw", Description: "no_mr: true\ndispatched_by: mayor"}

	env, err := resolveMRWispEnv(townRoot, "openclaw", "polecat/nux_op-krtw", nil, issue, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !env.BeadOptOut {
		t.Fatal("no_mr: true on the source bead must set BeadOptOut")
	}
	if verdict := evaluateMRWispCreation(env); verdict.Allow {
		t.Fatal("bead opt-out must refuse MR-wisp creation")
	}
}

func TestMRDeliveryProvenance(t *testing.T) {
	got := mrDeliveryProvenance(mrWispEnv{
		MergeStrategy: "pr",
		PRNumber:      21606,
		PRURL:         "https://github.com/acme/capital/pull/21606",
	})
	for _, want := range []string{"\nmerge_strategy: pr", "\npr: 21606", "\npr_url: https://github.com/acme/capital/pull/21606"} {
		if !strings.Contains(got, want) {
			t.Errorf("provenance missing %q, got: %q", want, got)
		}
	}

	// A direct rig stamps only what it knows; no empty keys.
	if got := mrDeliveryProvenance(mrWispEnv{MergeStrategy: "direct"}); got != "\nmerge_strategy: direct" {
		t.Errorf("direct provenance = %q", got)
	}
	if got := mrDeliveryProvenance(mrWispEnv{}); got != "" {
		t.Errorf("empty env should stamp nothing, got %q", got)
	}
}

// TestShouldDeferSourceCloseOnDone covers op-uhd2 defect #6: submitting behind
// a PR is not landing, so gt done must leave the source bead for the refinery
// to close at merge.
func TestShouldDeferSourceCloseOnDone(t *testing.T) {
	tests := []struct {
		name      string
		exitType  string
		strategy  string
		mrCreated bool
		want      bool
	}{
		{"pr strategy with MR created defers the close", ExitCompleted, "pr", true, true},
		{"direct strategy still closes at done", ExitCompleted, "direct", true, false},
		{"unknown strategy still closes at done", ExitCompleted, "", true, false},
		{"no MR created means nothing to wait for", ExitCompleted, "pr", false, false},
		{"deferred exit is unaffected", ExitDeferred, "pr", true, false},
		{"escalated exit is unaffected", ExitEscalated, "pr", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldDeferSourceCloseOnDone(tt.exitType, tt.strategy, tt.mrCreated); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMRWispSkipNote(t *testing.T) {
	note := mrWispSkipNote("polecat/mica_op-abc", string(mrWispDenyQueueDisabled), "rig capital runs no merge queue",
		mrWispEnv{PRNumber: 21606, PRURL: "https://github.com/acme/capital/pull/21606"})
	for _, want := range []string{"queue-disabled", "rig capital runs no merge queue", "polecat/mica_op-abc", "pull/21606"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q:\n%s", want, note)
		}
	}
}
