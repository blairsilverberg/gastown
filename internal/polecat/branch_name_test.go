package polecat

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestFormatGeneratedBranchName_ActionCompatible(t *testing.T) {
	branch := FormatGeneratedBranchName("alpha", "gt-pin-bd-metadata", "mk123456")
	if strings.Contains(branch, "@") {
		t.Fatalf("FormatGeneratedBranchName() = %q, must not contain @", branch)
	}

	claudeCodeActionBranch := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_.#+,-]*$`)
	if !claudeCodeActionBranch.MatchString(branch) {
		t.Fatalf("FormatGeneratedBranchName() = %q, rejected by claude-code-action branch pattern", branch)
	}
	if err := exec.Command("git", "check-ref-format", "--branch", branch).Run(); err != nil {
		t.Fatalf("FormatGeneratedBranchName() = %q, rejected by git check-ref-format: %v", branch, err)
	}
}

func TestParseBranchName(t *testing.T) {
	tests := []struct {
		name          string
		branch        string
		wantOk        bool
		wantGenerated bool
		wantPolecat   string
		wantIssue     string
	}{
		{
			name:          "generated issue with plus suffix",
			branch:        "polecat/alpha/gt-pin-bd-metadata+mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-pin-bd-metadata",
		},
		{
			name:          "generated dotted subtask with plus suffix",
			branch:        "polecat/alpha/gt-4kp9.5.5.1+mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-4kp9.5.5.1",
		},
		{
			name:          "legacy generated issue with at suffix",
			branch:        "polecat/alpha/gt-jns7.1@mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-jns7.1",
		},
		{
			name:        "raw dashed issue slug is not truncated",
			branch:      "polecat/alpha/gt-pin-bd-metadata",
			wantOk:      true,
			wantPolecat: "alpha",
			wantIssue:   "gt-pin-bd-metadata",
		},
		{
			name:          "no issue generated branch",
			branch:        "polecat/alpha-mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
		},
		{
			name:          "generated issue with underscore suffix",
			branch:        "polecat/alpha/cap-5gw_mrb05j24",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "cap-5gw",
		},
		{
			name:          "generated dashed issue with underscore suffix",
			branch:        "polecat/alpha/gt-pin-bd-metadata_mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-pin-bd-metadata",
		},
		{
			name:          "generated dotted subtask with underscore suffix",
			branch:        "polecat/alpha/gt-4kp9.5.5.1_mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-4kp9.5.5.1",
		},
		{
			name:          "encoded nested subtask with underscore suffix",
			branch:        "polecat/alpha/gt-4kp9_5_5_1_mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-4kp9.5.5.1",
		},
		{
			name:          "encoded subtask with underscore suffix",
			branch:        "polecat/alpha/gt-4kp9_5_mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-4kp9.5",
		},
		{
			name:          "encoded subtask with legacy plus suffix",
			branch:        "polecat/alpha/gt-4kp9_5+mk123456",
			wantOk:        true,
			wantGenerated: true,
			wantPolecat:   "alpha",
			wantIssue:     "gt-4kp9.5",
		},
		{
			name:   "empty generated suffix is invalid",
			branch: "polecat/alpha/gt-abc+",
		},
		{
			name:   "empty underscore suffix is invalid",
			branch: "polecat/alpha/gt-abc_",
		},
		{
			name:   "empty generated issue is invalid",
			branch: "polecat/alpha/+mk123456",
		},
		{
			name:   "empty underscore issue is invalid",
			branch: "polecat/alpha/_mk123456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseBranchName(tt.branch)
			if ok != tt.wantOk {
				t.Fatalf("ParseBranchName(%q) ok = %v, want %v", tt.branch, ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if got.Generated != tt.wantGenerated {
				t.Errorf("Generated = %v, want %v", got.Generated, tt.wantGenerated)
			}
			if got.Polecat != tt.wantPolecat {
				t.Errorf("Polecat = %q, want %q", got.Polecat, tt.wantPolecat)
			}
			if got.Issue != tt.wantIssue {
				t.Errorf("Issue = %q, want %q", got.Issue, tt.wantIssue)
			}
		})
	}
}

func TestFormatGeneratedBranchNameWithDelimiter(t *testing.T) {
	tests := []struct {
		name      string
		issue     string
		delimiter string
		want      string
	}{
		{name: "underscore delimiter", issue: "cap-5gw", delimiter: "_", want: "polecat/alpha/cap-5gw_mk123456"},
		{name: "plus delimiter", issue: "cap-5gw", delimiter: "+", want: "polecat/alpha/cap-5gw+mk123456"},
		{name: "invalid delimiter falls back to underscore", issue: "cap-5gw", delimiter: "!", want: "polecat/alpha/cap-5gw_mk123456"},
		{name: "empty delimiter falls back to underscore", issue: "cap-5gw", delimiter: "", want: "polecat/alpha/cap-5gw_mk123456"},
		{name: "multi-char delimiter falls back to underscore", issue: "cap-5gw", delimiter: "__", want: "polecat/alpha/cap-5gw_mk123456"},
		{name: "no issue ignores delimiter", issue: "", delimiter: "_", want: "polecat/alpha-mk123456"},
		{name: "subtask dot encoded with underscore delimiter", issue: "gt-4kp9.5", delimiter: "_", want: "polecat/alpha/gt-4kp9_5_mk123456"},
		{name: "subtask dot encoded with plus delimiter", issue: "gt-4kp9.5", delimiter: "+", want: "polecat/alpha/gt-4kp9_5+mk123456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatGeneratedBranchNameWithDelimiter("alpha", tt.issue, "mk123456", tt.delimiter)
			if got != tt.want {
				t.Errorf("FormatGeneratedBranchNameWithDelimiter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultBranchNameIsDockerComposeSafe(t *testing.T) {
	// op-jt4a: pipelines derive docker-compose project names from the branch
	// by mapping "/" to "-"; the result must match [a-z0-9][a-z0-9_-]*. The
	// old "+" default broke `hum stack up` with
	// "invalid project name ...cap-b9k+mrz49cs3-1-common".
	composeProjectName := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	for _, tc := range []struct{ polecat, issue, suffix string }{
		{"cheedo", "op-jt4a", "mrz49cs3"},
		{"alpha", "cap-b9k", "mk123456"},
		{"alpha", "", "mk123456"},
	} {
		branch := FormatGeneratedBranchName(tc.polecat, tc.issue, tc.suffix)
		if strings.Contains(branch, "+") {
			t.Errorf("FormatGeneratedBranchName(%q, %q, %q) = %q, must not contain +", tc.polecat, tc.issue, tc.suffix, branch)
		}
		derived := strings.ReplaceAll(branch, "/", "-")
		if !composeProjectName.MatchString(derived) {
			t.Errorf("branch %q → compose project %q does not match %s", branch, derived, composeProjectName)
		}
	}
}

func TestDefaultBranchNameRoundTrip(t *testing.T) {
	// The default delimiter must round-trip through ParseGeneratedBranchName
	// so gt done / mq submit derive the right bead from the branch (op-jt4a).
	branch := FormatGeneratedBranchName("cheedo", "op-jt4a", "mrz49cs3")
	if branch != "polecat/cheedo/op-jt4a_mrz49cs3" {
		t.Fatalf("format = %q, want polecat/cheedo/op-jt4a_mrz49cs3", branch)
	}
	meta, ok := ParseGeneratedBranchName(branch)
	if !ok {
		t.Fatalf("ParseGeneratedBranchName(%q) not ok", branch)
	}
	if meta.Polecat != "cheedo" || meta.Issue != "op-jt4a" {
		t.Fatalf("round-trip = %+v, want polecat cheedo issue op-jt4a", meta)
	}
	if err := exec.Command("git", "check-ref-format", "--branch", branch).Run(); err != nil {
		t.Fatalf("branch %q rejected by git check-ref-format: %v", branch, err)
	}
}

func TestLegacyPlusBranchStillParses(t *testing.T) {
	// In-flight branches created under the old "+" default must keep
	// resolving to their issue after the default changed to "_" (op-jt4a).
	meta, ok := ParseGeneratedBranchName("polecat/cheedo/cap-b9k+mrz49cs3")
	if !ok {
		t.Fatal("ParseGeneratedBranchName rejected legacy + branch")
	}
	if meta.Polecat != "cheedo" || meta.Issue != "cap-b9k" {
		t.Fatalf("legacy + parse = %+v, want polecat cheedo issue cap-b9k", meta)
	}
}

func TestUnderscoreBranchRoundTrip(t *testing.T) {
	// The docker-compose-safe delimiter must round-trip: format with "_" and
	// parse back to the same issue (AA-849).
	branch := FormatGeneratedBranchNameWithDelimiter("nux", "cap-5gw", "mrb05j24", "_")
	if branch != "polecat/nux/cap-5gw_mrb05j24" {
		t.Fatalf("format = %q, want polecat/nux/cap-5gw_mrb05j24", branch)
	}
	meta, ok := ParseGeneratedBranchName(branch)
	if !ok {
		t.Fatalf("ParseGeneratedBranchName(%q) not ok", branch)
	}
	if meta.Polecat != "nux" || meta.Issue != "cap-5gw" {
		t.Fatalf("round-trip = %+v, want polecat nux issue cap-5gw", meta)
	}
	if err := exec.Command("git", "check-ref-format", "--branch", branch).Run(); err != nil {
		t.Fatalf("branch %q rejected by git check-ref-format: %v", branch, err)
	}
}

func TestValidBranchDelimiter(t *testing.T) {
	for _, valid := range []string{"+", "@", "_"} {
		if !ValidBranchDelimiter(valid) {
			t.Errorf("ValidBranchDelimiter(%q) = false, want true", valid)
		}
	}
	for _, invalid := range []string{"", "-", ".", "/", "!", "__", "+@", "a"} {
		if ValidBranchDelimiter(invalid) {
			t.Errorf("ValidBranchDelimiter(%q) = true, want false", invalid)
		}
	}
}

func TestSubtaskBranchNameIsDockerComposeSafe(t *testing.T) {
	// op-42p9: subtask bead IDs embed "." (gt-4kp9.5.5.1), which is outside
	// the docker-compose project-name charset even after the "+" delimiter
	// fix (op-jt4a). Generated branches must encode the dots.
	composeProjectName := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	for _, tc := range []struct{ polecat, issue, suffix string }{
		{"valkyrie", "gt-4kp9.5.5.1", "mrz8vky5"},
		{"alpha", "op-abc.12", "mk123456"},
	} {
		branch := FormatGeneratedBranchName(tc.polecat, tc.issue, tc.suffix)
		derived := strings.ReplaceAll(branch, "/", "-")
		if !composeProjectName.MatchString(derived) {
			t.Errorf("branch %q → compose project %q does not match %s", branch, derived, composeProjectName)
		}
	}
}

func TestSubtaskBranchNameRoundTrip(t *testing.T) {
	// The "." → "_" encoding must round-trip exactly for every delimiter so
	// gt done / mq submit derive the right subtask bead from the branch
	// (op-42p9).
	for _, delimiter := range []string{"_", "+", "@"} {
		branch := FormatGeneratedBranchNameWithDelimiter("valkyrie", "gt-4kp9.5.5.1", "mrz8vky5", delimiter)
		if strings.Contains(branch, ".") {
			t.Fatalf("format(delimiter %q) = %q, must not embed raw subtask dots", delimiter, branch)
		}
		meta, ok := ParseGeneratedBranchName(branch)
		if !ok {
			t.Fatalf("ParseGeneratedBranchName(%q) not ok", branch)
		}
		if meta.Polecat != "valkyrie" || meta.Issue != "gt-4kp9.5.5.1" {
			t.Fatalf("round-trip via %q = %+v, want polecat valkyrie issue gt-4kp9.5.5.1", branch, meta)
		}
	}
	branch := FormatGeneratedBranchName("valkyrie", "gt-4kp9.5.5.1", "mrz8vky5")
	if branch != "polecat/valkyrie/gt-4kp9_5_5_1_mrz8vky5" {
		t.Fatalf("format = %q, want polecat/valkyrie/gt-4kp9_5_5_1_mrz8vky5", branch)
	}
	if err := exec.Command("git", "check-ref-format", "--branch", branch).Run(); err != nil {
		t.Fatalf("branch %q rejected by git check-ref-format: %v", branch, err)
	}
}

func TestInFlightRawDotSubtaskBranchStillParses(t *testing.T) {
	// Branches created before the encoding (op-jt4a era and older) embed raw
	// subtask dots. They must keep resolving to their issue: "." passes
	// through decoding untouched.
	for branch, wantIssue := range map[string]string{
		"polecat/alpha/gt-4kp9.5_mk123456":     "gt-4kp9.5",
		"polecat/alpha/gt-4kp9.5.5.1+mk123456": "gt-4kp9.5.5.1",
		"polecat/alpha/gt-jns7.1@mk123456":     "gt-jns7.1",
	} {
		meta, ok := ParseGeneratedBranchName(branch)
		if !ok {
			t.Errorf("ParseGeneratedBranchName(%q) not ok", branch)
			continue
		}
		if meta.Issue != wantIssue {
			t.Errorf("ParseGeneratedBranchName(%q).Issue = %q, want %q", branch, meta.Issue, wantIssue)
		}
	}
}

func TestParseGeneratedBranchNameRejectsRawDashedIssues(t *testing.T) {
	rejects := []string{
		"polecat/alpha/gt-pin-bd-metadata",
		"polecat/alpha/gt-jns7.1-mk123456",
	}
	for _, branch := range rejects {
		if meta, ok := ParseGeneratedBranchName(branch); ok {
			t.Errorf("ParseGeneratedBranchName(%q) = %+v, want ok=false", branch, meta)
		}
	}
}
