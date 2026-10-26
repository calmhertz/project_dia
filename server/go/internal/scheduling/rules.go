package scheduling

import (
	"errors"
	"fmt"
	"time"

	"aagasa/internal/domain"
)

var (
	ErrLeadTime = errors.New("pass starts inside the minimum scheduling lead time")
	ErrStationConflict = errors.New("the station is already reserved for that period")
	ErrWorkerOffline = errors.New("the station worker is offline")
	ErrNotSchedulable = errors.New("this satellite is not available for scheduling")

	ErrPipelineRequired = errors.New("this recording mode needs a SatDump pipeline")
	ErrPassNotFound = errors.New("no predicted pass matches the requested time")
	ErrForbidden = errors.New("not permitted")
)

type Reservation struct {
	From time.Time
	To   time.Time
}

func ReservationFor(aos, los time.Time, config domain.SchedulingConfig) Reservation {
	return Reservation{
		From: aos.Add(-config.PrePassBuffer),
		To:   los.Add(config.PostPassBuffer),
	}
}

func (r Reservation) Overlaps(other Reservation) bool {
	return r.From.Before(other.To) && other.From.Before(r.To)
}

func CheckLeadTime(now, aos time.Time, config domain.SchedulingConfig) error {
	earliest := now.Add(config.MinimumLeadTime)
	if aos.Before(earliest) {
		return fmt.Errorf("%w: the earliest bookable start is %s",
			ErrLeadTime, earliest.UTC().Format(time.RFC3339))
	}
	return nil
}

func EffectiveFromForNewConfig(now time.Time, currentLeadTime, newLeadTime time.Duration) time.Time {
	if newLeadTime < currentLeadTime {
		return now.Add(newLeadTime)
	}
	return now
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const warningBandMismatch = "band_mismatch"

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

func CanViewPass(actor domain.User, pass domain.Pass) bool {
	if pass.Visibility == domain.VisibilityPublic {
		return true
	}
	if pass.RequestedBy == actor.ID {
		return true
	}
	return actor.Role == domain.RoleAdmin || actor.Role == domain.RoleRoot
}

func CanCancelPass(actor domain.User, pass domain.Pass) bool {
	if pass.RequestedBy == actor.ID {
		return true
	}
	return actor.Role == domain.RoleAdmin || actor.Role == domain.RoleRoot
}

func CancellationReasonFor(actor domain.User, pass domain.Pass) domain.CancellationReason {
	if pass.RequestedBy == actor.ID {
		return domain.CancelledByOwner
	}
	return domain.CancelledByAdmin
}
