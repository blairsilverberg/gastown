package daemon

import (
	"time"

	"github.com/steveyegge/gastown/internal/reaper"
)

const (
	// defaultStepWispJanitorInterval is how often the janitor sweeps.
	defaultStepWispJanitorInterval = 1 * time.Hour
	// defaultStepWispJanitorMaxAge: open ephemeral wisps older than this with no
	// open parent are closed. Matches the wisp reaper's default max age.
	defaultStepWispJanitorMaxAge = 24 * time.Hour
)

// StepWispJanitorConfig holds configuration for the step_wisp_janitor patrol.
//
// Unlike the other dog patrols this one defaults to ENABLED when absent from
// mayor/daemon.json: it is the belt-and-braces backstop for leaked ephemeral
// step wisps (hq-oeq9), and must keep running even when the wisp_reaper's
// dog-dispatch path is broken — which is exactly when wisps flood.
type StepWispJanitorConfig struct {
	Enabled     bool     `json:"enabled"`
	IntervalStr string   `json:"interval,omitempty"`
	MaxAgeStr   string   `json:"max_age,omitempty"`
	Databases   []string `json:"databases,omitempty"`
}

// stepWispJanitorInterval returns the configured interval, or the default (1h).
func stepWispJanitorInterval(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.StepWispJanitor != nil {
		if config.Patrols.StepWispJanitor.IntervalStr != "" {
			if d, err := time.ParseDuration(config.Patrols.StepWispJanitor.IntervalStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultStepWispJanitorInterval
}

// stepWispJanitorMaxAge returns the configured max age, or the default (24h).
func stepWispJanitorMaxAge(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.StepWispJanitor != nil {
		if config.Patrols.StepWispJanitor.MaxAgeStr != "" {
			if d, err := time.ParseDuration(config.Patrols.StepWispJanitor.MaxAgeStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultStepWispJanitorMaxAge
}

// stepWispJanitorDatabases returns the configured database list, or nil to
// auto-discover.
func stepWispJanitorDatabases(config *DaemonPatrolConfig) []string {
	if config != nil && config.Patrols != nil && config.Patrols.StepWispJanitor != nil {
		return config.Patrols.StepWispJanitor.Databases
	}
	return nil
}

// runStepWispJanitor closes leaked ephemeral step wisps directly via SQL.
//
// This is the inline belt-and-braces companion to the wisp_reaper patrol.
// The reaper pours a molecule and dispatches a Dog agent for formula-driven
// execution — but a Dog is a Claude session that itself leaks wisps when its
// run ends without cleanup, and it cannot work at all while Dolt is degraded,
// which is precisely when leaked wisps pile into a connection cascade
// (hq-oeq9: ~4000/day accrual, town-wide gt mail outage every ~34h).
//
// The janitor makes no bd calls, pours no molecule, and dispatches nothing:
// it runs reaper.Reap in-process on every database, closing (a) step wisps
// whose parent molecule is already closed, and (b) open ephemeral bare wisps
// older than maxAge with no open parent. Both operations are idempotent, so
// overlap with a successful reaper Dog run is harmless.
func (d *Daemon) runStepWispJanitor() {
	if !d.isPatrolActive("step_wisp_janitor") {
		return
	}

	maxAge := stepWispJanitorMaxAge(d.patrolConfig)
	host := d.doltServerHost()
	port := d.doltServerPort()

	databases := stepWispJanitorDatabases(d.patrolConfig)
	if len(databases) == 0 {
		databases = reaper.DiscoverDatabases(host, port)
	}
	if len(databases) == 0 {
		d.logger.Printf("step_wisp_janitor: no databases to sweep")
		return
	}

	var totalReaped, totalMoleculeSteps, totalOpen, errCount int
	for _, dbName := range databases {
		if err := reaper.ValidateDBName(dbName); err != nil {
			continue
		}
		db, err := reaper.OpenDB(host, port, dbName, 10*time.Second, 10*time.Second)
		if err != nil {
			d.logger.Printf("step_wisp_janitor: %s: connect error: %v", dbName, err)
			errCount++
			continue
		}
		if ok, _ := reaper.HasReaperSchema(db); !ok {
			db.Close()
			continue
		}
		result, err := reaper.Reap(db, dbName, maxAge, false)
		db.Close()
		if err != nil {
			d.logger.Printf("step_wisp_janitor: %s: reap error: %v", dbName, err)
			errCount++
			continue
		}
		totalReaped += result.Reaped
		totalMoleculeSteps += result.MoleculeStepsClosed
		totalOpen += result.OpenRemain
		if result.Reaped > 0 || result.MoleculeStepsClosed > 0 {
			d.logger.Printf("step_wisp_janitor: %s: closed %d molecule steps, reaped %d stale wisps, %d open remain",
				dbName, result.MoleculeStepsClosed, result.Reaped, result.OpenRemain)
		}
	}

	if totalReaped > 0 || totalMoleculeSteps > 0 || errCount > 0 {
		d.logger.Printf("step_wisp_janitor: sweep complete — molecule_steps_closed=%d reaped=%d open=%d errors=%d databases=%d",
			totalMoleculeSteps, totalReaped, totalOpen, errCount, len(databases))
	}
}
