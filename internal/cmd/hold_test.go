package cmd

// op-uhd2 / hq-khtga incident-derived tests for the session HOLD gate.
//
// Incident shape (4x on 2026-07-25): a polecat holding work for human
// approval (uncommitted test change, or interactive with Blair in-pane)
// exited COMPLETED through gt done — closing its bead, firing POLECAT_DONE,
// and feeding the dispatcher a phantom-idle slot. The HOLD gate must refuse
// every gt done exit path while holding, without touching the hold unless
// explicitly overridden by approver authorization.

import (
	"strings"
	"testing"
)

func TestEnforceSessionHold_HoldingRefusesEveryExitType(t *testing.T) {
	for _, exitType := range []string{ExitCompleted, ExitDeferred, ExitEscalated} {
		t.Run(exitType, func(t *testing.T) {
			cleared := false
			err := enforceSessionHold(true, false, exitType, func() error {
				cleared = true
				return nil
			})
			if err == nil {
				t.Fatalf("holding polecat must refuse gt done (%s exit) — phantom POLECAT_DONE is the hq-khtga incident", exitType)
			}
			if !strings.Contains(err.Error(), "HOLD") {
				t.Errorf("refusal should identify the HOLD: %v", err)
			}
			if !strings.Contains(err.Error(), "gt approval clear") {
				t.Errorf("refusal should name the release path: %v", err)
			}
			if cleared {
				t.Error("a refused gt done must NOT clear the holding state")
			}
		})
	}
}

func TestEnforceSessionHold_OverrideClearsHoldAndProceeds(t *testing.T) {
	cleared := false
	err := enforceSessionHold(true, true, ExitCompleted, func() error {
		cleared = true
		return nil
	})
	if err != nil {
		t.Fatalf("--override-approval-hold must proceed past the session hold: %v", err)
	}
	if !cleared {
		t.Error("override must clear the holding state so the exit is consistent")
	}
}

func TestEnforceSessionHold_NotHoldingIsNoop(t *testing.T) {
	err := enforceSessionHold(false, false, ExitCompleted, func() error {
		t.Fatal("clearHolding must not be called when not holding")
		return nil
	})
	if err != nil {
		t.Fatalf("non-holding polecat must pass the gate: %v", err)
	}
}

func TestAgentStateIsHolding_FailsOpenOnEmptyBeadID(t *testing.T) {
	// A missing agent bead id must not wedge every gt done exit path.
	if agentStateIsHolding(nil, "") {
		t.Fatal("empty agent bead id must fail open (not holding)")
	}
}
