// Package scheduling is Aagasa's authoritative scheduler.
//
// It is the only place a pass is created, approved or cancelled. The Worker
// executes what this package decides and never schedules for itself
// (spec.md section 2.2).
package scheduling

import (
	"errors"
	"fmt"

	"aagasa/internal/domain"
)

// allowedTransitions is the externally meaningful pass lifecycle from
// spec.md section 14. Anything absent here is refused.
var allowedTransitions = map[domain.PassStatus][]domain.PassStatus{
	domain.PassPendingApproval: {
		domain.PassApproved,
		domain.PassRejected,
		domain.PassCancelled,
	},
	domain.PassApproved: {
		domain.PassCancelled,
		domain.PassExecuting,
		// The window passed without the Worker executing it.
		domain.PassMissed,
	},
	domain.PassExecuting: {
		domain.PassCompleted,
		domain.PassFailed,
	},
	// Terminal states. A completed or missed pass is never replayed
	// (spec.md sections 6.4 and 20).
	domain.PassRejected:  {},
	domain.PassCancelled: {},
	domain.PassCompleted: {},
	domain.PassFailed:    {},
	domain.PassMissed:    {},
}

// liveStatuses hold the station resource. They must match the WHERE clause of
// the passes_no_station_overlap constraint, or the database and the
// application would disagree about what conflicts.
var liveStatuses = []domain.PassStatus{
	domain.PassPendingApproval,
	domain.PassApproved,
	domain.PassExecuting,
}

// IsTerminal reports whether a pass can no longer change state.
func IsTerminal(status domain.PassStatus) bool {
	next, known := allowedTransitions[status]
	return known && len(next) == 0
}

// HoldsStationResource reports whether a pass in this state occupies the
// station and therefore conflicts with an overlapping request.
func HoldsStationResource(status domain.PassStatus) bool {
	for _, live := range liveStatuses {
		if status == live {
			return true
		}
	}
	return false
}

// LiveStatuses returns the states that hold the station resource.
func LiveStatuses() []domain.PassStatus {
	out := make([]domain.PassStatus, len(liveStatuses))
	copy(out, liveStatuses)
	return out
}

// CanTransition reports whether a state change is allowed.
func CanTransition(from, to domain.PassStatus) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ValidateTransition returns an error describing why a change is refused.
// ErrInvalidTransition means the pass is not in a state where the requested
// change makes sense, usually because someone else acted first.
var ErrInvalidTransition = errors.New("invalid pass state change")

func ValidateTransition(from, to domain.PassStatus) error {
	if _, known := allowedTransitions[from]; !known {
		return fmt.Errorf("%w: unknown pass status %q", ErrInvalidTransition, from)
	}
	if _, known := allowedTransitions[to]; !known {
		return fmt.Errorf("%w: unknown target status %q", ErrInvalidTransition, to)
	}
	if from == to {
		return fmt.Errorf("%w: this pass is already %s", ErrInvalidTransition, from)
	}
	if IsTerminal(from) {
		return fmt.Errorf("%w: this pass is %s and cannot change state", ErrInvalidTransition, from)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: a %s pass cannot become %s", ErrInvalidTransition, from, to)
	}
	return nil
}
