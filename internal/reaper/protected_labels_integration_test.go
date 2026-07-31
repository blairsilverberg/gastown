//go:build integration

// Behavioural proof for dbt-kk9. The unit tests in protected_labels_test.go
// assert the SQL contains the exclusion; these assert the reaper actually leaves
// the row alone. A query test cannot distinguish "the predicate is present" from
// "the predicate works", and the whole finding is about a guard that reads right
// and destroys anyway.
//
// Every test here is a TWO-VERDICT run: each asserts something IS destroyed in
// the same pass in which something else survives. A guard that cannot say SAFE
// is not a guard, and a probe that only ever sees survivors cannot tell a
// working exemption from a reaper that did not run.
//
// Run against the town's Dolt server (creates and drops its own testdb_ scratch
// database, which DiscoverDatabases filters out of every patrol):
//
//	GT_TEST_DOLT_ADDR=127.0.0.1:3307 go test -tags=integration ./internal/reaper -run ProtectedLabels -v
//
// With no GT_TEST_DOLT_ADDR, falls back to the package's containerised Dolt.

package reaper

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/testutil"
)

// mailFixture is one bead the reaper will consider.
type mailFixture struct {
	id          string
	labels      []string
	status      string
	closedAgo   time.Duration // age of closed_at; 0 means NULL
	updatedAgo  time.Duration
	priority    int
	wantSurvive bool   // for purge tests: is the ROW still there afterwards
	wantStatus  string // for auto-close tests: status afterwards
	why         string
}

func doltAddr(t *testing.T) string {
	t.Helper()
	if addr := os.Getenv("GT_TEST_DOLT_ADDR"); addr != "" {
		return addr
	}
	testutil.RequireDoltContainer(t)
	return testutil.DoltContainerAddr()
}

