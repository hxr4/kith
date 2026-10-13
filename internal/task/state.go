// Package task holds the task spec, the state machine, globbing and the
// process executor.
package task

import "fmt"

const (
	Submitted    = "SUBMITTED"
	Accepted     = "ACCEPTED"
	Rejected     = "REJECTED"
	Transferring = "TRANSFERRING"
	Running      = "RUNNING"
	Finalizing   = "FINALIZING"
	Cancelling   = "CANCELLING"
	Interrupted  = "INTERRUPTED"
	Completed    = "COMPLETED"
	Failed       = "FAILED"
	Cancelled    = "CANCELLED"
)

// MaxAttempts caps INTERRUPTED → RUNNING re-runs (§9.2).
const MaxAttempts = 3

// edges is the complete transition table (§9.2). No other edges exist.
var edges = map[string][]string{
	Submitted:    {Accepted, Rejected},
	Accepted:     {Transferring},
	Transferring: {Running, Failed},
	Running:      {Finalizing, Cancelling, Failed, Interrupted},
	Finalizing:   {Completed, Failed, Interrupted},
	Cancelling:   {Cancelled, Failed},
	Interrupted:  {Running, Failed},
}

// CanTransition reports whether from → to is a legal edge.
func CanTransition(from, to string) bool {
	for _, s := range edges[from] {
		if s == to {
			return true
		}
	}
	return false
}

// CheckTransition returns an error for an illegal edge.
func CheckTransition(from, to string) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("illegal transition %s → %s", from, to)
	}
	return nil
}

func IsTerminal(s string) bool {
	return s == Completed || s == Failed || s == Cancelled || s == Rejected
}

// NonTerminal lists every non-terminal state.
var NonTerminal = []string{Submitted, Accepted, Transferring, Running, Finalizing, Cancelling, Interrupted}

// Active states occupy the executor (one at a time in v1; pending approval excluded).
var Active = []string{Accepted, Transferring, Running, Finalizing, Cancelling, Interrupted}

// AllStates for validation.
var AllStates = []string{Submitted, Accepted, Rejected, Transferring, Running, Finalizing, Cancelling, Interrupted, Completed, Failed, Cancelled}
