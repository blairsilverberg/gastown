package doltserver

import (
	"os"
	"path/filepath"
	"testing"
)

// buildBdPidTestTown creates a town layout with a town-level .beads dir, two
// rigs with .beads dirs, one rig without, and a hidden data dir.
func buildBdPidTestTown(t *testing.T) string {
	t.Helper()
	townRoot := t.TempDir()
	for _, dir := range []string{
		".beads",
		".dolt-data",
		filepath.Join("openclaw", ".beads"),
		filepath.Join("gastown", ".beads"),
		filepath.Join("mayor"), // no .beads — must not get a pid file
	} {
		if err := os.MkdirAll(filepath.Join(townRoot, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return townRoot
}

// TestBdPidFilePaths verifies every bd-expected location is covered: the town
// root, the town .beads dir, and each rig's .beads dir — and nothing else.
// Regression test for the stale-pidfile failure (upstream beads #4145): after
// gt dolt start, bd read a dead PID from these files and silently fell back
// to an in-memory throwaway database.
func TestBdPidFilePaths(t *testing.T) {
	townRoot := buildBdPidTestTown(t)

	paths := bdPidFilePaths(townRoot)
	want := map[string]bool{
		filepath.Join(townRoot, "dolt-server.pid"):                       false,
		filepath.Join(townRoot, ".beads", "dolt-server.pid"):             false,
		filepath.Join(townRoot, "openclaw", ".beads", "dolt-server.pid"): false,
		filepath.Join(townRoot, "gastown", ".beads", "dolt-server.pid"):  false,
	}
	for _, p := range paths {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected pid file path: %s", p)
			continue
		}
		want[p] = true
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("missing pid file path: %s", p)
		}
	}
}

func TestBdPidFilePathsEmptyTownRoot(t *testing.T) {
	if paths := bdPidFilePaths(""); paths != nil {
		t.Errorf("bdPidFilePaths(\"\") = %v, want nil", paths)
	}
}

func TestWriteBdPidFiles(t *testing.T) {
	townRoot := buildBdPidTestTown(t)

	// Simulate a stale pid left by the previous server.
	stalePath := filepath.Join(townRoot, ".beads", "dolt-server.pid")
	if err := os.WriteFile(stalePath, []byte("99999\n"), 0o644); err != nil {
		t.Fatalf("write stale pid: %v", err)
	}

	if err := writeBdPidFiles(townRoot, 12345); err != nil {
		t.Fatalf("writeBdPidFiles() error = %v", err)
	}

	for _, p := range []string{
		filepath.Join(townRoot, "dolt-server.pid"),
		stalePath,
		filepath.Join(townRoot, "openclaw", ".beads", "dolt-server.pid"),
		filepath.Join(townRoot, "gastown", ".beads", "dolt-server.pid"),
	} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("pid file %s not written: %v", p, err)
			continue
		}
		if string(data) != "12345\n" {
			t.Errorf("pid file %s = %q, want %q", p, string(data), "12345\n")
		}
	}

	// A rig without .beads must not gain one.
	if _, err := os.Stat(filepath.Join(townRoot, "mayor", ".beads")); !os.IsNotExist(err) {
		t.Errorf("writeBdPidFiles must not create .beads dirs (err=%v)", err)
	}
}

func TestWriteBdPidFilesIdempotent(t *testing.T) {
	townRoot := buildBdPidTestTown(t)

	if err := writeBdPidFiles(townRoot, 777); err != nil {
		t.Fatalf("first write: %v", err)
	}
	path := filepath.Join(townRoot, "openclaw", ".beads", "dolt-server.pid")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after first write: %v", err)
	}

	// Second write with the same PID must leave files untouched.
	if err := writeBdPidFiles(townRoot, 777); err != nil {
		t.Fatalf("second write: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after second write: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("same-PID rewrite should be a no-op; mtime changed %v → %v", before.ModTime(), after.ModTime())
	}
}

func TestWriteBdPidFilesIgnoresNonPositivePid(t *testing.T) {
	townRoot := buildBdPidTestTown(t)
	if err := writeBdPidFiles(townRoot, 0); err != nil {
		t.Fatalf("writeBdPidFiles(0) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(townRoot, "dolt-server.pid")); !os.IsNotExist(err) {
		t.Errorf("pid 0 must not write files (err=%v)", err)
	}
}
