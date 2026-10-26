package scheduling_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/domain"
	"aagasa/internal/scheduling"
)

func configWith(leadTime, preBuffer, postBuffer time.Duration) domain.SchedulingConfig {
	return domain.SchedulingConfig{
		MinimumLeadTime: leadTime,
		PrePassBuffer:   preBuffer,
		PostPassBuffer:  postBuffer,
	}
}


func TestLifecycleTransitions(t *testing.T) {
	allowed := []struct{ from, to domain.PassStatus }{
		{domain.PassPendingApproval, domain.PassApproved},
		{domain.PassPendingApproval, domain.PassRejected},
		{domain.PassPendingApproval, domain.PassCancelled},
		{domain.PassApproved, domain.PassCancelled},
		{domain.PassApproved, domain.PassExecuting},
		{domain.PassApproved, domain.PassMissed},
		{domain.PassExecuting, domain.PassCompleted},
		{domain.PassExecuting, domain.PassFailed},
	}
	for _, transition := range allowed {
		if err := scheduling.ValidateTransition(transition.from, transition.to); err != nil {
			t.Errorf("%s -> %s should be allowed: %v", transition.from, transition.to, err)
		}
	}
}

func TestTerminalStatesCannotChange(t *testing.T) {
	terminal := []domain.PassStatus{
		domain.PassRejected, domain.PassCancelled,
		domain.PassCompleted, domain.PassFailed, domain.PassMissed,
	}
	for _, from := range terminal {
		if !scheduling.IsTerminal(from) {
			t.Errorf("%s should be terminal", from)
		}
		for _, to := range []domain.PassStatus{
			domain.PassApproved, domain.PassExecuting, domain.PassPendingApproval,
		} {
			if err := scheduling.ValidateTransition(from, to); err == nil {
				t.Errorf("%s -> %s must be refused", from, to)
			}
		}
	}
}

func TestNonsenseTransitionsAreRefused(t *testing.T) {
	refused := []struct{ from, to domain.PassStatus }{
		{domain.PassPendingApproval, domain.PassExecuting},
		{domain.PassPendingApproval, domain.PassCompleted},
		{domain.PassApproved, domain.PassPendingApproval},
		{domain.PassExecuting, domain.PassApproved},
		{domain.PassApproved, domain.PassCompleted},
		{domain.PassApproved, domain.PassApproved},
	}
	for _, transition := range refused {
		if err := scheduling.ValidateTransition(transition.from, transition.to); err == nil {
			t.Errorf("%s -> %s must be refused", transition.from, transition.to)
		}
	}
	if err := scheduling.ValidateTransition("nonsense", domain.PassApproved); err == nil {
		t.Error("an unknown status must be refused")
	}
}

func TestLiveStatusesMatchTheDatabaseConstraint(t *testing.T) {
	live := map[domain.PassStatus]bool{
		domain.PassPendingApproval: true,
		domain.PassApproved:        true,
		domain.PassExecuting:       true,
	}
	all := []domain.PassStatus{
		domain.PassPendingApproval, domain.PassApproved, domain.PassRejected,
		domain.PassCancelled, domain.PassExecuting, domain.PassCompleted,
		domain.PassFailed, domain.PassMissed,
	}
	for _, status := range all {
		if scheduling.HoldsStationResource(status) != live[status] {
			t.Errorf("%s: HoldsStationResource = %v, want %v",
				status, scheduling.HoldsStationResource(status), live[status])
		}
	}
}


func TestReservationAppliesBuffers(t *testing.T) {
	aos := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	los := aos.Add(15 * time.Minute)
	config := configWith(0, 2*time.Minute, 3*time.Minute)

	reservation := scheduling.ReservationFor(aos, los, config)

	if !reservation.From.Equal(aos.Add(-2 * time.Minute)) {
		t.Errorf("From = %s, want AOS minus the pre-pass buffer", reservation.From)
	}
	if !reservation.To.Equal(los.Add(3 * time.Minute)) {
		t.Errorf("To = %s, want LOS plus the post-pass buffer", reservation.To)
	}
}

func TestOverlapMatchesTheSpecExample(t *testing.T) {
	base := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	passA := scheduling.Reservation{From: base, To: base.Add(15 * time.Minute)}
	passB := scheduling.Reservation{From: base.Add(10 * time.Minute), To: base.Add(20 * time.Minute)}

	if !passA.Overlaps(passB) || !passB.Overlaps(passA) {
		t.Error("10:00-10:15 and 10:10-10:20 must conflict")
	}
}

func TestTouchingReservationsDoNotOverlap(t *testing.T) {
	base := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	first := scheduling.Reservation{From: base, To: base.Add(15 * time.Minute)}
	second := scheduling.Reservation{From: base.Add(15 * time.Minute), To: base.Add(30 * time.Minute)}

	if first.Overlaps(second) || second.Overlaps(first) {
		t.Error("reservations that merely touch must not conflict")
	}
}

func TestContainedReservationOverlaps(t *testing.T) {
	base := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	outer := scheduling.Reservation{From: base, To: base.Add(time.Hour)}
	inner := scheduling.Reservation{From: base.Add(10 * time.Minute), To: base.Add(20 * time.Minute)}

	if !outer.Overlaps(inner) || !inner.Overlaps(outer) {
		t.Error("a fully contained reservation must conflict")
	}
}


