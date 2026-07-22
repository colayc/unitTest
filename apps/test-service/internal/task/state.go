package task

import (
	"errors"
	"fmt"
	"time"
)

type Transition struct {
	From, To                Status
	Outcome                 Outcome
	At                      time.Time
	ErrorCode, ErrorMessage string
}

func ApplyTransition(current Task, change Transition) (Task, error) {
	if current.Status != change.From {
		return Task{}, fmt.Errorf("state conflict: have %s, expected %s", current.Status, change.From)
	}
	allowed := map[Status]map[Status]bool{
		StatusQueued:     {StatusRunning: true, StatusFinished: true},
		StatusRunning:    {StatusCancelling: true, StatusFinished: true},
		StatusCancelling: {StatusFinished: true},
		StatusFinished:   {},
	}
	if !allowed[change.From][change.To] {
		return Task{}, fmt.Errorf("invalid transition %s -> %s", change.From, change.To)
	}
	if change.To == StatusFinished && !validOutcome(change.Outcome) {
		return Task{}, errors.New("finished task requires valid outcome")
	}
	if change.To != StatusFinished && change.Outcome != "" {
		return Task{}, errors.New("nonterminal task cannot have outcome")
	}
	next := current
	next.Status, next.Outcome = change.To, change.Outcome
	next.ErrorCode, next.ErrorMessage = change.ErrorCode, change.ErrorMessage
	if change.To == StatusRunning {
		at := change.At
		next.StartedAt = &at
	}
	if change.To == StatusFinished {
		at := change.At
		next.FinishedAt = &at
	}
	return next, nil
}

func validOutcome(value Outcome) bool {
	switch value {
	case OutcomeSucceeded, OutcomeCommandFailed, OutcomeCancelled, OutcomeTimedOut, OutcomeInterrupted, OutcomeInfrastructureFailed:
		return true
	default:
		return false
	}
}
