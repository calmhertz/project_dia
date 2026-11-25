package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/domain"
)

// Who a notification goes to. The actor who caused an event is never
// notified about it.
type audience int

const (
	toPassOwner audience = 1 << iota
	toAdmins
	toEntityUser
)

type notificationRule struct {
	audience audience
	severity string
	title    string
	body     func(ctx notificationContext) string
}

type notificationContext struct {
	record    domain.AuditRecord
	satellite string
	aos       time.Time
	ownerID   *uuid.UUID
	status    string
}

func (c notificationContext) passLabel() string {
	if c.satellite == "" {
		return "A pass"
	}
	if c.aos.IsZero() {
		return c.satellite
	}
	return fmt.Sprintf("%s at %s", c.satellite, c.aos.UTC().Format("Jan 2 15:04 UTC"))
}

func (c notificationContext) detail() string {
	if detail, ok := c.record.NewState["detail"].(string); ok && detail != "" {
		return ": " + detail
	}
	return ""
}

var notificationRules = map[string]notificationRule{
	"pass.requested": {
		audience: toAdmins, severity: "info", title: "Pass awaiting approval",
		body: func(c notificationContext) string { return c.passLabel() + " needs a decision." },
	},
	"pass.approved": {
		audience: toPassOwner, severity: "success", title: "Pass approved",
		body: func(c notificationContext) string { return c.passLabel() + " was approved." },
	},
	"pass.rejection_overruled": {
		audience: toPassOwner, severity: "success", title: "Pass approved",
		body: func(c notificationContext) string {
			return c.passLabel() + " was approved after an earlier rejection."
		},
	},
	"pass.rejected": {
		audience: toPassOwner, severity: "warning", title: "Pass rejected",
		body: func(c notificationContext) string { return c.passLabel() + " was rejected." },
	},
	"pass.cancelled": {
		audience: toPassOwner, severity: "warning", title: "Pass cancelled",
		body: func(c notificationContext) string { return c.passLabel() + " was cancelled." },
	},
	"pass.cancelled_by_root_override": {
		audience: toPassOwner, severity: "warning", title: "Pass cancelled",
		body: func(c notificationContext) string {
			return c.passLabel() + " was cancelled to make room for a root override."
		},
	},
	"pass.executing": {
		audience: toPassOwner, severity: "info", title: "Recording started",
		body: func(c notificationContext) string { return c.passLabel() + " is being recorded." },
	},
	"pass.completed": {
		audience: toPassOwner, severity: "success", title: "Pass completed",
		body: func(c notificationContext) string {
			return c.passLabel() + " finished recording."
		},
	},
	"pass.failed": {
		audience: toPassOwner | toAdmins, severity: "error", title: "Pass failed",
		body: func(c notificationContext) string { return c.passLabel() + " failed" + c.detail() + "." },
	},
	"pass.missed": {
		audience: toPassOwner | toAdmins, severity: "error", title: "Pass missed",
		body: func(c notificationContext) string {
			return c.passLabel() + " was missed; the worker never reported it."
		},
	},
	"worker.offline": {
		audience: toAdmins, severity: "error", title: "Worker offline",
		body: func(c notificationContext) string {
			return fmt.Sprintf("Worker %s stopped sending heartbeats. New passes cannot be scheduled.",
				c.record.EntityID)
		},
	},
	"worker.online": {
		audience: toAdmins, severity: "success", title: "Worker back online",
		body: func(notificationContext) string { return "The station worker is reporting again." },
	},
	"user.role_changed": {
		audience: toEntityUser, severity: "info", title: "Your role changed",
		body: func(c notificationContext) string {
			if role, ok := c.record.NewState["new_role"].(string); ok && role != "" {
				return "You are now " + role + "."
			}
			return "An administrator changed your role."
		},
	},
}