func TestLeadTimeRejectsPassesStartingTooSoon(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	config := configWith(30*time.Minute, 0, 0)

	if err := scheduling.CheckLeadTime(now, now.Add(29*time.Minute), config); !errors.Is(err, scheduling.ErrLeadTime) {
		t.Errorf("29 minutes ahead should be refused, got %v", err)
	}
	if err := scheduling.CheckLeadTime(now, now.Add(31*time.Minute), config); err != nil {
		t.Errorf("31 minutes ahead should be allowed, got %v", err)
	}
	if err := scheduling.CheckLeadTime(now, now.Add(30*time.Minute), config); err != nil {
		t.Errorf("exactly the lead time should be allowed, got %v", err)
	}
}

func TestLeadTimeRejectsPassesInThePast(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	config := configWith(30*time.Minute, 0, 0)

	if err := scheduling.CheckLeadTime(now, now.Add(-time.Hour), config); !errors.Is(err, scheduling.ErrLeadTime) {
		t.Error("a pass in the past must be refused")
	}
}

func TestShorteningTheLeadTimeCannotBeUsedImmediately(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	current := 30 * time.Minute
	shortened := 5 * time.Minute

	effectiveFrom := scheduling.EffectiveFromForNewConfig(now, current, shortened)

	if !effectiveFrom.Equal(now.Add(shortened)) {
		t.Errorf("effectiveFrom = %s, want now plus the new lead time", effectiveFrom)
	}
	if !effectiveFrom.After(now) {
		t.Error("a new configuration must not be usable immediately")
	}
}

func TestLengtheningTheLeadTimeAppliesImmediately(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	current := time.Minute
	lengthened := 6 * time.Hour

	effectiveFrom := scheduling.EffectiveFromForNewConfig(now, current, lengthened)

	if !effectiveFrom.Equal(now) {
		t.Errorf("effectiveFrom = %s, want immediately", effectiveFrom)
	}
}

func TestAnUnchangedLeadTimeAppliesImmediately(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	same := 30 * time.Minute

	if got := scheduling.EffectiveFromForNewConfig(now, same, same); !got.Equal(now) {
		t.Errorf("effectiveFrom = %s, want immediately", got)
	}
}


func TestBandMismatchProducesAWarningNotAnError(t *testing.T) {
	warning := scheduling.BandMismatchWarning(domain.BandVHF, domain.BandUHF)
	if warning == nil {
		t.Fatal("a mismatch must produce a warning")
	}
	if warning.Code != "band_mismatch" {
		t.Errorf("Code = %q", warning.Code)
	}
	if !strings.Contains(warning.Message, "UHF") || !strings.Contains(warning.Message, "VHF") {
		t.Errorf("Message does not name both bands: %q", warning.Message)
	}
}

func TestMatchingBandProducesNoWarning(t *testing.T) {
	if warning := scheduling.BandMismatchWarning(domain.BandVHF, domain.BandVHF); warning != nil {
		t.Errorf("unexpected warning: %+v", warning)
	}
}


func TestPrivatePassVisibility(t *testing.T) {
	owner := domain.User{ID: uuid.New(), Role: domain.RoleUser}
	stranger := domain.User{ID: uuid.New(), Role: domain.RoleUser}
	admin := domain.User{ID: uuid.New(), Role: domain.RoleAdmin}
	root := domain.User{ID: uuid.New(), Role: domain.RoleRoot}

	private := domain.Pass{RequestedBy: owner.ID, Visibility: domain.VisibilityPrivate}

	if !scheduling.CanViewPass(owner, private) {
		t.Error("the owner cannot see their own private pass")
	}
	if scheduling.CanViewPass(stranger, private) {
		t.Error("an unrelated user can see a private pass")
	}
	if !scheduling.CanViewPass(admin, private) || !scheduling.CanViewPass(root, private) {
		t.Error("admin and root must see private passes")
	}
}

func TestPublicPassIsVisibleToEveryone(t *testing.T) {
	owner := domain.User{ID: uuid.New(), Role: domain.RoleUser}
	stranger := domain.User{ID: uuid.New(), Role: domain.RoleUser}
	public := domain.Pass{RequestedBy: owner.ID, Visibility: domain.VisibilityPublic}

	if !scheduling.CanViewPass(stranger, public) {
		t.Error("a public pass must be visible to any authenticated user")
	}
}

func TestCancellationPermissionsAndReasons(t *testing.T) {
	owner := domain.User{ID: uuid.New(), Role: domain.RoleUser}
	stranger := domain.User{ID: uuid.New(), Role: domain.RoleUser}
	admin := domain.User{ID: uuid.New(), Role: domain.RoleAdmin}
	pass := domain.Pass{RequestedBy: owner.ID}

	if !scheduling.CanCancelPass(owner, pass) {
		t.Error("the owner must be able to cancel their pass")
	}
	if scheduling.CanCancelPass(stranger, pass) {
		t.Error("an unrelated user must not cancel someone else's pass")
	}
	if !scheduling.CanCancelPass(admin, pass) {
		t.Error("an admin must be able to cancel a pass")
	}

	if reason := scheduling.CancellationReasonFor(owner, pass); reason != domain.CancelledByOwner {
		t.Errorf("owner cancellation reason = %s", reason)
	}
	if reason := scheduling.CancellationReasonFor(admin, pass); reason != domain.CancelledByAdmin {
		t.Errorf("admin cancellation reason = %s", reason)
	}
}
