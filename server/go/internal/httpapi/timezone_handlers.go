package httpapi

import (
	"errors"
	"net/http"
	"time"

	"aagasa/internal/timezone"
)

type timezoneListResponse struct {
	Zones []timezone.Zone `json:"zones"`
}

// handleListTimezones offers the IANA zones the station can be set to, read
// from the system tz database so they follow the host's tzdata updates.
func (s *Server) handleListTimezones(w http.ResponseWriter, r *http.Request) {
	zones, err := timezone.List(timezone.Directory(), time.Now().UTC())
	if err != nil {
		if errors.Is(err, timezone.ErrNoZoneList) {
			s.logger.Warn("time zone list unavailable", "error", err.Error())
			writeError(s.logger, w, http.StatusServiceUnavailable, "timezones_unavailable",
				"the server has no time zone list; enter an IANA name such as Asia/Kolkata")
			return
		}
		s.internalError(w, "list time zones", err)
		return
	}
	writeJSON(s.logger, w, http.StatusOK, timezoneListResponse{Zones: zones})
}
