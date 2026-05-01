// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package user

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yousysadmin/pacer/internal/core/dbutil"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const userSelect = `
SELECT id, email, password_hash, oidc_subject, role, super_user, disabled,
       refresh_version, created_at, last_login_at
FROM users`

// Get fetches a user by email (the login key). Returns (nil, nil)
// when the row is missing so the auth handler can return a uniform
// "invalid credentials" without leaking which half was wrong.
func (s *Store) Get(ctx context.Context, email string) (*usermodel.User, error) {
	row := s.db.QueryRowContext(ctx, userSelect+` WHERE email = ?`, email)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (s *Store) GetByID(ctx context.Context, id string) (*usermodel.User, error) {
	row := s.db.QueryRowContext(ctx, userSelect+` WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// GetByOIDCSubject is the lookup the OIDC callback uses to find an
// existing user already linked to a particular IdP `sub` claim.
func (s *Store) GetByOIDCSubject(ctx context.Context, sub string) (*usermodel.User, error) {
	if sub == "" {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx, userSelect+` WHERE oidc_subject = ?`, sub)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// Put upserts the user keyed by id. Empty PasswordHash / OIDCSubject
// are stored as SQL NULL so the partial-unique index on oidc_subject
// only fires for actually-linked users.
func (s *Store) Put(ctx context.Context, u *usermodel.User) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	if u.Role == "" {
		u.Role = usermodel.RoleAdmin
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO users (
            id, email, password_hash, oidc_subject, role, super_user, disabled,
            refresh_version, created_at, last_login_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            email           = excluded.email,
            password_hash   = excluded.password_hash,
            oidc_subject    = excluded.oidc_subject,
            role            = excluded.role,
            super_user      = excluded.super_user,
            disabled        = excluded.disabled,
            refresh_version = excluded.refresh_version,
            last_login_at   = excluded.last_login_at
    `,
		u.ID, u.Email,
		dbutil.NullStr(u.PasswordHash),
		dbutil.NullStr(u.OIDCSubject),
		string(u.Role),
		dbutil.BoolInt(u.SuperUser), dbutil.BoolInt(u.Disabled),
		u.RefreshVersion, u.CreatedAt, dbutil.NullTime(u.LastLoginAt),
	)
	return err
}

func (s *Store) Delete(ctx context.Context, email string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE email = ?`, email)
	return err
}

func (s *Store) List(ctx context.Context) ([]*usermodel.User, error) {
	rows, err := s.db.QueryContext(ctx, userSelect+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*usermodel.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) TouchLastLogin(ctx context.Context, email string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE email = ?`, time.Now().UTC(), email)
	return err
}

func scanUser(r interface{ Scan(...any) error }) (*usermodel.User, error) {
	var u usermodel.User
	var role string
	var superUser, disabled int
	var passwordHash, oidcSubject sql.NullString
	var lastLogin sql.NullTime
	if err := r.Scan(&u.ID, &u.Email, &passwordHash, &oidcSubject, &role,
		&superUser, &disabled, &u.RefreshVersion,
		&u.CreatedAt, &lastLogin); err != nil {
		return nil, err
	}
	u.PasswordHash = passwordHash.String
	u.OIDCSubject = oidcSubject.String
	u.Role = usermodel.Role(role)
	u.SuperUser = superUser != 0
	u.Disabled = disabled != 0
	if lastLogin.Valid {
		u.LastLoginAt = new(lastLogin.Time)
	}
	return &u, nil
}
