package core

import (
	"github.com/tripledoublev/v100/internal/providers"
)

// Checkpoint represents a snapshot of the agent state.
type Checkpoint struct {
	Messages  []providers.Message
	StepCount int
}

// Checkpoint captures the current state of the loop.
func (l *Loop) Checkpoint() Checkpoint {
	msgs := make([]providers.Message, len(l.Messages))
	copy(msgs, l.Messages)
	return Checkpoint{
		Messages:  msgs,
		StepCount: l.stepCount,
	}
}

// Restore resets the loop state to a previous checkpoint.
func (l *Loop) Restore(cp Checkpoint) {
	l.Messages = make([]providers.Message, len(cp.Messages))
	copy(l.Messages, cp.Messages)
	l.stepCount = cp.StepCount
}
