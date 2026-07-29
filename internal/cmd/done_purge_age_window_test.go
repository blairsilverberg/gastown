package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
)

// TestCompletionPurgeAgeWindowBothArms proves the age window on the
// completion-step wisp purge (op-vps9) in a single run, both arms:
//
//	arm 1  a wisp closed OUTSIDE the window is still COLLECTED  (the guard has not
//	       become a no-op — the step still does its hq-6161m job)
//	arm 2  a wisp closed INSIDE the window is REFUSED            (the guard fires)
//
// A guard that cannot say SAFE is not a cautious guard, it is the absence of the
// step printing a reassuring sentence — so one arm alone proves nothing. Both are
// asserted against the same store after the same call to purgeClosedEphemeralBeads.
// The assertion is the IDENTITY of the surviving bead, never a stored count: "does
// it still report N?" passes on a broken guard and fails on a working one.
//
// The two arms also control each other. The existence probe is the same query for
// both, so a probe that is blind (always 0) fails arm 2 and a probe that is stuck
// (always 1) fails arm 1. Neither failure can be mistaken for a passing guard.
//
// ISOLATION: the store is a fresh embedded-Dolt database this test creates inside
// its own t.TempDir(). It is never a rig or town store and reaches no Dolt server,
// so the purge under test cannot touch a wisp this test did not create.
func TestCompletionPurgeAgeWindowBothArms(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping integration test")
	}
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not installed, skipping integration test (needed to back-date closed_at)")
	}

	dir := t.TempDir()
	bd := beads.NewIsolated(dir)
	if err := bd.Init("scr"); err != nil {
		t.Fatalf("bd init in scratch store: %v", err)
	}
	store := newScratchDoltStore(t, dir)

	// Two ephemeral beads (wisps). Both closed; only their closed_at differs.
	oldWisp := createClosedWisp(t, bd, "arm1: closed outside the window")
	newWisp := createClosedWisp(t, bd, "arm2: closed inside the window")

	// Back-date arm 1 well past completionPurgeAge (168h == 7d). bd derives its
	// cutoff from closed_at at day granularity, so 30d is unambiguously outside the
	// window and "just now" is unambiguously inside it.
	store.backdateClosedAt(t, oldWisp, 30*24*time.Hour)

	// Precondition: the probe must be able to express PRESENCE for both beads
	// before the purge. Without this, an arm-1 "collected" verdict is
	// indistinguishable from a probe that could never see the bead at all.
	if got := store.wispCount(t, oldWisp); got != 1 {
		t.Fatalf("precondition: old wisp %s count = %d, want 1", oldWisp, got)
	}
	if got := store.wispCount(t, newWisp); got != 1 {
		t.Fatalf("precondition: new wisp %s count = %d, want 1", newWisp, got)
	}

	// The code path under test — the real function gt done calls.
	purgeClosedEphemeralBeads(bd)

	if got := store.wispCount(t, oldWisp); got != 0 {
		t.Errorf("arm 1 FAILED: wisp %s closed 30d ago survived the purge (count=%d) — the age window has turned the completion step into a no-op (hq-6161m regression)", oldWisp, got)
	}
	if got := store.wispCount(t, newWisp); got != 1 {
		t.Errorf("arm 2 FAILED: wisp %s closed seconds ago was destroyed (count=%d) — the age window is not gating the completion-step purge (op-vps9)", newWisp, got)
	}
}

// TestCompletionPurgeAgeMatchesReaperGate pins the window to the house gate.
// gt reaper purge's --purge-age default and lifecycle.reaper.delete_age are both
// 168h. If one moves and this does not, the routine completion step every polecat
// runs at every turn end becomes the more aggressive of the two again, which is the
// asymmetry op-vps9 exists to close.
func TestCompletionPurgeAgeMatchesReaperGate(t *testing.T) {
	if completionPurgeAge != "168h" {
		t.Errorf("completionPurgeAge = %q, want 168h to match gt reaper purge --purge-age", completionPurgeAge)
	}
}

func createClosedWisp(t *testing.T, bd *beads.Beads, title string) string {
	t.Helper()
	issue, err := bd.Create(beads.CreateOptions{
		Title:     title,
		Type:      "task",
		Priority:  2,
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("create ephemeral bead %q: %v", title, err)
	}
	if err := bd.Close(issue.ID); err != nil {
		t.Fatalf("close %s: %v", issue.ID, err)
	}
	return issue.ID
}

// scratchDoltStore talks to the test's own embedded-Dolt directory with the dolt
// CLI. `bd sql` refuses to run in embedded mode ("not yet supported in embedded
// mode"), and bd has no CLI flag for writing closed_at, so back-dating a bead has
// to go through dolt directly.
type scratchDoltStore struct {
	dataDir string
	dbName  string
}

func newScratchDoltStore(t *testing.T, workDir string) *scratchDoltStore {
	t.Helper()
	metaPath := filepath.Join(workDir, ".beads", "metadata.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read %s: %v", metaPath, err)
	}
	var meta struct {
		DoltDatabase string `json:"dolt_database"`
		DoltMode     string `json:"dolt_mode"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("parse %s: %v", metaPath, err)
	}
	// Assert the isolation this test's safety rests on, rather than assuming it.
	if meta.DoltMode != "embedded" {
		t.Fatalf("scratch store is not embedded (dolt_mode=%q) — refusing to run a purge against a shared server", meta.DoltMode)
	}
	if meta.DoltDatabase == "" {
		t.Fatalf("scratch store has no dolt_database in %s", metaPath)
	}
	return &scratchDoltStore{
		dataDir: filepath.Join(workDir, ".beads", "embeddeddolt"),
		dbName:  meta.DoltDatabase,
	}
}

func (s *scratchDoltStore) query(t *testing.T, stmt string) string {
	t.Helper()
	cmd := exec.Command("dolt", "--data-dir", s.dataDir, "--use-db", s.dbName, "sql", "-r", "csv", "-q", stmt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dolt sql %q: %v (%s)", stmt, err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

// backdateClosedAt moves a closed wisp's closed_at into the past.
func (s *scratchDoltStore) backdateClosedAt(t *testing.T, id string, age time.Duration) {
	t.Helper()
	when := time.Now().UTC().Add(-age).Format("2006-01-02 15:04:05")
	s.query(t, fmt.Sprintf("UPDATE wisps SET closed_at='%s', updated_at='%s' WHERE id='%s'", when, when, id))
	// Read back: an UPDATE matching zero rows exits 0, would leave arm 1 inside the
	// window, and the test would then pass for the wrong reason.
	got := s.query(t, fmt.Sprintf("SELECT closed_at FROM wisps WHERE id='%s'", id))
	if !strings.Contains(got, when[:10]) {
		t.Fatalf("back-date did not land for %s: closed_at readback %q does not contain %q",
			id, strings.TrimSpace(got), when[:10])
	}
}

// wispCount returns how many rows the wisps table holds for id: 1 or 0. Any
// failure of the probe itself is fatal rather than reported as 0 — a zero from a
// broken probe and a true zero are byte-identical.
func (s *scratchDoltStore) wispCount(t *testing.T, id string) int {
	t.Helper()
	out := s.query(t, fmt.Sprintf("SELECT COUNT(*) FROM wisps WHERE id='%s'", id))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	n, err := strconv.Atoi(last)
	if err != nil {
		t.Fatalf("existence probe for %s returned unparseable count %q (full output %q)", id, last, out)
	}
	return n
}
