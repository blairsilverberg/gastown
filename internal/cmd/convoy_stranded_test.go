package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsReadyIssue_BlockingAndStatus(t *testing.T) {
	tests := []struct {
		name string
		in   trackedIssueInfo
		want bool
	}{
		{
			name: "closed issue never ready",
			in: trackedIssueInfo{
				Status:  "closed",
				Blocked: false,
			},
			want: false,
		},
		{
			name: "unknown issue never ready",
			in: trackedIssueInfo{
				Status:  trackedStatusUnknown,
				Blocked: false,
			},
			want: false,
		},
		{
			name: "blank status never ready",
			in: trackedIssueInfo{
				Status:  " ",
				Blocked: false,
			},
			want: false,
		},
		{
			name: "blocked open issue not ready",
			in: trackedIssueInfo{
				Status:  "open",
				Blocked: true,
			},
			want: false,
		},
		{
			name: "open unassigned issue ready",
			in: trackedIssueInfo{
				Status:  "open",
				Blocked: false,
			},
			want: true,
		},
		{
			name: "non-open unassigned issue treated ready for recovery",
			in: trackedIssueInfo{
				Status:  "in_progress",
				Blocked: false,
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isReadyIssue(tc.in, nil)
			if got != tc.want {
				t.Fatalf("isReadyIssue() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Incident-derived (op-mj0t / hq-gk229): the stranded scan classified witness
// patrol step wisps tracked by a workflow convoy (hq-wf-wnmnu) as feedable
// ready_issues, so the daemon retried a doomed `gt sling` every scan cycle
// against the op-dat2 role-owned-wisp dispatch guard.
func TestIsReadyIssue_RoleOwnedWispNeverReady(t *testing.T) {
	tests := []struct {
		name string
		in   trackedIssueInfo
		want bool
	}{
		{
			name: "witness-created patrol step wisp not ready (incident shape)",
			in: trackedIssueInfo{
				ID:        "dbt-wfs-7laya",
				Status:    "open",
				CreatedBy: "humdbt/witness",
			},
			want: false,
		},
		{
			name: "witness-assigned wisp root not ready even with dead session",
			in: trackedIssueInfo{
				ID:       "hq-wisp-abc12",
				Status:   "in_progress",
				Assignee: "openclaw/witness",
			},
			want: false,
		},
		{
			name: "deacon-created step wisp not ready",
			in: trackedIssueInfo{
				ID:        "hq-wfs-xyz99",
				Status:    "open",
				CreatedBy: "deacon",
			},
			want: false,
		},
		{
			name: "polecat-created wisp still ready (guard requires role actor)",
			in: trackedIssueInfo{
				ID:        "gt-wisp-def34",
				Status:    "open",
				CreatedBy: "gastown/polecats/nux",
			},
			want: true,
		},
		{
			name: "witness-created regular task still ready (guard requires wisp ID)",
			in: trackedIssueInfo{
				ID:        "gt-abc123",
				Status:    "open",
				CreatedBy: "gastown/witness",
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isReadyIssue(tc.in, nil)
			if got != tc.want {
				t.Fatalf("isReadyIssue(%s) = %v, want %v", tc.in.ID, got, tc.want)
			}
		})
	}
}

func TestApplyFreshIssueDetails_PropagatesCreatedBy(t *testing.T) {
	dep := trackedDependency{ID: "dbt-wfs-7laya", Status: "open"}
	details := &issueDetails{
		ID:        "dbt-wfs-7laya",
		Status:    "open",
		CreatedBy: "humdbt/witness",
	}

	applyFreshIssueDetails(&dep, details)

	if dep.CreatedBy != "humdbt/witness" {
		t.Fatalf("dep.CreatedBy = %q, want %q", dep.CreatedBy, "humdbt/witness")
	}
}

func TestApplyFreshIssueDetails_SetsBlockedFlag(t *testing.T) {
	dep := trackedDependency{
		ID:     "gt-123",
		Status: "open",
	}
	details := &issueDetails{
		ID:             "gt-123",
		Status:         "open",
		BlockedByCount: 1,
	}

	applyFreshIssueDetails(&dep, details)

	if !dep.Blocked {
		t.Fatalf("applyFreshIssueDetails() should set Blocked=true when details are blocked")
	}
}

func TestApplyFreshIssueDetails_BlankStatusBecomesUnknown(t *testing.T) {
	dep := trackedDependency{ID: "gt-123"}
	details := &issueDetails{ID: "gt-123", Status: "  "}

	applyFreshIssueDetails(&dep, details)

	if dep.Status != trackedStatusUnknown {
		t.Fatalf("dep.Status = %q, want %q", dep.Status, trackedStatusUnknown)
	}
}

func TestIssueDetailsIsBlocked(t *testing.T) {
	tests := []struct {
		name string
		in   issueDetails
		want bool
	}{
		{
			name: "blocked_by_count marks blocked",
			in: issueDetails{
				BlockedByCount: 2,
			},
			want: true,
		},
		{
			name: "blocked_by list marks blocked",
			in: issueDetails{
				BlockedBy: []string{"gt-1"},
			},
			want: true,
		},
		{
			name: "open blocks dependency marks blocked",
			in: issueDetails{
				Dependencies: []issueDependency{
					{DependencyType: "blocks", Status: "open"},
				},
			},
			want: true,
		},
		{
			name: "closed blocks dependency does not mark blocked",
			in: issueDetails{
				Dependencies: []issueDependency{
					{DependencyType: "blocks", Status: "closed"},
				},
			},
			want: false,
		},
		{
			name: "non-blocking dependency does not mark blocked",
			in: issueDetails{
				Dependencies: []issueDependency{
					{DependencyType: "parent-child", Status: "open"},
				},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.IsBlocked()
			if got != tc.want {
				t.Fatalf("IsBlocked() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsSlingableBead(t *testing.T) {
	// Set up a fake town root with routes.jsonl
	townRoot := t.TempDir()
	beadsDir := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	routesContent := `{"prefix": "gt-", "path": "gastown/mayor/rig"}
{"prefix": "bd-", "path": "beads/mayor/rig"}
{"prefix": "hq-", "path": "."}
`
	if err := os.WriteFile(filepath.Join(beadsDir, "routes.jsonl"), []byte(routesContent), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		beadID string
		want   bool
	}{
		{"rig bead is slingable", "gt-wisp-abc", true},
		{"another rig bead is slingable", "bd-wisp-xyz", true},
		{"town-level bead not slingable", "hq-wisp-abc", false},
		{"town-level convoy not slingable", "hq-cv-kl6ns", false},
		{"unknown prefix not slingable", "zz-wisp-abc", false},
		{"no prefix assumes slingable", "nohyphen", true},
		{"empty ID assumes slingable", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isSlingableBead(townRoot, tc.beadID)
			if got != tc.want {
				t.Fatalf("isSlingableBead(%q) = %v, want %v", tc.beadID, got, tc.want)
			}
		})
	}
}
