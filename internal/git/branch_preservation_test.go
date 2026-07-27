package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupPreservationRepo builds a clone that has real remote-tracking refs
// (refs/remotes/origin/*), which is the shape every caller of
// DeleteBranchPreserved operates on: a rig's .repo.git or a mayor clone.
//
// A plain `git clone --bare` would NOT do — it creates no refs/remotes at all,
// which makes every branch look unpreserved and every assertion here vacuous.
func setupPreservationRepo(t *testing.T) (workDir string, g *Git) {
	t.Helper()

	root := t.TempDir()
	originDir := filepath.Join(root, "origin.git")
	if err := exec.Command("git", "init", "-q", "--bare", originDir).Run(); err != nil {
		t.Fatalf("init origin: %v", err)
	}

	workDir = filepath.Join(root, "work")
	if err := exec.Command("git", "clone", "-q", originDir, workDir).Run(); err != nil {
		t.Fatalf("clone: %v", err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test User")

	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", ".")
	run("commit", "-qm", "initial")
	run("branch", "-M", "master")
	run("push", "-q", "-u", "origin", "master")

	return workDir, NewGit(workDir)
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commitOnBranch creates branch off master with one commit, then returns to
// master so the branch is deletable.
func commitOnBranch(t *testing.T, dir, branch, file string) {
	t.Helper()
	gitIn(t, dir, "checkout", "-q", "-b", branch, "master")
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "work on "+branch)
	gitIn(t, dir, "checkout", "-q", "master")
}

func branchExists(t *testing.T, g *Git, name string) bool {
	t.Helper()
	exists, err := g.BranchExists(name)
	if err != nil {
		t.Fatalf("BranchExists(%s): %v", name, err)
	}
	return exists
}

// TestDeleteBranchPreservedKeepsUnpushedBranch is the incident configuration:
// work is COMMITTED but was never pushed, so it lives on no remote. Before
// op-id26 this reached `git branch -D`, which exists specifically to bypass
// git's refusal to delete unmerged work, and the commits were unrecoverable.
func TestDeleteBranchPreservedKeepsUnpushedBranch(t *testing.T) {
	dir, g := setupPreservationRepo(t)
	commitOnBranch(t, dir, "polecat/unpushed", "work.txt")

	preserved, err := g.BranchPreserved("polecat/unpushed")
	if err != nil {
		t.Fatalf("BranchPreserved: %v", err)
	}
	if preserved {
		t.Fatal("branch was never pushed but reported as preserved")
	}

	// Deletion is expected to fail — that refusal IS the fix. Callers already
	// treat this as non-fatal warn-and-continue.
	if err := g.DeleteBranchPreserved("polecat/unpushed"); err == nil {
		t.Error("expected git to refuse deleting an unpushed branch, got nil error")
	}
	if !branchExists(t, g, "polecat/unpushed") {
		t.Fatal("UNPUSHED BRANCH DESTROYED — the commits existed nowhere else")
	}
}

// TestDeleteBranchPreservedDeletesPushedBranch is the positive control for the
// test above: without it, a DeleteBranchPreserved that refused everything
// would pass the incident test while breaking every ordinary teardown.
func TestDeleteBranchPreservedDeletesPushedBranch(t *testing.T) {
	dir, g := setupPreservationRepo(t)
	commitOnBranch(t, dir, "polecat/pushed", "work.txt")
	gitIn(t, dir, "push", "-q", "origin", "polecat/pushed")

	preserved, err := g.BranchPreserved("polecat/pushed")
	if err != nil {
		t.Fatalf("BranchPreserved: %v", err)
	}
	if !preserved {
		t.Fatal("branch was pushed but reported as unpreserved")
	}

	if err := g.DeleteBranchPreserved("polecat/pushed"); err != nil {
		t.Fatalf("DeleteBranchPreserved on a pushed branch: %v", err)
	}
	if branchExists(t, g, "polecat/pushed") {
		t.Fatal("pushed branch survived — ordinary teardown would now accumulate refs")
	}
}

// TestDeleteBranchPreservedDeletesPushedUnmergedBranch pins the reason the
// one-character `-D`->`-d` fix was not sufficient on its own.
//
// `git branch -d` only accepts a branch merged into its UPSTREAM (or HEAD).
// Polecat branches are cut from master and inherit upstream=origin/master, so
// a fully-pushed but not-yet-merged branch is refused by plain `-d`. Measured
// on the live openclaw rig: 26 of 30 local polecat branches would be refused,
// and all 26 were already safe on a remote — pure ref accumulation, zero work
// preserved. This case must delete.
func TestDeleteBranchPreservedDeletesPushedUnmergedBranch(t *testing.T) {
	dir, g := setupPreservationRepo(t)
	commitOnBranch(t, dir, "polecat/pushed-unmerged", "work.txt")
	gitIn(t, dir, "push", "-q", "origin", "polecat/pushed-unmerged")
	// Reproduce the real config: upstream points at master, not at itself.
	gitIn(t, dir, "branch", "--set-upstream-to=origin/master", "polecat/pushed-unmerged")

	// Establish that plain `-d` really does refuse here, so this test is
	// pinning a difference rather than asserting a tautology.
	cmd := exec.Command("git", "branch", "-d", "polecat/pushed-unmerged")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		t.Fatal("precondition failed: plain -d accepted a pushed-but-unmerged branch")
	}

	if err := g.DeleteBranchPreserved("polecat/pushed-unmerged"); err != nil {
		t.Fatalf("DeleteBranchPreserved on a pushed-but-unmerged branch: %v", err)
	}
	if branchExists(t, g, "polecat/pushed-unmerged") {
		t.Fatal("pushed-but-unmerged branch survived; plain -d semantics leaked through")
	}
}

// TestBranchPreservedAcceptsAnyRemoteRef covers a branch preserved only by a
// rescue push under a DIFFERENT name. Scoping the evidence to
// refs/remotes/origin/<branch> would call this unpreserved; on the live
// openclaw rig one branch is preserved exactly this way.
func TestBranchPreservedAcceptsAnyRemoteRef(t *testing.T) {
	dir, g := setupPreservationRepo(t)
	commitOnBranch(t, dir, "polecat/rescued", "work.txt")
	gitIn(t, dir, "push", "-q", "origin", "polecat/rescued:rescue/somebody/rescued")

	preserved, err := g.BranchPreserved("polecat/rescued")
	if err != nil {
		t.Fatalf("BranchPreserved: %v", err)
	}
	if !preserved {
		t.Fatal("commits are on a remote under a rescue name but reported unpreserved")
	}
}

// TestBranchPreservedMergedToRemoteDefault: commits already on origin/master
// are preserved even though no branch-shaped remote ref names them.
func TestBranchPreservedMergedToRemoteDefault(t *testing.T) {
	dir, g := setupPreservationRepo(t)
	commitOnBranch(t, dir, "polecat/merged", "work.txt")
	gitIn(t, dir, "merge", "-q", "--ff-only", "polecat/merged")
	gitIn(t, dir, "push", "-q", "origin", "master")

	preserved, err := g.BranchPreserved("polecat/merged")
	if err != nil {
		t.Fatalf("BranchPreserved: %v", err)
	}
	if !preserved {
		t.Fatal("commits are on origin/master but reported unpreserved")
	}
}

// TestUnpreservedBranchesBatch is the sweep path: one revision walk must
// classify a mixed set correctly. The batching is not a micro-optimisation —
// per-branch `git branch -r --contains` costs ~27s each on a repo with 18k
// remote refs, so a sweep would take minutes.
func TestUnpreservedBranchesBatch(t *testing.T) {
	dir, g := setupPreservationRepo(t)
	commitOnBranch(t, dir, "polecat/a-pushed", "a.txt")
	gitIn(t, dir, "push", "-q", "origin", "polecat/a-pushed")
	commitOnBranch(t, dir, "polecat/b-unpushed", "b.txt")
	commitOnBranch(t, dir, "polecat/c-pushed", "c.txt")
	gitIn(t, dir, "push", "-q", "origin", "polecat/c-pushed")

	got, err := g.UnpreservedBranches([]string{"polecat/a-pushed", "polecat/b-unpushed", "polecat/c-pushed"})
	if err != nil {
		t.Fatalf("UnpreservedBranches: %v", err)
	}
	if got["polecat/a-pushed"] || got["polecat/c-pushed"] {
		t.Errorf("pushed branches reported unpreserved: %v", got)
	}
	if !got["polecat/b-unpushed"] {
		t.Errorf("unpushed branch not reported: %v", got)
	}
}

// TestUnpreservedBranchesUnresolvableIsUnpreserved: a probe that cannot answer
// must not render as "safe to destroy".
func TestUnpreservedBranchesUnresolvableIsUnpreserved(t *testing.T) {
	_, g := setupPreservationRepo(t)

	got, err := g.UnpreservedBranches([]string{"polecat/does-not-exist"})
	if err != nil {
		t.Fatalf("UnpreservedBranches: %v", err)
	}
	if !got["polecat/does-not-exist"] {
		t.Fatal("an unresolvable branch was reported as preserved")
	}
}

// TestUnpreservedBranchesNoRemoteRefs: in a repo with no remote-tracking refs
// at all, nothing can be proven safe, so nothing is force-deleted.
func TestUnpreservedBranchesNoRemoteRefs(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)
	gitIn(t, dir, "branch", "polecat/local-only")

	got, err := g.UnpreservedBranches([]string{"polecat/local-only"})
	if err != nil {
		t.Fatalf("UnpreservedBranches: %v", err)
	}
	if !got["polecat/local-only"] {
		t.Fatal("branch reported preserved in a repo that has no remotes")
	}
}

