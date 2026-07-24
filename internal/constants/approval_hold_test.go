package constants

import "testing"

// Approval-hold label predicate tests (op-ijaw). The label forms are defined
// by the op-96zr `gt approval hold` gate; the dispatch pipeline shares these
// predicates so a held bead can never be auto-dispatched to a polecat.

func TestIsApprovalHoldLabel(t *testing.T) {
	tests := []struct {
		label string
		want  bool
	}{
		{"needs-approval", true},
		{"needs-approval:openclaw/witness:1753380020", true},
		{"needs-approval:mayor:0", true},
		{"needs-approval:", true}, // malformed but unmistakably a hold
		{"needs-approvals", false},
		{"approved", false},
		{"gt:message", false},
		{"HumAssist", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsApprovalHoldLabel(tt.label); got != tt.want {
			t.Errorf("IsApprovalHoldLabel(%q) = %v, want %v", tt.label, got, tt.want)
		}
	}
}

func TestHasApprovalHold(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   bool
	}{
		{"nil labels", nil, false},
		{"empty labels", []string{}, false},
		{"bare hold", []string{"needs-approval"}, true},
		{"metadata hold among others", []string{"HumAssist", "needs-approval:openclaw/witness:1753380020"}, true},
		{"no hold", []string{"HumAssist", "bug"}, false},
	}
	for _, tt := range tests {
		if got := HasApprovalHold(tt.labels); got != tt.want {
			t.Errorf("%s: HasApprovalHold(%v) = %v, want %v", tt.name, tt.labels, got, tt.want)
		}
	}
}
