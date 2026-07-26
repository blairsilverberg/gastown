package beads

// op-uhd2: agent_state=holding semantics — an intentional park awaiting
// approval. It must protect the session from cleanup/reaping and must not
// read as actively-working.

import "testing"

func TestAgentStateHolding_ProtectsFromCleanup(t *testing.T) {
	if !AgentStateHolding.ProtectsFromCleanup() {
		t.Fatal("holding must protect from cleanup — the idle reaper killed/reset holding sessions in hq-khtga")
	}
}

func TestAgentStateHolding_NotActive(t *testing.T) {
	if AgentStateHolding.IsActive() {
		t.Fatal("holding is a park, not active work")
	}
}
