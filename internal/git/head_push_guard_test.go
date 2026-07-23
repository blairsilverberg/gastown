package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the hq-ushes fix: a polecat auto-checkpoint push from a
// detached-HEAD checkout must never create a remote ref literally named HEAD.

func TestRefuseHeadRefPush(t *testing.T) {
	tests := []struct {
		refspec string
		refused bool
	}{
		{"HEAD", true},
		{"HEAD:HEAD", true},
		{"feature:HEAD", true},
		{"+HEAD:HEAD", true},
		{"HEAD:refs/heads/HEAD", true},
		{" HEAD ", true},
		{"HEAD:refs/heads/polecat/toast/checkpoint", false},
		{"HEAD:main", false},
		{"polecat/toast/gt-abc", false},
		{"polecat/toast/gt-abc:polecat/toast/gt-abc", false},
		{"main", false},
	}
	for _, tt := range tests {
		err := RefuseHeadRefPush(tt.refspec)
		if tt.refused && err == nil {
			t.Errorf("RefuseHeadRefPush(%q) = nil, want error", tt.refspec)
		}
		if !tt.refused && err != nil {
			t.Errorf("RefuseHeadRefPush(%q) = %v, want nil", tt.refspec, err)
		}
	}
}

// setupCheckpointPushRepos creates a work repo with an initial commit on a
// feature branch and a bare origin remote. Returns (workDir, bareDir).
func setupCheckpointPushRepos(t *testing.T) (string, string) {
	t.Helper()

	workDir := initTestRepo(t)
	runGit(t, workDir, "checkout", "-b", "polecat/toast/gt-abc")
	// Diverge from the default branch so only the polecat branch points at
	// HEAD — mirrors a real polecat worktree with committed work.
	if err := os.WriteFile(filepath.Join(workDir, "work.txt"), []byte("work\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, workDir, "add", ".")
	runGit(t, workDir, "commit", "-m", "feat: polecat work")

	bareDir := filepath.Join(t.TempDir(), "origin.git")
	cmd := exec.Command("git", "init", "--bare", bareDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	runGit(t, workDir, "remote", "add", "origin", bareDir)

	return workDir, bareDir
}

func remoteHasRef(t *testing.T, bareDir, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = bareDir
	return cmd.Run() == nil
}

func detachAndCommit(t *testing.T, workDir string) {
	t.Helper()
	runGit(t, workDir, "checkout", "--detach")
	if err := os.WriteFile(filepath.Join(workDir, "wip.txt"), []byte("wip\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, workDir, "add", ".")
	runGit(t, workDir, "commit", "-m", "WIP: checkpoint (auto)")
}

func TestPushRefusesLiteralHeadBranch(t *testing.T) {
	workDir, bareDir := setupCheckpointPushRepos(t)
	detachAndCommit(t, workDir)

	g := NewGit(workDir)

	// This is the exact incident shape: CurrentBranch() returned "HEAD" in a
	// detached checkout and the checkpoint code pushed it verbatim.
	branch, err := g.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "HEAD" {
		t.Fatalf("expected detached checkout to report literal HEAD, got %q", branch)
	}

	for _, refspec := range []string{branch, branch + ":" + branch} {
		if err := g.Push("origin", refspec, false); err == nil {
			t.Errorf("Push(origin, %q) succeeded, want refusal", refspec)
		}
	}
	if err := g.PushWithEnv("origin", "HEAD:HEAD", false, nil); err == nil {
		t.Error("PushWithEnv(origin, HEAD:HEAD) succeeded, want refusal")
	}

	if remoteHasRef(t, bareDir, "refs/heads/HEAD") {
		t.Fatal("remote has refs/heads/HEAD — the guard did not prevent the incident ref")
	}
}

func TestCheckpointPushRefspecOnBranch(t *testing.T) {
	workDir, bareDir := setupCheckpointPushRepos(t)
	g := NewGit(workDir)

	branch, refspec, err := g.CheckpointPushRefspec("polecat/toast/checkpoint")
	if err != nil {
		t.Fatalf("CheckpointPushRefspec: %v", err)
	}
	if branch != "polecat/toast/gt-abc" {
		t.Errorf("branch = %q, want polecat/toast/gt-abc", branch)
	}
	if refspec != "polecat/toast/gt-abc:polecat/toast/gt-abc" {
		t.Errorf("refspec = %q, want branch:branch", refspec)
	}

	if err := g.Push("origin", refspec, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !remoteHasRef(t, bareDir, "refs/heads/polecat/toast/gt-abc") {
		t.Error("remote missing refs/heads/polecat/toast/gt-abc after on-branch checkpoint push")
	}
	if remoteHasRef(t, bareDir, "refs/heads/HEAD") {
		t.Error("remote has refs/heads/HEAD after on-branch checkpoint push")
	}
}

func TestCheckpointPushRefspecDetachedAtBranchTip(t *testing.T) {
	workDir, bareDir := setupCheckpointPushRepos(t)
	// Detach at the branch tip WITHOUT new commits: the feature branch still
	// points at HEAD, so the checkpoint must resolve the real branch.
	runGit(t, workDir, "checkout", "--detach")

	g := NewGit(workDir)
	branch, refspec, err := g.CheckpointPushRefspec("polecat/toast/checkpoint")
	if err != nil {
		t.Fatalf("CheckpointPushRefspec: %v", err)
	}
	if branch != "polecat/toast/gt-abc" {
		t.Errorf("branch = %q, want polecat/toast/gt-abc (the branch pointing at HEAD)", branch)
	}
	if refspec != "HEAD:refs/heads/polecat/toast/gt-abc" {
		t.Errorf("refspec = %q, want HEAD:refs/heads/polecat/toast/gt-abc", refspec)
	}

	if err := g.Push("origin", refspec, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !remoteHasRef(t, bareDir, "refs/heads/polecat/toast/gt-abc") {
		t.Error("remote missing refs/heads/polecat/toast/gt-abc after detached checkpoint push")
	}
	if remoteHasRef(t, bareDir, "refs/heads/HEAD") {
		t.Error("remote has refs/heads/HEAD after detached checkpoint push")
	}
}

func TestCheckpointPushRefspecDetachedWithNewCommits(t *testing.T) {
	workDir, bareDir := setupCheckpointPushRepos(t)
	detachAndCommit(t, workDir)

	g := NewGit(workDir)
	// No local branch points at the detached commit — the checkpoint must
	// synthesize the fallback branch, never a ref named HEAD.
	branch, refspec, err := g.CheckpointPushRefspec("polecat/toast/gt-abc-checkpoint")
	if err != nil {
		t.Fatalf("CheckpointPushRefspec: %v", err)
	}
	if branch != "polecat/toast/gt-abc-checkpoint" {
		t.Errorf("branch = %q, want fallback polecat/toast/gt-abc-checkpoint", branch)
	}
	if refspec != "HEAD:refs/heads/polecat/toast/gt-abc-checkpoint" {
		t.Errorf("refspec = %q, want HEAD:refs/heads/polecat/toast/gt-abc-checkpoint", refspec)
	}

	if err := g.Push("origin", refspec, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !remoteHasRef(t, bareDir, "refs/heads/polecat/toast/gt-abc-checkpoint") {
		t.Error("remote missing synthesized checkpoint branch after detached push")
	}
	if remoteHasRef(t, bareDir, "refs/heads/HEAD") {
		t.Error("remote has refs/heads/HEAD after detached checkpoint push")
	}
}

func TestCheckpointPushRefspecDetachedNoFallback(t *testing.T) {
	workDir, _ := setupCheckpointPushRepos(t)
	detachAndCommit(t, workDir)

	g := NewGit(workDir)
	for _, fallback := range []string{"", "HEAD"} {
		if _, _, err := g.CheckpointPushRefspec(fallback); err == nil {
			t.Errorf("CheckpointPushRefspec(%q) = nil error, want failure with no usable fallback", fallback)
		} else if !strings.Contains(err.Error(), "detached HEAD") {
			t.Errorf("CheckpointPushRefspec(%q) error = %v, want detached HEAD explanation", fallback, err)
		}
	}
}
