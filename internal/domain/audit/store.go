// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package audit

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/yousysadmin/pacer/internal/core/dbutil"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const auditSelect = `
SELECT id, actor_user_id, actor_email, action, target_type,
       target_id, detail, client_ip, request_id, occurred_at
FROM audit_log`

func (s *Store) Put(ctx context.Context, e *auditmodel.Entry) error {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO audit_log (id, actor_user_id, actor_email, action, target_type,
                               target_id, detail, client_ip, request_id, occurred_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `,
		e.ID, dbutil.NullStr(e.ActorUserID), dbutil.NullStr(e.ActorEmail), e.Action,
		dbutil.NullStr(e.TargetType), dbutil.NullStr(e.TargetID), dbutil.NullStr(e.Detail),
		dbutil.NullStr(e.ClientIP), dbutil.NullStr(e.RequestID), e.OccurredAt,
	)
	return err
}

// List returns the newest matching rows first.
// Limit defaults to 100 and is capped at 1000 so a UI without a limit query param can't
// accidentally pull the whole log.
func (s *Store) List(ctx context.Context, f auditmodel.ListFilter) ([]*auditmodel.Entry, error) {
	where, args := buildAuditWhere(f)
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	q := auditSelect + where + " ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*auditmodel.Entry
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Count returns the total number of matching rows, ignoring Limit + Offset.
func (s *Store) Count(ctx context.Context, f auditmodel.ListFilter) (int, error) {
	where, args := buildAuditWhere(f)
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log"+where, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE occurred_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// buildAuditWhere assembles the shared WHERE clause for List/Count so
// pagination totals match the page contents.
func buildAuditWhere(f auditmodel.ListFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if f.Action != "" {
		clauses = append(clauses, "action = ?")
		args = append(args, f.Action)
	}
	if f.Actor != "" {
		clauses = append(clauses, "actor_email = ?")
		args = append(args, f.Actor)
	}
	if f.TargetType != "" {
		clauses = append(clauses, "target_type = ?")
		args = append(args, f.TargetType)
	}
	if f.TargetID != "" {
		clauses = append(clauses, "target_id = ?")
		args = append(args, f.TargetID)
	}
	if f.Q != "" {
		// Escape LIKE meta-chars so an operator pasting an IP
		// (1.2.3.4) or a job ID with underscores doesn't get
		// unintended wildcard behavior. SQLite's LIKE accepts
		// ESCAPE '\' to opt out of metachar interpretation per-char.
		needle := "%" + escapeLike(f.Q) + "%"
		clauses = append(clauses, `(
            target_id LIKE ? ESCAPE '\' OR
            detail LIKE ? ESCAPE '\' OR
            client_ip LIKE ? ESCAPE '\' OR
            actor_email LIKE ? ESCAPE '\' OR
            request_id LIKE ? ESCAPE '\' OR
            action LIKE ? ESCAPE '\'
        )`)
		args = append(args, needle, needle, needle, needle, needle, needle)
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, "occurred_at >= ?")
		args = append(args, f.Since)
	}
	if !f.Until.IsZero() {
		clauses = append(clauses, "occurred_at < ?")
		args = append(args, f.Until)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// escapeLike backslash-escapes the three characters SQLite's LIKE
// treats as metacharacters so a free-text needle behaves like a
// literal substring search. Paired with LIKE ? ESCAPE '\' in the
// query string.
func escapeLike(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(s)
}

func scanAudit(r interface{ Scan(...any) error }) (*auditmodel.Entry, error) {
	var e auditmodel.Entry
	var actor, email, targetType, targetID, detail, clientIP, reqID sql.NullString
	if err := r.Scan(&e.ID, &actor, &email, &e.Action, &targetType,
		&targetID, &detail, &clientIP, &reqID, &e.OccurredAt); err != nil {
		return nil, err
	}
	e.ActorUserID = actor.String
	e.ActorEmail = email.String
	e.TargetType = targetType.String
	e.TargetID = targetID.String
	e.Detail = detail.String
	e.ClientIP = clientIP.String
	e.RequestID = reqID.String
	return &e, nil
}
