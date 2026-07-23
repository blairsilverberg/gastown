package cmd

import (
	"reflect"
	"strings"
	"testing"
)

// hq-wq4be / op-rowc: gt sling --base-branch emitted TWO base_branch formula
// vars (rig-default + flag value) with undefined downstream resolution. The
// fix: the effective worktree base overrides any other base_branch value, and
// var merging keeps exactly one entry per key.

func countVarKey(vars []string, key string) int {
	n := 0
	for _, v := range vars {
		if v == key || strings.HasPrefix(v, key+"=") {
			n++
		}
	}
	return n
}

func TestMergeFormulaVars(t *testing.T) {
	tests := []struct {
		name  string
		lists [][]string
		want  []string
	}{
		{
			name:  "later list overrides earlier list, first-appearance order kept",
			lists: [][]string{{"base_branch=master", "test_command=pytest"}, {"base_branch=release/v2"}},
			want:  []string{"base_branch=release/v2", "test_command=pytest"},
		},
		{
			name:  "identical duplicate collapses to one entry",
			lists: [][]string{{"base_branch=master"}, {"base_branch=master"}},
			want:  []string{"base_branch=master"},
		},
		{
			name:  "later entry wins within a single list",
			lists: [][]string{{"k=1", "other=x", "k=2"}},
			want:  []string{"k=2", "other=x"},
		},
		{
			name:  "empty value overrides non-empty",
			lists: [][]string{{"test_command=pytest"}, {"test_command="}},
			want:  []string{"test_command="},
		},
		{
			name:  "entry without equals passes through once",
			lists: [][]string{{"oddball", "k=1"}, {"oddball"}},
			want:  []string{"oddball", "k=1"},
		},
		{
			name:  "nil and empty lists",
			lists: [][]string{nil, {}, {"k=1"}},
			want:  []string{"k=1"},
		},
		{
			name:  "all empty yields empty",
			lists: [][]string{nil, {}},
			want:  []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeFormulaVars(tt.lists...)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeFormulaVars(%v) = %v, want %v", tt.lists, got, tt.want)
			}
		})
	}
}

func TestApplyBaseBranchVar(t *testing.T) {
	tests := []struct {
		name          string
		vars          []string
		effectiveBase string
		want          []string
	}{
		{
			name:          "replaces existing base_branch instead of duplicating",
			vars:          []string{"base_branch=master", "feature=x"},
			effectiveBase: "polecat/pearl/cap-2ps_mrogjm54",
			want:          []string{"base_branch=polecat/pearl/cap-2ps_mrogjm54", "feature=x"},
		},
		{
			name:          "appends when absent",
			vars:          []string{"feature=x"},
			effectiveBase: "master",
			want:          []string{"feature=x", "base_branch=master"},
		},
		{
			name:          "main is skipped (formula default handles it)",
			vars:          []string{"feature=x"},
			effectiveBase: "main",
			want:          []string{"feature=x"},
		},
		{
			name:          "empty base leaves vars untouched",
			vars:          []string{"base_branch=master"},
			effectiveBase: "",
			want:          []string{"base_branch=master"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyBaseBranchVar(tt.vars, tt.effectiveBase)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("applyBaseBranchVar(%v, %q) = %v, want %v", tt.vars, tt.effectiveBase, got, tt.want)
			}
		})
	}
}

// TestSlingBaseBranchVar_FlagAbsent reproduces the executeSling / runSling var
// assembly when NO --base-branch flag is given on a rig whose default branch is
// "master": the rig command vars carry base_branch=master AND the spawned
// worktree's effective base is master. Pre-fix this produced base_branch=master
// twice (exactly what op-rowc's own attached_vars showed).
func TestSlingBaseBranchVar_FlagAbsent(t *testing.T) {
	rigCmdVars := []string{"base_branch=master", "test_command=go test ./..."}
	userVars := []string{"feature=some title", "issue=op-rowc"}
	effectiveBase := "master" // spawn fell back to the rig default

	got := applyBaseBranchVar(mergeFormulaVars(rigCmdVars, userVars), effectiveBase)

	if n := countVarKey(got, "base_branch"); n != 1 {
		t.Fatalf("want exactly 1 base_branch var, got %d in %v", n, got)
	}
	for _, v := range got {
		if strings.HasPrefix(v, "base_branch=") && v != "base_branch=master" {
			t.Fatalf("base_branch resolved to %q, want base_branch=master (in %v)", v, got)
		}
	}
	for _, keep := range []string{"feature=some title", "issue=op-rowc", "test_command=go test ./..."} {
		if countVarKey(got, strings.SplitN(keep, "=", 2)[0]) != 1 {
			t.Errorf("var %q lost or duplicated in %v", keep, got)
		}
	}
}

// TestSlingBaseBranchVar_FlagPresent reproduces the hq-wq4be incident shape:
// --base-branch <branch> given while the rig default is master. The flag value
// must be the ONLY base_branch var — no conflicting duplicate.
func TestSlingBaseBranchVar_FlagPresent(t *testing.T) {
	rigCmdVars := []string{"base_branch=master", "test_command=go test ./..."}
	userVars := []string{"feature=some title", "issue=cap-4ez"}
	effectiveBase := "polecat/pearl/cap-2ps_mrogjm54" // --base-branch flag value

	got := applyBaseBranchVar(mergeFormulaVars(rigCmdVars, userVars), effectiveBase)

	if n := countVarKey(got, "base_branch"); n != 1 {
		t.Fatalf("want exactly 1 base_branch var, got %d in %v", n, got)
	}
	found := false
	for _, v := range got {
		if v == "base_branch="+effectiveBase {
			found = true
		} else if strings.HasPrefix(v, "base_branch=") {
			t.Fatalf("conflicting base_branch var %q survived alongside flag value (in %v)", v, got)
		}
	}
	if !found {
		t.Fatalf("flag value base_branch=%s missing from %v", effectiveBase, got)
	}
}

// TestSlingBaseBranchVar_RunSlingSequence mirrors runSling's two-step order:
// the effective base is applied to the user vars FIRST (sling.go), and the rig
// command defaults are merged in LATER (formula instantiation). The rig-default
// base_branch must not resurrect a duplicate.
func TestSlingBaseBranchVar_RunSlingSequence(t *testing.T) {
	userVars := []string{"feature=some title"}
	slingVars := applyBaseBranchVar(userVars, "release/v2") // --base-branch release/v2

	rigCmdVars := []string{"base_branch=master", "lint_command=golangci-lint run"}
	got := mergeFormulaVars(rigCmdVars, slingVars)

	if n := countVarKey(got, "base_branch"); n != 1 {
		t.Fatalf("want exactly 1 base_branch var, got %d in %v", n, got)
	}
	if countVarKey(got, "base_branch=release/v2") != 1 {
		t.Fatalf("rig default overrode the --base-branch flag value: %v", got)
	}
	if countVarKey(got, "lint_command") != 1 {
		t.Errorf("rig default lint_command lost: %v", got)
	}
}
