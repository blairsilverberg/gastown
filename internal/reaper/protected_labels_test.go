package reaper

import (
	"strings"
	"testing"
)

// TestProtectedLabelExclusionCoversBeadsProtectedSet pins the SQL-side label set
// to the Go-side one in beads.IsProtectedBead. The two are separate
// implementations of the same policy and nothing but this test stops them from
// drifting apart.
//
// The list is duplicated literally here on purpose: importing beads from this
// test would make the assertion tautological if someone edits the shared source.
func TestProtectedLabelExclusionCoversBeadsProtectedSet(t *testing.T) {
	want := []string{"gt:standing-orders", "gt:keep", "gt:role", "gt:rig"}
	if len(ProtectedLabels) != len(want) {
		t.Fatalf("ProtectedLabels = %v, want %v", ProtectedLabels, want)
	}
	for i, w := range want {
		if ProtectedLabels[i] != w {
			t.Errorf("ProtectedLabels[%d] = %q, want %q", i, ProtectedLabels[i], w)
		}
	}
}

// TestProtectedLabelExclusionQualifiesDatabase verifies both call shapes: the
// db-qualified one used by purgeOldMail/AutoClose and the unqualified one used
// by Scan, which reaches the table through the connection's own database.
func TestProtectedLabelExclusionQualifiesDatabase(t *testing.T) {
	qualified := protectedLabelExclusion("hq", "i.id")
	if !strings.Contains(qualified, "`hq`.labels") {
		t.Errorf("qualified exclusion should reference `hq`.labels, got: %s", qualified)
	}
	if !strings.HasPrefix(qualified, "i.id NOT IN (") {
		t.Errorf("qualified exclusion should start with the id expression, got: %s", qualified)
	}

	unqualified := protectedLabelExclusion("", "id")
	if strings.Contains(unqualified, "`") {
		t.Errorf("unqualified exclusion should not backtick-qualify any table, got: %s", unqualified)
	}
	if !strings.Contains(unqualified, "FROM labels pl") {
		t.Errorf("unqualified exclusion should reference bare labels, got: %s", unqualified)
	}

	for _, label := range ProtectedLabels {
		if !strings.Contains(qualified, "'"+label+"'") {
			t.Errorf("exclusion missing label %q: %s", label, qualified)
		}
	}
}

// TestPurgeOldMailQueriesExcludeProtectedLabels reproduces the two purgeOldMail
// queries exactly as the function builds them and asserts the exclusion is in
// both. The count query gates whether the delete runs at all, so an exclusion
// present in only one of them would produce a purge that reports a candidate
// count it then refuses to act on — or worse, the reverse.
//
// dbt-kk9: before the fix these queries filtered on the 'gt:message' label with
// no protection test of any kind, and gt:message is the label every mail bead
// carries.
func TestPurgeOldMailQueriesExcludeProtectedLabels(t *testing.T) {
	const dbName = "hq"

	countQuery := buildMailPurgeCountQuery(dbName)
	idQuery := buildMailPurgeIDQuery(dbName)

	for name, q := range map[string]string{"count": countQuery, "id": idQuery} {
		if !strings.Contains(q, "gt:message") {
			t.Errorf("%s query lost its gt:message predicate: %s", name, q)
		}
		if !strings.Contains(q, "'gt:keep'") {
			t.Errorf("%s query does not exclude gt:keep — protected mail would be DELETED: %s", name, q)
		}
		if !strings.Contains(q, "'gt:standing-orders'") {
			t.Errorf("%s query does not exclude gt:standing-orders: %s", name, q)
		}
	}
}

// TestAutoCloseAndPurgeShareTheSameExclusion is the anti-drift assertion that
// matters most. AutoClose (status change) and purgeOldMail (row DELETE) are two
// independent 7-day clocks over the same beads. If they disagree about what the
// retention marker means, the marker's effect depends on which clock reaches a
// bead first — which is exactly the state dbt-kk9 recorded.
func TestAutoCloseAndPurgeShareTheSameExclusion(t *testing.T) {
	const dbName = "hq"
	shared := protectedLabelExclusion(dbName, "i.id")

	autoCloseWhere := buildAutoCloseWhereClause(dbName)
	if !strings.Contains(autoCloseWhere, shared) {
		t.Errorf("AutoClose where clause does not contain the shared exclusion.\nwant substring: %s\ngot: %s", shared, autoCloseWhere)
	}

	idQuery := buildMailPurgeIDQuery(dbName)
	if !strings.Contains(idQuery, protectedLabelExclusion(dbName, "i.id")) {
		t.Errorf("purgeOldMail id query does not contain the shared exclusion: %s", idQuery)
	}
}
