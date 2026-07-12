package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/dog"
)

// stubBdForWispClose installs a bd stub that serves hooked/in_progress wisp
// queries for one root each, empty child lists, and logs every invocation to
// bd-calls.log under townRoot.
func stubBdForWispClose(t *testing.T, townRoot string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bd stub")
	}

	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	callLog := filepath.Join(townRoot, "bd-calls.log")
	bdScript := `#!/bin/sh
echo "$@" >> "` + callLog + `"
case "$1" in
  query)
    case "$3" in
      *'status="hooked"'*)
        echo '[{"id":"hq-wisp-root1","title":"mol-dog-doctor","status":"hooked"}]'
        ;;
      *'status="in_progress"'*)
        echo '[{"id":"hq-wisp-root2","title":"mol-dog-reaper","status":"in_progress"}]'
        ;;
      *)
        echo '[]'
        ;;
    esac
    ;;
  list)
    echo '[]'
    ;;
esac
exit 0
`
	writeBDStub(t, binDir, bdScript, "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return callLog
}

// TestCloseAgentHookedWisps_ClosesHookedAndInProgress verifies that the sweep
// closes every ephemeral wisp molecule still hooked to (or in progress for)
// the agent, with the given close reason. Regression test for hq-oeq9: dogs
// ending a run without closing their slung formula wisps was the dominant
// wisp-accrual class behind the recurring Dolt connection cascades.
func TestCloseAgentHookedWisps_ClosesHookedAndInProgress(t *testing.T) {
	townRoot := t.TempDir()
	callLog := stubBdForWispClose(t, townRoot)

	closed := closeAgentHookedWisps(townRoot, "deacon/dogs/alpha", "dog run complete: closed by gt dog done")
	if closed != 2 {
		t.Fatalf("closeAgentHookedWisps() = %d, want 2", closed)
	}

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	log := string(data)

	if !strings.Contains(log, `assignee="deacon/dogs/alpha"`) {
		t.Errorf("wisp queries should filter by dog assignee; got:\n%s", log)
	}
	if !strings.Contains(log, "ephemeral=true") {
		t.Errorf("wisp queries should target ephemeral wisps only; got:\n%s", log)
	}
	for _, root := range []string{"hq-wisp-root1", "hq-wisp-root2"} {
		if !strings.Contains(log, "close "+root) {
			t.Errorf("root wisp %s was not closed; got:\n%s", root, log)
		}
	}
	if !strings.Contains(log, "--reason=dog run complete: closed by gt dog done") {
		t.Errorf("close should carry the dog-done reason; got:\n%s", log)
	}
	if !strings.Contains(log, "--force") {
		t.Errorf("close should force-close hooked wisps; got:\n%s", log)
	}
}

// TestCloseAgentHookedWisps_NoWisps verifies the sweep is a quiet no-op when
// the agent has nothing hooked.
func TestCloseAgentHookedWisps_NoWisps(t *testing.T) {
	townRoot := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bd stub")
	}
	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	writeBDStub(t, binDir, "#!/bin/sh\necho '[]'\nexit 0\n", "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if closed := closeAgentHookedWisps(townRoot, "deacon/dogs/alpha", "test"); closed != 0 {
		t.Fatalf("closeAgentHookedWisps() = %d, want 0", closed)
	}
}

// TestRunDogDone_IdleDogStillClosesHookedWisps verifies that gt dog done
// sweeps hooked wisps even when the dog is already idle — a dog auto-cleared
// by the health checker must not strand its slung molecule.
func TestRunDogDone_IdleDogStillClosesHookedWisps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bd stub")
	}

	townRoot := t.TempDir()
	stubBdForWispClose(t, townRoot)
	t.Setenv("GT_TOWN_ROOT", townRoot)
	t.Chdir(townRoot)

	// Minimal town + idle dog state.
	if err := os.MkdirAll(filepath.Join(townRoot, "mayor"), 0o755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	rigsJSON := `{"version":1,"rigs":{}}`
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "rigs.json"), []byte(rigsJSON), 0o644); err != nil {
		t.Fatalf("write rigs.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	dogPath := filepath.Join(townRoot, "deacon", "dogs", "alpha")
	if err := os.MkdirAll(dogPath, 0o755); err != nil {
		t.Fatalf("mkdir dog path: %v", err)
	}
	now := time.Now()
	state := &dog.DogState{Name: "alpha", State: dog.StateIdle, CreatedAt: now, UpdatedAt: now}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal dog state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dogPath, ".dog.json"), data, 0o644); err != nil {
		t.Fatalf("write dog state: %v", err)
	}

	var closedFor string
	origFn := closeDogHookedWispsFn
	closeDogHookedWispsFn = func(dogName string) { closedFor = dogName }
	defer func() { closeDogHookedWispsFn = origFn }()

	if err := runDogDone(nil, []string{"alpha"}); err != nil {
		t.Fatalf("runDogDone() error = %v", err)
	}
	if closedFor != "alpha" {
		t.Fatalf("runDogDone should close hooked wisps for the dog; closed for %q, want %q", closedFor, "alpha")
	}
}
