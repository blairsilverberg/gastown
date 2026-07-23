package constants

import (
	"testing"
)

func TestRoleEmoji(t *testing.T) {
	tests := []struct {
		role   string
		expect string
	}{
		{RoleMayor, EmojiMayor},
		{RoleDeacon, EmojiDeacon},
		{RoleWitness, EmojiWitness},
		{RoleRefinery, EmojiRefinery},
		{RoleCrew, EmojiCrew},
		{RolePolecat, EmojiPolecat},
		{"unknown", "❓"},
		{"", "❓"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := RoleEmoji(tt.role)
			if got != tt.expect {
				t.Errorf("RoleEmoji(%q) = %q, want %q", tt.role, got, tt.expect)
			}
		})
	}
}

func TestBeadsCustomTypesList(t *testing.T) {
	types := BeadsCustomTypesList()
	expected := []string{"agent", "role", "rig", "convoy", "slot", "queue", "event", "message", "molecule", "gate", "merge-request"}

	if len(types) != len(expected) {
		t.Fatalf("BeadsCustomTypesList() returned %d items, want %d", len(types), len(expected))
	}
	for i, typ := range types {
		if typ != expected[i] {
			t.Errorf("BeadsCustomTypesList()[%d] = %q, want %q", i, typ, expected[i])
		}
	}
}

func TestBeadsInfraTypesList(t *testing.T) {
	types := BeadsInfraTypesList()
	expected := []string{"agent", "role", "message"}

	if len(types) != len(expected) {
		t.Fatalf("BeadsInfraTypesList() returned %d items, want %d", len(types), len(expected))
	}
	for i, typ := range types {
		if typ != expected[i] {
			t.Errorf("BeadsInfraTypesList()[%d] = %q, want %q", i, typ, expected[i])
		}
		if typ == "rig" {
			t.Fatal("rig must stay durable and must not be an infra type")
		}
	}
}

func TestMayorRigsPath(t *testing.T) {
	got := MayorRigsPath("/town")
	expect := "/town/mayor/rigs.json"
	if got != expect {
		t.Errorf("MayorRigsPath = %q, want %q", got, expect)
	}
}

func TestMayorTownPath(t *testing.T) {
	got := MayorTownPath("/town")
	expect := "/town/mayor/town.json"
	if got != expect {
		t.Errorf("MayorTownPath = %q, want %q", got, expect)
	}
}

func TestRigMayorPath(t *testing.T) {
	got := RigMayorPath("/rig")
	expect := "/rig/mayor/rig"
	if got != expect {
		t.Errorf("RigMayorPath = %q, want %q", got, expect)
	}
}

func TestRigBeadsPath(t *testing.T) {
	got := RigBeadsPath("/rig")
	expect := "/rig/mayor/rig/.beads"
	if got != expect {
		t.Errorf("RigBeadsPath = %q, want %q", got, expect)
	}
}

func TestRigPolecatsPath(t *testing.T) {
	got := RigPolecatsPath("/rig")
	expect := "/rig/polecats"
	if got != expect {
		t.Errorf("RigPolecatsPath = %q, want %q", got, expect)
	}
}

func TestRigCrewPath(t *testing.T) {
	got := RigCrewPath("/rig")
	expect := "/rig/crew"
	if got != expect {
		t.Errorf("RigCrewPath = %q, want %q", got, expect)
	}
}

func TestMayorConfigPath(t *testing.T) {
	got := MayorConfigPath("/town")
	expect := "/town/mayor/config.json"
	if got != expect {
		t.Errorf("MayorConfigPath = %q, want %q", got, expect)
	}
}

func TestTownRuntimePath(t *testing.T) {
	got := TownRuntimePath("/town")
	expect := "/town/.runtime"
	if got != expect {
		t.Errorf("TownRuntimePath = %q, want %q", got, expect)
	}
}

func TestRigRuntimePath(t *testing.T) {
	got := RigRuntimePath("/rig")
	expect := "/rig/.runtime"
	if got != expect {
		t.Errorf("RigRuntimePath = %q, want %q", got, expect)
	}
}

