package scheduling

import (
	"errors"
	"fmt"

	"aagasa/internal/domain"
)

var allowedTransitions = map[domain.PassStatus][]domain.PassStatus{
	domain.PassPendingApproval: {
		domain.PassApproved,
		domain.PassRejected,
		domain.PassCancelled,
	},
	domain.PassApproved: {
		domain.PassCancelled,
		domain.PassExecuting,
		domain.PassMissed,
	},
	domain.PassExecuting: {
		domain.PassCompleted,
		domain.PassFailed,
	},
	domain.PassRejected:  {},
	domain.PassCancelled: {},
	domain.PassCompleted: {},
	domain.PassFailed:    {},
	domain.PassMissed:    {},
}

var liveStatuses = []domain.PassStatus{
	domain.PassPendingApproval,
	domain.PassApproved,
	domain.PassExecuting,
}

func IsTerminal(status domain.PassStatus) bool {
	next, known := allowedTransitions[status]
	return known && len(next) == 0
}

func HoldsStationResource(status domain.PassStatus) bool {
	for _, live := range liveStatuses {
		if status == live {
			return true
		}
	}
	return false
}

func LiveStatuses() []domain.PassStatus {
	out := make([]domain.PassStatus, len(liveStatuses))
	copy(out, liveStatuses)
	return out
}

func CanTransition(from, to domain.PassStatus) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

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
