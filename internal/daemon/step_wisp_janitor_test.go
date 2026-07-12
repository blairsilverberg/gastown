package daemon

import (
	"io"
	"log"
	"testing"
	"time"
)

func TestStepWispJanitorInterval(t *testing.T) {
	tests := []struct {
		name   string
		config *DaemonPatrolConfig
		want   time.Duration
	}{
		{"nil config", nil, defaultStepWispJanitorInterval},
		{"nil patrols", &DaemonPatrolConfig{}, defaultStepWispJanitorInterval},
		{"nil janitor", &DaemonPatrolConfig{Patrols: &PatrolsConfig{}}, defaultStepWispJanitorInterval},
		{"empty interval", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: true},
		}}, defaultStepWispJanitorInterval},
		{"configured interval", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: true, IntervalStr: "30m"},
		}}, 30 * time.Minute},
		{"invalid interval falls back", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: true, IntervalStr: "bogus"},
		}}, defaultStepWispJanitorInterval},
		{"negative interval falls back", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: true, IntervalStr: "-5m"},
		}}, defaultStepWispJanitorInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stepWispJanitorInterval(tt.config); got != tt.want {
				t.Errorf("stepWispJanitorInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStepWispJanitorMaxAge(t *testing.T) {
	tests := []struct {
		name   string
		config *DaemonPatrolConfig
		want   time.Duration
	}{
		{"nil config", nil, defaultStepWispJanitorMaxAge},
		{"configured max age", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: true, MaxAgeStr: "48h"},
		}}, 48 * time.Hour},
		{"invalid max age falls back", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: true, MaxAgeStr: "soon"},
		}}, defaultStepWispJanitorMaxAge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stepWispJanitorMaxAge(tt.config); got != tt.want {
				t.Errorf("stepWispJanitorMaxAge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStepWispJanitorDefaultsToOneHourAnd24h(t *testing.T) {
	if defaultStepWispJanitorInterval != time.Hour {
		t.Errorf("default janitor interval = %v, want 1h", defaultStepWispJanitorInterval)
	}
	if defaultStepWispJanitorMaxAge != 24*time.Hour {
		t.Errorf("default janitor max age = %v, want 24h", defaultStepWispJanitorMaxAge)
	}
}

// TestStepWispJanitorEnabledByDefault pins the belt-and-braces contract: the
// janitor runs even in towns whose mayor/daemon.json predates it or omits the
// block entirely. Unlike the opt-in dog patrols, absence means ENABLED —
// leaked step wisps must get cleaned even where wisp_reaper is disabled or
// its dog-dispatch path is broken (hq-oeq9).
func TestStepWispJanitorEnabledByDefault(t *testing.T) {
	tests := []struct {
		name   string
		config *DaemonPatrolConfig
		want   bool
	}{
		{"nil config", nil, true},
		{"nil patrols", &DaemonPatrolConfig{}, true},
		{"janitor block absent", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			WispReaper: &WispReaperConfig{Enabled: true},
		}}, true},
		{"explicitly disabled", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: false},
		}}, false},
		{"explicitly enabled", &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: true},
		}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPatrolEnabled(tt.config, "step_wisp_janitor"); got != tt.want {
				t.Errorf("IsPatrolEnabled(step_wisp_janitor) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStepWispJanitorDatabases(t *testing.T) {
	if got := stepWispJanitorDatabases(nil); got != nil {
		t.Errorf("stepWispJanitorDatabases(nil) = %v, want nil (auto-discover)", got)
	}
	cfg := &DaemonPatrolConfig{Patrols: &PatrolsConfig{
		StepWispJanitor: &StepWispJanitorConfig{Enabled: true, Databases: []string{"hq", "gt"}},
	}}
	got := stepWispJanitorDatabases(cfg)
	if len(got) != 2 || got[0] != "hq" || got[1] != "gt" {
		t.Errorf("stepWispJanitorDatabases() = %v, want [hq gt]", got)
	}
}

// TestRunStepWispJanitorRespectsDisabledPatrol verifies the janitor honors
// both the config toggle and the town-level disabled_patrols list without
// touching Dolt (a disabled janitor must not even attempt DB discovery).
func TestRunStepWispJanitorRespectsDisabledPatrol(t *testing.T) {
	d := &Daemon{
		config: DefaultConfig(t.TempDir()),
		logger: log.New(io.Discard, "", 0),
		patrolConfig: &DaemonPatrolConfig{Patrols: &PatrolsConfig{
			StepWispJanitor: &StepWispJanitorConfig{Enabled: false},
		}},
	}
	// Must return immediately; any Dolt access here would fail loudly on the
	// unreachable default port, but the disabled gate short-circuits first.
	d.runStepWispJanitor()

	d2 := &Daemon{
		config:          DefaultConfig(t.TempDir()),
		logger:          log.New(io.Discard, "", 0),
		patrolConfig:    nil,
		disabledPatrols: map[string]bool{"step_wisp_janitor": true},
	}
	d2.runStepWispJanitor()
}