func TestRigSettingsPath(t *testing.T) {
	got := RigSettingsPath("/rig")
	expect := "/rig/settings"
	if got != expect {
		t.Errorf("RigSettingsPath = %q, want %q", got, expect)
	}
}

func TestMayorAccountsPath(t *testing.T) {
	got := MayorAccountsPath("/town")
	expect := "/town/mayor/accounts.json"
	if got != expect {
		t.Errorf("MayorAccountsPath = %q, want %q", got, expect)
	}
}

func TestMayorQuotaPath(t *testing.T) {
	got := MayorQuotaPath("/town")
	expect := "/town/mayor/quota.json"
	if got != expect {
		t.Errorf("MayorQuotaPath = %q, want %q", got, expect)
	}
}

func TestIsPatrolFormula(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{MolDeaconPatrol, true},
		{MolWitnessPatrol, true},
		{MolRefineryPatrol, true},
		{"mol-future-patrol-v2", true},
		{"MOL-DEACON-PATROL", true},
		{"mol-polecat-work", false},
		{"mol-dog-reaper", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsPatrolFormula(tt.name); got != tt.want {
			t.Errorf("IsPatrolFormula(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
	// Every registered patrol formula must be recognized — keeps the helper in
	// sync if PatrolFormulas() grows.
	for _, name := range PatrolFormulas() {
		if !IsPatrolFormula(name) {
			t.Errorf("IsPatrolFormula(%q) = false for registered patrol formula", name)
		}
	}
}

func TestIsWispID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"dbt-wfs-7laya", true}, // hq-gk229 incident: witness patrol step
		{"op-wisp-rcs", true},   // molecule root wisp
		{"hq-wisp-abc", true},
		{"gt-abc", false},
		{"op-dat2", false},
		{"cap-8ko", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsWispID(tt.id); got != tt.want {
			t.Errorf("IsWispID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestIsRoleAgentActor(t *testing.T) {
	tests := []struct {
		actor string
		want  bool
	}{
		{"humdbt/witness", true}, // hq-gk229 incident creator
		{"witness", true},
		{"gastown/refinery", true},
		{"refinery", true},
		{"deacon", true},
		{"mayor", true},
		{"openclaw/polecats/furiosa", false},
		{"humdbt/polecats/rust", false},
		{"gastown/crew/max", false},
		{"blair@humcapital.com", false},
		{"", false},
		{"  ", false},
		// Deep paths that merely end in a role name are not role addresses.
		{"a/b/witness", false},
	}
	for _, tt := range tests {
		if got := IsRoleAgentActor(tt.actor); got != tt.want {
			t.Errorf("IsRoleAgentActor(%q) = %v, want %v", tt.actor, got, tt.want)
		}
	}
}

func TestIsRoleOwnedWisp(t *testing.T) {
	tests := []struct {
		name      string
		beadID    string
		createdBy string
		assignee  string
		want      bool
	}{
		// hq-gk229 incident shape: witness patrol step wisp.
		{"witness patrol step", "dbt-wfs-7laya", "humdbt/witness", "", true},
		{"role assignee only", "dbt-wfs-7laya", "", "humdbt/witness", true},
		{"refinery wisp", "gt-wisp-x1", "gastown/refinery", "", true},
		{"deacon wisp", "hq-wisp-x2", "deacon", "", true},
		{"mayor wisp", "hq-wfs-x3", "mayor", "", true},
		// Wisp with no role actor: polecat molecule steps stay untouched.
		{"polecat-assigned wisp", "op-wisp-rv6", "", "openclaw/polecats/furiosa", false},
		{"unattributed wisp", "op-wisp-rv6", "", "", false},
		// Non-wisp beads are never excluded even with role actors.
		{"role-created task", "gt-abc", "gastown/witness", "", false},
		{"mayor-created task", "op-dat2", "mayor", "", false},
	}
	for _, tt := range tests {
		if got := IsRoleOwnedWisp(tt.beadID, tt.createdBy, tt.assignee); got != tt.want {
			t.Errorf("%s: IsRoleOwnedWisp(%q, %q, %q) = %v, want %v",
				tt.name, tt.beadID, tt.createdBy, tt.assignee, got, tt.want)
		}
	}
}