// newScratchDB creates an isolated database with the minimum beads schema the
// reaper touches, and drops it on cleanup. The testdb_ prefix is the repo's
// established marker for test pollution (see testPollutionPrefixes) and is
// filtered out of DiscoverDatabases, so a live daemon will not reap it even if
// this test is interrupted before cleanup.
func newScratchDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	addr := doltAddr(t)

	root, err := sql.Open("mysql", fmt.Sprintf("root@tcp(%s)/?parseTime=true&timeout=10s", addr))
	if err != nil {
		t.Fatalf("open dolt at %s: %v", addr, err)
	}
	if err := root.Ping(); err != nil {
		root.Close()
		t.Skipf("no Dolt server reachable at %s: %v", addr, err)
	}

	dbName := fmt.Sprintf("testdb_reaper_kk9_%d", os.Getpid())
	if _, err := root.Exec("DROP DATABASE IF EXISTS " + dbName); err != nil {
		t.Fatalf("drop stale scratch db: %v", err)
	}
	if _, err := root.Exec("CREATE DATABASE " + dbName); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	root.Close()

	t.Cleanup(func() {
		cleanup, err := sql.Open("mysql", fmt.Sprintf("root@tcp(%s)/?parseTime=true&timeout=10s", addr))
		if err != nil {
			t.Logf("cleanup: reopen: %v", err)
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.Exec("DROP DATABASE IF EXISTS " + dbName); err != nil {
			t.Logf("cleanup: drop %s: %v", dbName, err)
		}
	})

	db, err := sql.Open("mysql", fmt.Sprintf("root@tcp(%s)/%s?parseTime=true&timeout=10s", addr, dbName))
	if err != nil {
		t.Fatalf("open scratch db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := []string{
		`CREATE TABLE issues (
			id varchar(255) NOT NULL PRIMARY KEY,
			title varchar(500) NOT NULL DEFAULT '',
			status varchar(32) NOT NULL DEFAULT 'open',
			priority int NOT NULL DEFAULT 2,
			issue_type varchar(32) NOT NULL DEFAULT 'task',
			created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
			closed_at datetime NULL,
			close_reason longtext NULL,
			ephemeral tinyint(1) NULL DEFAULT 0
		)`,
		`CREATE TABLE labels (
			issue_id varchar(255) NOT NULL,
			label varchar(255) NOT NULL,
			PRIMARY KEY (issue_id, label)
		)`,
		`CREATE TABLE comments (issue_id varchar(255) NOT NULL, body longtext, PRIMARY KEY (issue_id))`,
		`CREATE TABLE events (issue_id varchar(255) NOT NULL, kind varchar(64), PRIMARY KEY (issue_id, kind))`,
		`CREATE TABLE dependencies (
			id char(36) NOT NULL PRIMARY KEY,
			issue_id varchar(255) NOT NULL,
			depends_on_issue_id varchar(255) NULL,
			depends_on_wisp_id varchar(255) NULL,
			depends_on_external varchar(255) NULL,
			type varchar(32) NOT NULL DEFAULT 'blocks'
		)`,
		// purgeClosedWisps runs first inside Purge and needs the table to exist.
		`CREATE TABLE wisps (
			id varchar(255) NOT NULL PRIMARY KEY,
			status varchar(32) NOT NULL DEFAULT 'open',
			wisp_type varchar(32) NULL,
			closed_at datetime NULL,
			created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, ddl := range schema {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("schema: %v\n%s", err, ddl)
		}
	}
	return db, dbName
}

func insertFixtures(t *testing.T, db *sql.DB, fixtures []mailFixture) {
	t.Helper()
	now := time.Now().UTC()
	for _, f := range fixtures {
		var closedAt interface{}
		if f.closedAgo > 0 {
			closedAt = now.Add(-f.closedAgo)
		}
		if _, err := db.Exec(
			`INSERT INTO issues (id, title, status, priority, issue_type, created_at, updated_at, closed_at, ephemeral)
			 VALUES (?, ?, ?, ?, 'task', ?, ?, ?, 0)`,
			f.id, f.why, f.status, f.priority,
			now.Add(-30*24*time.Hour), now.Add(-f.updatedAgo), closedAt,
		); err != nil {
			t.Fatalf("insert %s: %v", f.id, err)
		}
		for _, l := range f.labels {
			if _, err := db.Exec(`INSERT INTO labels (issue_id, label) VALUES (?, ?)`, f.id, l); err != nil {
				t.Fatalf("label %s=%s: %v", f.id, l, err)
			}
		}
	}
}

func rowExists(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM issues WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("existence probe for %s: %v", id, err)
	}
	return n > 0
}

func statusOf(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM issues WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatalf("status probe for %s: %v", id, err)
	}
	return s
}

// TestProtectedLabelsSurviveMailPurge is the deletion half. All five beads are
// permanent mail (ephemeral=0) — which is what every gt:message row in the real
// issues table is — so this cannot be passed by an ephemeral test.
func TestProtectedLabelsSurviveMailPurge(t *testing.T) {
	db, dbName := newScratchDB(t)

	const past = 8 * 24 * time.Hour // past the 7d mail delete age
	const fresh = 1 * time.Hour

	fixtures := []mailFixture{
		{id: "m-plain", labels: []string{"gt:message"}, status: "closed", closedAgo: past, updatedAgo: past,
			priority: 2, wantSurvive: false, why: "unprotected closed mail past the cutoff — MUST be deleted"},
		{id: "m-keep", labels: []string{"gt:message", "gt:keep"}, status: "closed", closedAgo: past, updatedAgo: past,
			priority: 2, wantSurvive: true, why: "gt:keep — MUST survive"},
		{id: "m-standing", labels: []string{"gt:message", "gt:standing-orders"}, status: "closed", closedAgo: past, updatedAgo: past,
			priority: 1, wantSurvive: true, why: "gt:standing-orders — MUST survive"},
		{id: "m-fresh", labels: []string{"gt:message"}, status: "closed", closedAgo: fresh, updatedAgo: fresh,
			priority: 2, wantSurvive: true, why: "inside the window — survives on age, not protection"},
		{id: "m-open", labels: []string{"gt:message"}, status: "open", closedAgo: 0, updatedAgo: past,
			priority: 2, wantSurvive: true, why: "not closed — purge does not consider it"},
	}
	insertFixtures(t, db, fixtures)

	result, err := Purge(db, dbName, 7*24*time.Hour, 7*24*time.Hour, false)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}

	// The destructive arm must have fired. Without this the survivor assertions
	// below are satisfied by a reaper that did nothing at all.
	if result.MailPurged != 1 {
		t.Fatalf("MailPurged = %d, want exactly 1 (the unprotected bead). "+
			"0 means the purge did not run and every survival assertion below is vacuous.", result.MailPurged)
	}

	for _, f := range fixtures {
		got := rowExists(t, db, f.id)
		if got != f.wantSurvive {
			verb := map[bool]string{true: "survived", false: "was DELETED"}
			t.Errorf("%s (%s): %s, want %s",
				f.id, f.why, verb[got], verb[f.wantSurvive])
		}
	}

	// Aux rows for the deleted bead must be gone too; aux rows for the protected
	// one must not have been collaterally cleaned.
	var plainLabels, keepLabels int
	if err := db.QueryRow(`SELECT COUNT(*) FROM labels WHERE issue_id='m-plain'`).Scan(&plainLabels); err != nil {
		t.Fatalf("plain label probe: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM labels WHERE issue_id='m-keep'`).Scan(&keepLabels); err != nil {
		t.Fatalf("keep label probe: %v", err)
	}
	if plainLabels != 0 {
		t.Errorf("m-plain left %d label rows behind after delete, want 0", plainLabels)
	}
	if keepLabels != 2 {
		t.Errorf("m-keep has %d label rows, want 2 — its aux rows were collaterally deleted", keepLabels)
	}
}

// TestProtectedLabelsSurviveAutoClose is the closure half — stage 1 of the
// two-stage lifespan. Auto-closing a mail bead is not itself destructive, but it
// starts the purge clock, so an exemption honoured by only one stage delays
// deletion rather than preventing it.
func TestProtectedLabelsSurviveAutoClose(t *testing.T) {
	db, dbName := newScratchDB(t)

	const stale = 8 * 24 * time.Hour
	const recent = 1 * time.Hour

	fixtures := []mailFixture{
		{id: "a-plain", labels: []string{"gt:message"}, status: "open", updatedAgo: stale,
			priority: 2, wantStatus: "closed", why: "unprotected stale P2 mail — MUST be auto-closed"},
		{id: "a-keep", labels: []string{"gt:message", "gt:keep"}, status: "open", updatedAgo: stale,
			priority: 2, wantStatus: "open", why: "gt:keep — MUST stay open"},
		{id: "a-p1", labels: []string{"gt:message"}, status: "open", updatedAgo: stale,
			priority: 1, wantStatus: "open", why: "P1 fails priority > 1 — stays open without any label"},
		{id: "a-recent", labels: []string{"gt:message"}, status: "open", updatedAgo: recent,
			priority: 2, wantStatus: "open", why: "not stale — stays open on age, not protection"},
	}
	insertFixtures(t, db, fixtures)

	result, err := AutoClose(db, dbName, 7*24*time.Hour, false)
	if err != nil {
		t.Fatalf("AutoClose: %v", err)
	}
	if result.Closed != 1 {
		t.Fatalf("Closed = %d, want exactly 1 (the unprotected bead). "+
			"0 means AutoClose did not fire and the survival assertions are vacuous.", result.Closed)
	}

	for _, f := range fixtures {
		if got := statusOf(t, db, f.id); got != f.wantStatus {
			t.Errorf("%s (%s): status %q, want %q", f.id, f.why, got, f.wantStatus)
		}
	}
}

// TestMailPurgeDryRunCountMatchesDelete guards the half of the fix that is easy
// to forget: purgeOldMail short-circuits on its COUNT query, so an exclusion
// added to the delete but not the count (or vice versa) produces a reaper whose
// reported candidate set and actual victim set disagree.
func TestMailPurgeDryRunCountMatchesDelete(t *testing.T) {
	db, dbName := newScratchDB(t)

	const past = 8 * 24 * time.Hour
	insertFixtures(t, db, []mailFixture{
		{id: "d-plain1", labels: []string{"gt:message"}, status: "closed", closedAgo: past, updatedAgo: past, priority: 2},
		{id: "d-plain2", labels: []string{"gt:message"}, status: "closed", closedAgo: past, updatedAgo: past, priority: 2},
		{id: "d-keep", labels: []string{"gt:message", "gt:keep"}, status: "closed", closedAgo: past, updatedAgo: past, priority: 2},
	})

	dry, err := Purge(db, dbName, 7*24*time.Hour, 7*24*time.Hour, true)
	if err != nil {
		t.Fatalf("Purge dry-run: %v", err)
	}
	if dry.MailPurged != 2 {
		t.Errorf("dry-run count = %d, want 2 (the keep-labelled bead must not be counted)", dry.MailPurged)
	}
	if !rowExists(t, db, "d-plain1") {
		t.Error("dry run deleted d-plain1 — dry run must not mutate")
	}

	wet, err := Purge(db, dbName, 7*24*time.Hour, 7*24*time.Hour, false)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if wet.MailPurged != dry.MailPurged {
		t.Errorf("dry run counted %d but delete removed %d — count and delete predicates disagree",
			dry.MailPurged, wet.MailPurged)
	}
	if !rowExists(t, db, "d-keep") {
		t.Error("d-keep was deleted")
	}
	if rowExists(t, db, "d-plain1") || rowExists(t, db, "d-plain2") {
		t.Error("unprotected beads survived — the purge did not actually run")
	}
}
