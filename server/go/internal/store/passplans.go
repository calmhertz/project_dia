package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StoredPassPlan is a generated plan as persisted.
type StoredPassPlan struct {
	PassID      uuid.UUID
	Generation  string
	PlanVersion int
	Encoded     []byte
	GeneratedAt time.Time
}

// UpsertPassPlan stores or replaces the plan for a pass.
//
// Regenerating an unchanged plan must not churn the row, so an identical
// generation is left alone and reports false.
func (r *Repository) UpsertPassPlan(ctx context.Context, plan StoredPassPlan) (bool, error) {
	var changed bool
	err := r.db.QueryRow(ctx, `
		INSERT INTO pass_plans (pass_id, generation, plan_version, encoded, generated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (pass_id) DO UPDATE SET
			generation   = EXCLUDED.generation,
			plan_version = EXCLUDED.plan_version,
			encoded      = EXCLUDED.encoded,
			generated_at = EXCLUDED.generated_at
		WHERE pass_plans.generation <> EXCLUDED.generation
		RETURNING true`,
		plan.PassID, plan.Generation, plan.PlanVersion, plan.Encoded, plan.GeneratedAt,
	).Scan(&changed)
	if err != nil {
		// No row returned means the conflicting row was identical.
		if isNoRows(err) {
			return false, nil
		}
		return false, translate(err)
	}
	return changed, nil
}

// GetPassPlan returns the stored plan for a pass.
func (r *Repository) GetPassPlan(ctx context.Context, passID uuid.UUID) (StoredPassPlan, error) {
	var plan StoredPassPlan
	err := r.db.QueryRow(ctx, `
		SELECT pass_id, generation, plan_version, encoded, generated_at
		FROM pass_plans WHERE pass_id = $1`, passID).Scan(
		&plan.PassID, &plan.Generation, &plan.PlanVersion, &plan.Encoded, &plan.GeneratedAt)
	return plan, translate(err)
}

// ListPassPlansForStation returns stored plans for a station's future passes,
// soonest first. This is the basis of the Worker's desired state.
func (r *Repository) ListPassPlansForStation(ctx context.Context, stationID uuid.UUID,
	notBefore time.Time) ([]StoredPassPlan, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.pass_id, p.generation, p.plan_version, p.encoded, p.generated_at
		FROM pass_plans p
		JOIN passes ON passes.id = p.pass_id
		WHERE passes.station_id = $1
		  AND passes.status = 'approved'
		  AND passes.los_at >= $2
		ORDER BY passes.aos_at`, stationID, notBefore)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var plans []StoredPassPlan
	for rows.Next() {
		var plan StoredPassPlan
		if err := rows.Scan(&plan.PassID, &plan.Generation, &plan.PlanVersion,
			&plan.Encoded, &plan.GeneratedAt); err != nil {
			return nil, translate(err)
		}
		plans = append(plans, plan)
	}
	return plans, translate(rows.Err())
}

// DeletePassPlan removes a plan, used when a pass stops being executable.
func (r *Repository) DeletePassPlan(ctx context.Context, passID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM pass_plans WHERE pass_id = $1`, passID)
	return translate(err)
}
