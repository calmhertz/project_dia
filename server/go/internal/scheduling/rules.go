package scheduling

import (
	"errors"
	"fmt"
	"time"

	"aagasa/internal/domain"
)

var (
	// ErrLeadTime means the pass starts too soon (spec.md section 13.6).
	ErrLeadTime = errors.New("pass starts inside the minimum scheduling lead time")
	// ErrStationConflict means the reservation overlaps a live pass.
	ErrStationConflict = errors.New("the station is already reserved for that period")
	// ErrWorkerOffline means the station cannot accept new work.
	ErrWorkerOffline = errors.New("the station worker is offline")
	// ErrNotSchedulable means the satellite is not open for scheduling.
	ErrNotSchedulable = errors.New("this satellite is not available for scheduling")

	// ErrPipelineRequired means a pass asked for a decoded result without
	// saying how to decode it. Without a pipeline the Worker would fall back
	// to a raw capture, quietly producing something other than what was
	// asked for (spec.md section 19.1).
	ErrPipelineRequired = errors.New("this recording mode needs a SatDump pipeline")
	// ErrPassNotFound means no predicted pass matches the request.
	ErrPassNotFound = errors.New("no predicted pass matches the requested time")
	// ErrForbidden means the actor may not perform this action.
	ErrForbidden = errors.New("not permitted")
)

// Reservation is the station resource window a pass occupies.
//
// Buffers widen the pass into a reservation (spec.md section 13.4). They are
// resource-protection margins and are distinct from recording margins, which
// control when capture starts and stops.
type Reservation struct {
	From time.Time
	To   time.Time
}

// ReservationFor widens a pass window by the configured buffers.
func ReservationFor(aos, los time.Time, config domain.SchedulingConfig) Reservation {
	return Reservation{
		From: aos.Add(-config.PrePassBuffer),
		To:   los.Add(config.PostPassBuffer),
	}
}

// Overlaps reports whether two reservations collide.
//
// Half-open: a reservation ending exactly when another begins does not
// conflict. This must agree with the database's tstzrange '[)' bound.
func (r Reservation) Overlaps(other Reservation) bool {
	return r.From.Before(other.To) && other.From.Before(r.To)
}

// CheckLeadTime enforces the minimum scheduling lead time.
//
// spec.md section 13.6 and global test property 4: the rule applies to every
// role, Root included. There is no override.
func CheckLeadTime(now, aos time.Time, config domain.SchedulingConfig) error {
	earliest := now.Add(config.MinimumLeadTime)
	if aos.Before(earliest) {
		return fmt.Errorf("%w: the earliest bookable start is %s",
			ErrLeadTime, earliest.UTC().Format(time.RFC3339))
	}
	return nil
}

// EffectiveFromForNewConfig returns when a newly saved scheduling
// configuration may start being used.
//
// spec.md section 13.6 exists to stop a new lead time becoming an immediate
// bypass of the rule: shortening it must not let anyone book sooner than the
// old rule allowed, so a shorter value waits out the newly chosen period
// before it applies.
//
// A lead time that is the same or longer is a tightening. It cannot be a
// bypass of anything, so it applies at once. Delaying it would serve no
// purpose and does real harm: raising the lead time to six hours would leave
// the old, looser rule in force for six hours, and an operator repeating the
// change would never see it take effect at all.
func EffectiveFromForNewConfig(now time.Time, currentLeadTime, newLeadTime time.Duration) time.Time {
	if newLeadTime < currentLeadTime {
		return now.Add(newLeadTime)
	}
	return now
}

// Warning is advisory information attached to a scheduling result. A warning
// never blocks a request.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const warningBandMismatch = "band_mismatch"

// BandMismatchWarning reports a pass needing the band the station is not
// currently configured for.
//
// spec.md section 8 is explicit that this is a warning, not a conflict: the
// pass may still be scheduled and the operator may switch the station before
// it runs.
func BandMismatchWarning(passBand, stationBand domain.RFBand) *Warning {
	if passBand == stationBand {
		return nil
	}
	return &Warning{
		Code: warningBandMismatch,
		Message: fmt.Sprintf(
			"The station is currently configured for %s, while this pass requires %s. "+
				"Reception may be poor unless the station configuration is changed before execution.",
			upper(stationBand), upper(passBand)),
	}
}

func upper(band domain.RFBand) string {
	switch band {
	case domain.BandVHF:
		return "VHF"
	case domain.BandUHF:
		return "UHF"
	default:
		return string(band)
	}
}

// CanViewPass reports whether an actor may see a pass in full.
//
// spec.md section 16: a private pass is visible to its owner, Admins and
// Root. A public pass is visible to any authenticated user.
func CanViewPass(actor domain.User, pass domain.Pass) bool {
	if pass.Visibility == domain.VisibilityPublic {
		return true
	}
	if pass.RequestedBy == actor.ID {
		return true
	}
	return actor.Role == domain.RoleAdmin || actor.Role == domain.RoleRoot
}

// CanCancelPass reports whether an actor may cancel a pass.
func CanCancelPass(actor domain.User, pass domain.Pass) bool {
	if pass.RequestedBy == actor.ID {
		return true
	}
	return actor.Role == domain.RoleAdmin || actor.Role == domain.RoleRoot
}

// CancellationReasonFor picks the reason recorded against a cancellation.
func CancellationReasonFor(actor domain.User, pass domain.Pass) domain.CancellationReason {
	if pass.RequestedBy == actor.ID {
		return domain.CancelledByOwner
	}
	return domain.CancelledByAdmin
}