// notifyFor fans an audit record out to the users who should hear about it.
// It runs on the same querier as the audit insert, so inside a transaction
// the notifications commit or roll back with the event.
func (r *Repository) notifyFor(ctx context.Context, record domain.AuditRecord) error {
	rule, ok := notificationRules[record.Action]
	if !ok {
		return nil
	}

	nctx := notificationContext{record: record}
	if record.EntityType == "pass" {
		passID, err := uuid.Parse(record.EntityID)
		if err != nil {
			return nil
		}
		var owner uuid.UUID
		err = r.db.QueryRow(ctx, `
			SELECT s.name, p.aos_at, p.requested_by, p.status::text
			FROM passes p JOIN satellites s ON s.id = p.satellite_id
			WHERE p.id = $1`, passID).Scan(&nctx.satellite, &nctx.aos, &owner, &nctx.status)
		if err != nil {
			if isNoRows(err) {
				return nil
			}
			return fmt.Errorf("load pass for notification: %w", err)
		}
		nctx.ownerID = &owner
	}

	// A request that was approved on creation has nothing to decide.
	if record.Action == "pass.requested" && nctx.status != string(domain.PassPendingApproval) {
		return nil
	}

	var owner, entityUser *uuid.UUID
	if rule.audience&toPassOwner != 0 {
		owner = nctx.ownerID
	}
	if rule.audience&toEntityUser != 0 && record.EntityType == "user" {
		if id, err := uuid.Parse(record.EntityID); err == nil {
			entityUser = &id
		}
	}

	// pg_notify inside a transaction is delivered on commit, so live clients
	// never hear about a notification that was rolled back.
	_, err := r.db.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO notifications (user_id, audit_record_id, kind, severity, title, body,
				entity_type, entity_id)
			SELECT u.id, $1, $2, $3, $4, $5, $6, $7
			FROM users u
			WHERE (u.id = $8 OR u.id = $9 OR ($10 AND u.role IN ('admin', 'root')))
			  AND ($11::uuid IS NULL OR u.id <> $11)
			RETURNING id, user_id
		)
		SELECT count(pg_notify('`+NotificationChannel+`', user_id::text || ':' || id::text))
		FROM inserted`,
		record.ID, record.Action, rule.severity, rule.title, rule.body(nctx),
		record.EntityType, record.EntityID,
		owner, entityUser, rule.audience&toAdmins != 0, record.ActorUserID)
	if err != nil {
		return fmt.Errorf("insert notifications: %w", err)
	}
	return nil
}

// NotificationChannel is the Postgres NOTIFY channel that carries
// "<user id>:<notification id>" for every new notification.
const NotificationChannel = "aagasa_notifications"

const notificationColumns = `id, user_id, audit_record_id, kind, severity, title, body,
	entity_type, coalesce(entity_id, ''), created_at, read_at`

// ListNotifications returns a user's newest notifications first. With
// afterID > 0 only notifications newer than that id are returned.
func (r *Repository) ListNotifications(ctx context.Context, userID uuid.UUID,
	afterID int64, limit int) ([]domain.Notification, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+notificationColumns+`
		FROM notifications
		WHERE user_id = $1 AND id > $2
		ORDER BY id DESC
		LIMIT $3`, userID, afterID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	var out []domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.AuditRecordID, &n.Kind, &n.Severity,
			&n.Title, &n.Body, &n.EntityType, &n.EntityID, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, n)
	}
	return out, translate(rows.Err())
}

func (r *Repository) CountUnreadNotifications(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`,
		userID).Scan(&count)
	return count, translate(err)
}

// MarkNotificationRead marks one of the user's notifications read. It
// returns ErrNotFound when the notification is not the user's.
func (r *Repository) MarkNotificationRead(ctx context.Context, userID uuid.UUID, id int64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications SET read_at = coalesce(read_at, now())
		WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) MarkAllNotificationsRead(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE notifications SET read_at = now()
		WHERE user_id = $1 AND read_at IS NULL`, userID)
	return translate(err)
}
