package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// Config holds auth-related configuration.
type Config struct {
	AdminEmail      string
	SessionDuration time.Duration
	CookieName      string
	SecureCookie    bool
}

// Service orchestrates authentication operations.
type Service struct {
	sqlDB   *sql.DB
	queries *db.Queries
	config  Config
}

// NewService creates an auth service with the given dependencies.
func NewService(sqlDB *sql.DB, cfg Config) *Service {
	return &Service{
		sqlDB:   sqlDB,
		queries: db.New(sqlDB),
		config:  cfg,
	}
}

// CreateSession generates a new session for the given user.
// Returns the raw token (for the cookie). The database stores only the hash.
func (s *Service) CreateSession(ctx context.Context, userID string) (string, error) {
	rawToken, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}

	csrfToken, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}

	tokenHash := HashToken(rawToken)
	expiresAt := time.Now().UTC().Add(s.config.SessionDuration)

	err = s.queries.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: tokenHash,
		UserID:    userID,
		CsrfToken: csrfToken,
		ExpiresAt: expiresAt.Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}

	return rawToken, nil
}

// ValidateSession checks if a raw session token is valid and not expired.
// Returns the associated user and session, or an error if invalid.
func (s *Service) ValidateSession(ctx context.Context, rawToken string) (*User, *Session, error) {
	tokenHash := HashToken(rawToken)

	row, err := s.queries.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("session not found")
		}
		return nil, nil, fmt.Errorf("lookup session: %w", err)
	}

	expiresAt, err := time.Parse("2006-01-02 15:04:05", row.ExpiresAt)
	if err != nil {
		return nil, nil, fmt.Errorf("parse session expiry: %w", err)
	}

	if time.Now().UTC().After(expiresAt) {
		_ = s.queries.DeleteSession(ctx, tokenHash)
		return nil, nil, fmt.Errorf("session expired")
	}

	createdAt, err := time.Parse("2006-01-02 15:04:05", row.CreatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("parse session created_at: %w", err)
	}

	session := &Session{
		TokenHash: row.TokenHash,
		UserID:    row.UserID,
		CSRFToken: row.CsrfToken,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}

	userRow, err := s.queries.GetUserByID(ctx, row.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("lookup user: %w", err)
	}

	user, err := userFromRow(userRow)
	if err != nil {
		return nil, nil, fmt.Errorf("convert user: %w", err)
	}

	if !user.Active {
		// Clean up the orphaned session for the inactive user.
		_ = s.queries.DeleteSession(ctx, tokenHash)
		return nil, nil, fmt.Errorf("account deactivated")
	}

	return &user, session, nil
}

// Logout deletes a session by its raw token.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	tokenHash := HashToken(rawToken)
	return s.queries.DeleteSession(ctx, tokenHash)
}

// ProvisionUser checks if an email is authorized and provisions or returns the user.
// Authorization: the email must match the admin email or have a pending invitation.
func (s *Service) ProvisionUser(ctx context.Context, email, displayName string) (*User, error) {
	existing, err := s.queries.GetUserByEmail(ctx, email)
	if err == nil {
		if existing.Active == 1 {
			u, convErr := userFromRow(existing)
			if convErr != nil {
				return nil, fmt.Errorf("convert user: %w", convErr)
			}
			return &u, nil
		}
		return nil, fmt.Errorf("account deactivated")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup user by email: %w", err)
	}

	// No existing user -- check authorization.
	var role Role

	if strings.EqualFold(email, s.config.AdminEmail) {
		role = RoleAdmin
	} else {
		inv, invErr := s.queries.GetInvitationByEmail(ctx, email)
		if invErr != nil {
			if errors.Is(invErr, sql.ErrNoRows) {
				return nil, fmt.Errorf("unauthorized email")
			}
			return nil, fmt.Errorf("lookup invitation: %w", invErr)
		}
		role = Role(inv.Role)
	}

	id, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("generate user id: %w", err)
	}

	row, err := s.queries.CreateUser(ctx, db.CreateUserParams{
		ID:          id,
		Email:       email,
		DisplayName: displayName,
		Role:        string(role),
	})
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	// Consume invitation if the user was invited.
	if !strings.EqualFold(email, s.config.AdminEmail) {
		if delErr := s.queries.DeleteInvitationByEmail(ctx, email); delErr != nil {
			return nil, fmt.Errorf("consume invitation: %w", delErr)
		}
	}

	user, err := userFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("convert user: %w", err)
	}
	return &user, nil
}

// InviteUser creates an invitation for the given email with the specified role.
func (s *Service) InviteUser(ctx context.Context, email string, role Role, invitedBy string) error {
	id, err := generateID()
	if err != nil {
		return fmt.Errorf("generate invitation id: %w", err)
	}
	return s.queries.CreateInvitation(ctx, db.CreateInvitationParams{
		ID:        id,
		Email:     email,
		Role:      string(role),
		InvitedBy: invitedBy,
	})
}

// RevokeInvitation deletes a pending invitation.
func (s *Service) RevokeInvitation(ctx context.Context, invitationID string) error {
	return s.queries.DeleteInvitation(ctx, invitationID)
}

// DeactivateUser marks a user as inactive and deletes all their sessions.
// Both operations run in a single transaction to prevent partial state.
func (s *Service) DeactivateUser(ctx context.Context, userID string) error {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)
	if err := qtx.DeleteSessionsByUserID(ctx, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	if err := qtx.DeactivateUser(ctx, userID); err != nil {
		return fmt.Errorf("deactivate user: %w", err)
	}
	return tx.Commit()
}

// ListUsers returns all user accounts.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.queries.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users := make([]User, 0, len(rows))
	for _, row := range rows {
		u, err := userFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("convert user: %w", err)
		}
		users = append(users, u)
	}
	return users, nil
}

// ListInvitations returns all pending invitations.
func (s *Service) ListInvitations(ctx context.Context) ([]Invitation, error) {
	rows, err := s.queries.ListInvitations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	invitations := make([]Invitation, 0, len(rows))
	for _, row := range rows {
		createdAt, err := time.Parse("2006-01-02 15:04:05", row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse invitation created_at: %w", err)
		}
		invitations = append(invitations, Invitation{
			ID:        row.ID,
			Email:     row.Email,
			Role:      Role(row.Role),
			InvitedBy: row.InvitedBy,
			CreatedAt: createdAt,
		})
	}
	return invitations, nil
}

// CleanExpiredSessions removes sessions past their expires_at and returns the count deleted.
func (s *Service) CleanExpiredSessions(ctx context.Context) (int64, error) {
	result, err := s.sqlDB.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < datetime('now')")
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return result.RowsAffected()
}

// generateID creates a random hex-encoded ID (16 bytes = 32 chars).
func generateID() (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	// Use first 32 chars for IDs (16 bytes of entropy).
	return token[:32], nil
}
