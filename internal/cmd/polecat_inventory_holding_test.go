package cmd

// op-uhd2: inventory/capacity classification of a HOLDING polecat. The
// dispatcher chose basalt because nothing represented "holding" — the slot
// read as idle/reusable. A holding polecat must surface as StateHolding and
// never as a reusable disposition.

import (
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/polecat"
)

func TestBuildPolecatInventoryItem_HoldingNeverIdle(t *testing.T) {
	fields := &beads.AgentFields{AgentState: string(beads.AgentStateHolding)}

	item := buildPolecatInventoryItemFromEvidence("capital", "basalt", fields,
		polecatActiveWorkEvidence{}, polecatSessionSet{})
	if item.State != polecat.StateHolding {
		t.Fatalf("holding polecat inventory state = %q, want %q (rendering as idle fed the dispatcher a phantom-idle slot)",
			item.State, polecat.StateHolding)
	}
	if item.Disposition.Reusable {
		t.Fatal("holding polecat must not be reusable")
	}
	if !item.Disposition.CountsTowardCapacity {
		t.Error("holding polecat occupies its slot and must count toward capacity")
	}
}
