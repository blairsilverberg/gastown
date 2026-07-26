package polecat

// op-uhd2 / hq-khtga: hard liveness gate on destructive polecat reuse.
//
// Incident (2026-07-25 16:55Z, capital/basalt): a holding session's LIVE clone
// was reset twice by reuse-prep. The pre-existing fail-safe only refused when
// the agent-process probe (IsAgentAlive) succeeded — when process-name
// detection failed, the live session was killed and the clone hard-reset.
//
// The rule now is unconditional: if the polecat's tmux session EXISTS, its
// work-state is unverifiable from the outside and the clone must not be
// touched. Refuse reuse/reset, alert witness + mayor, and let the allocator
// pick another polecat. Genuinely-idle slots whose session lingers after
// gt done (persistent-polecat model) become reusable once the daemon's
// idle-session reaper or the witness clears the session.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/gastown/internal/channelevents"
	"github.com/steveyegge/gastown/internal/session"
)

// reuseRefusalAlertDebounce bounds witness/mayor alert frequency per session.
// The gate can fire every 30s from the daemon's stranded-convoy feed loop;
// one alert an hour per slot is signal, sixty a half-hour is noise.
const reuseRefusalAlertDebounce = time.Hour

// polecatSessionNameFor returns the canonical tmux session name for one of
// this rig's polecats.
func (m *Manager) polecatSessionNameFor(name string) string {
	return session.PolecatSessionName(session.PrefixFor(m.rig.Name), name)
}

// sessionLiveForReuse reports whether the polecat's tmux session exists.
// Errors are fail-closed: if liveness cannot be verified, the session is
// treated as live so no destructive action proceeds on uncertainty.
func (m *Manager) sessionLiveForReuse(name string) bool {
	if m.tmux == nil {
		return false
	}
	alive, err := m.tmux.HasSession(m.polecatSessionNameFor(name))
	if err != nil {
		return true
	}
	return alive
}

// refuseReuseIfSessionLive is the op-uhd2 hard gate. Called before ANY
// destructive step of reuse-prep (session kill, target/ clean, reset --hard,
// clean -fd, branch churn). A live session refuses with
// ErrPolecatNeedsRecovery so callers fall through to another slot.
func (m *Manager) refuseReuseIfSessionLive(name string) error {
	if m.tmux == nil {
		return nil
	}
	sessionName := m.polecatSessionNameFor(name)
	alive, err := m.tmux.HasSession(sessionName)
	if err != nil {
		return fmt.Errorf("%w: cannot verify tmux session %s liveness (%v) — refusing destructive reuse (op-uhd2)",
			ErrPolecatNeedsRecovery, sessionName, err)
	}
	if !alive {
		return nil
	}
	m.alertReuseRefusedLiveSession(name, sessionName)
	return fmt.Errorf("%w: tmux session %s exists — refusing to reset a live polecat clone; pick another polecat (op-uhd2)",
		ErrPolecatNeedsRecovery, sessionName)
}

// alertReuseRefusedLiveSession tells the rig witness and the mayor that
// dispatch tried to destructively reuse a slot whose session is live —
// exactly the actuator that caused material damage in hq-khtga instance #4.
// Debounced per session via a townRoot/.runtime marker file.
func (m *Manager) alertReuseRefusedLiveSession(name, sessionName string) {
	townRoot := filepath.Dir(m.rig.Path)
	if !shouldSendReuseRefusalAlert(townRoot, sessionName, time.Now()) {
		return
	}
	payload := []string{
		"source=polecat-manager",
		"rig=" + m.rig.Name,
		"polecat=" + name,
		"session=" + sessionName,
		"reason=live_session_reuse_refused",
	}
	_, _ = channelevents.EmitToTown(townRoot, "witness", "REUSE_REFUSED_LIVE_SESSION", payload)
	_, _ = channelevents.EmitToTown(townRoot, "mayor", "REUSE_REFUSED_LIVE_SESSION", payload)
	if m.tmux != nil {
		witnessSession := session.WitnessSessionName(session.PrefixFor(m.rig.Name))
		if running, err := m.tmux.HasSession(witnessSession); err == nil && running {
			_ = m.tmux.NudgeSession(witnessSession, fmt.Sprintf(
				"REUSE_REFUSED: dispatch tried to reset %s/%s while tmux session %s is live — slot skipped, verify its work-state (op-uhd2)",
				m.rig.Name, name, sessionName))
		}
	}
}

type reuseRefusalAlertMarker struct {
	LastAlert time.Time `json:"last_alert"`
}

// shouldSendReuseRefusalAlert is a file-based debounce (the callers are
// short-lived gt processes, so in-memory state cannot carry across firings).
// Tracking failures err toward alerting: a duplicate nudge beats silence.
func shouldSendReuseRefusalAlert(townRoot, sessionName string, now time.Time) bool {
	dir := filepath.Join(townRoot, ".runtime", "reuse-refusal-alerts")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return true
	}
	path := filepath.Join(dir, sessionName+".json")
	if data, err := os.ReadFile(path); err == nil {
		var marker reuseRefusalAlertMarker
		if json.Unmarshal(data, &marker) == nil && now.Sub(marker.LastAlert) < reuseRefusalAlertDebounce {
			return false
		}
	}
	if blob, err := json.Marshal(reuseRefusalAlertMarker{LastAlert: now}); err == nil {
		_ = os.WriteFile(path, blob, 0644)
	}
	return true
}
