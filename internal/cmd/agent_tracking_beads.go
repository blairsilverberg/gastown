package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/beads"
)

// findCwdBeadsWorkDir finds the nearest .beads directory by walking up from CWD.
// It intentionally ignores BEADS_DIR for callers whose target is implied by
// the current rig worktree rather than inherited session environment.
func findCwdBeadsWorkDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	path := cwd
	for {
		if _, err := os.Stat(filepath.Join(path, ".beads")); err == nil {
			return path, nil
		}

		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}

	return "", fmt.Errorf("no .beads directory found")
}

// resolveAgentTrackingBeadsDir resolves the bead database used for agent state.
// Agent tracking follows the agent's current rig, so cwd-local redirects must
// win over an inherited town-level BEADS_DIR. The env-first resolver remains a
// fallback for contexts that do not have a cwd-local .beads directory.
func resolveAgentTrackingBeadsDir() (string, error) {
	workDir, err := findCwdBeadsWorkDir()
	if err != nil {
		workDir, err = findLocalBeadsDir()
	}
	if err != nil {
		return "", err
	}

	beadsDir := beads.ResolveBeadsDir(workDir)
	if beadsDir == "" {
		return "", fmt.Errorf("not in a beads workspace")
	}
	return beadsDir, nil
}

// resolveAgentBeadState locates the beads database that actually contains the
// given agent bead and returns its state labels (key:value pairs).
//
// Patrol agents run from a rig cwd, but agent beads — including rig-prefixed
// ones like gt-gastown-witness — are registered in the town (hq) database, not
// the rig database (see findAgentBeadCandidates in agents_resolve.go). Prefix
// routing cannot distinguish these, so we probe by lookup: rig-local database
// first, then the town database (hq-lckrv: a rig-local-only lookup made
// await-signal idle tracking silently no-op).
//
// On success, returns the beads dir that resolved the bead plus its labels.
// On failure, returns a best-effort fallback dir (rig-local when available,
// town otherwise — possibly empty) and a non-nil error describing both probes.
func resolveAgentBeadState(agentBead string) (string, map[string]string, error) {
	localDir, localErr := resolveAgentTrackingBeadsDir()
	if localErr == nil {
		labels, err := getAgentLabels(agentBead, localDir)
		if err == nil {
			return localDir, labels, nil
		}
		localErr = err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return localDir, nil, fmt.Errorf("rig database lookup failed (%v); getting working directory for town fallback: %w", localErr, err)
	}
	townRoot := beads.FindTownRoot(cwd)
	if townRoot == "" {
		return localDir, nil, fmt.Errorf("rig database lookup failed (%v); no town root found for hq fallback", localErr)
	}
	townDir := beads.ResolveBeadsDir(beads.GetTownBeadsPath(townRoot))
	if townDir == "" || (localDir != "" && filepath.Clean(townDir) == filepath.Clean(localDir)) {
		return localDir, nil, fmt.Errorf("agent bead %s not resolved in %s: %v", agentBead, localDir, localErr)
	}

	labels, townErr := getAgentLabels(agentBead, townDir)
	if townErr != nil {
		fallback := localDir
		if fallback == "" {
			fallback = townDir
		}
		return fallback, nil, fmt.Errorf("agent bead %s not resolved: rig database (%s): %v; town database (%s): %v",
			agentBead, localDir, localErr, townDir, townErr)
	}
	return townDir, labels, nil
}