// TestErrBranchKeptClassifiesRefusal pins the distinction the nuke path needs
// in order to explain itself: a refusal that PRESERVED work must be
// distinguishable from a delete that merely failed.
//
// Without it the guard's only user-visible output is git's own stderr, whose
// last line reads "If you are sure you want to delete it, run 'git branch -D
// <name>'". The one message emitted when the guard fires would be instructions
// for defeating it — and the accumulated refs carry no explanation, so whoever
// finds them later force-deletes them and reintroduces the fault outside the
// code path op-id26 fixed.
//
// The two subtests form a partition and are therefore self-controlling: the
// classifier demonstrates in-run that it can return both verdicts, so a
// blanket "everything is kept" implementation cannot pass.
func TestErrBranchKeptClassifiesRefusal(t *testing.T) {
	t.Run("kept: unpushed work is refused AND labelled", func(t *testing.T) {
		dir, g := setupPreservationRepo(t)
		commitOnBranch(t, dir, "polecat/unpushed", "work.txt")

		err := g.DeleteBranchPreserved("polecat/unpushed")
		if err == nil {
			t.Fatal("expected refusal on an unpushed branch")
		}
		if !errors.Is(err, ErrBranchKept) {
			t.Errorf("refusal not classified as ErrBranchKept, so the caller\n"+
				"cannot say why the ref survived; got: %v", err)
		}
		if !branchExists(t, g, "polecat/unpushed") {
			t.Fatal("UNPUSHED BRANCH DESTROYED — the commits existed nowhere else")
		}
	})

	t.Run("not kept: a missing branch must not be reported as preserved work", func(t *testing.T) {
		_, g := setupPreservationRepo(t)

		// Nuke reaches this step with a branch name recorded earlier, which
		// may already be gone. Nothing was preserved, so claiming otherwise
		// would raise a false alarm naming a ref nobody can recover.
		err := g.DeleteBranchPreserved("polecat/never-existed")
		if err == nil {
			t.Fatal("expected an error deleting a nonexistent branch")
		}
		if errors.Is(err, ErrBranchKept) {
			t.Errorf("a nonexistent branch was reported as kept work: %v", err)
		}
	})
}
