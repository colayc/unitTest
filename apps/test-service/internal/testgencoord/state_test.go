package testgencoord

import (
	"testing"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

func TestCompletedStageIsTheLastCheckpoint(t *testing.T) {
	for _, state := range []testgendomain.State{testgendomain.StateQueued, testgendomain.StateBaseline, testgendomain.StateAnalyzing, testgendomain.StateSolving, testgendomain.StateRendering, testgendomain.StateValidating, testgendomain.StateMinimizing} {
		next, err := NextStage(state)
		if err != nil || !testgendomain.ValidTransition(state, next) {
			t.Fatalf("NextStage(%s) = %s, %v", state, next, err)
		}
	}
	if _, err := NextStage(testgendomain.StateAccepted); err == nil {
		t.Fatal("terminal stage resumed")
	}
}
