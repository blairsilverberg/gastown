package polecat

// op-uhd2: a HOLDING polecat (parked awaiting human approval) must never be
// classified reusable, safe-to-nuke, or needing recovery — it is an occupied,
// intentional park. The 2026-07-25 incident chain started with a holding
// session being treated as a reusable idle slot.

import "testing"

func TestDecideWorkstate_HoldingNeverReusable(t *testing.T) {
	d := DecideWorkstate(WorkstateInput{State: StateHolding})
	if d.Reusable {
		t.Fatal("holding polecat must not be reusable — destructive reuse of a holding session is the hq-khtga material-damage incident")
	}
	if d.SafeToNuke {
		t.Error("holding polecat must not be safe to nuke")
	}
	if d.NeedsRecovery {
		t.Error("holding is an intentional park, not a recovery case")
	}
	if !d.CountsTowardCapacity {
		t.Error("holding polecat occupies its slot and must count toward capacity")
	}
	if d.Verdict != WorkstateVerdictHolding {
		t.Errorf("verdict = %q, want %q", d.Verdict, WorkstateVerdictHolding)
	}
}

func TestDecideSlotReuse_HoldingRefused(t *testing.T) {
	d := DecideSlotReuse(SlotReuseInput{State: StateHolding})
	if d.Reusable {
		t.Fatal("DecideSlotReuse must refuse a holding polecat")
	}
}
