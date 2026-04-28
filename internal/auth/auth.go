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

// ErrUserNotFound is returned when an operation targets a user ID that does
// not exist (e.g., DeactivateUser called with a stale or fabricated ID).
var ErrUserNotFound = errors.New("user not found")

// ErrInvitationNotFound is returned when an operation targets an invitation ID
// that does not exist (e.g., RevokeInvitation called with a stale or
// fabricated ID).
var ErrInvitationNotFound = errors.New("invitation not found")

// ErrDeviceNotFound is returned when an operation targets a device id that
// does not exist or, for rotation, has already been revoked.
var ErrDeviceNotFound = errors.New("device not found")

// ErrDeviceRevoked is returned by ValidateDeviceToken when the token belongs
// to a device that has been revoked.
var ErrDeviceRevoked = errors.New("device revoked")

// Config holds auth-related configuration.
type Config struct {
	AdminEmail             string
	SessionDuration        time.Duration
	CookieName             string
	SecureCookie           bool
	DeviceCookieName       string
	DeviceLastSeenInterval time.Duration
	DeviceLandingURL       string
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

// SessionDuration returns the configured session lifetime.
func (s *Service) SessionDuration() time.Duration {
	return s.config.SessionDuration
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

// RevokeInvitation deletes a pending invitation. Returns ErrInvitationNotFound
// if no invitation with the given ID exists.
func (s *Service) RevokeInvitation(ctx context.Context, invitationID string) error {
	if _, err := s.queries.GetInvitationByID(ctx, invitationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationNotFound
		}
		return fmt.Errorf("lookup invitation: %w", err)
	}
	return s.queries.DeleteInvitation(ctx, invitationID)
}

// DeactivateUser marks a user as inactive and deletes all their sessions.
// Both operations run in a single transaction to prevent partial state.
// Returns ErrUserNotFound if no user with the given ID exists.
func (s *Service) DeactivateUser(ctx context.Context, userID string) error {
	if _, err := s.queries.GetUserByID(ctx, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("lookup user: %w", err)
	}

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

// CreateDevice provisions a new device. It generates a random raw token,
// stores its SHA-256 hash, and returns the new Device alongside the raw
// token. The caller MUST surface the raw token to the admin once and then
// discard it -- it cannot be recovered later.
func (s *Service) CreateDevice(ctx context.Context, name, createdBy string) (Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Device{}, "", fmt.Errorf("device name required")
	}

	rawToken, err := GenerateToken()
	if err != nil {
		return Device{}, "", fmt.Errorf("generate device token: %w", err)
	}

	id, err := generateID()
	if err != nil {
		return Device{}, "", fmt.Errorf("generate device id: %w", err)
	}

	if err := s.queries.CreateDevice(ctx, db.CreateDeviceParams{
		ID:        id,
		Name:      name,
		TokenHash: HashToken(rawToken),
		CreatedBy: createdBy,
	}); err != nil {
		return Device{}, "", fmt.Errorf("create device: %w", err)
	}

	row, err := s.queries.GetDeviceByID(ctx, id)
	if err != nil {
		return Device{}, "", fmt.Errorf("fetch created device: %w", err)
	}
	dev, err := deviceFromRow(row)
	if err != nil {
		return Device{}, "", fmt.Errorf("convert device: %w", err)
	}
	return dev, rawToken, nil
}

// ValidateDeviceToken hashes the raw token, looks it up, and returns the
// associated Device on success. Returns ErrDeviceNotFound when no device row
// matches and ErrDeviceRevoked when the matching device has been revoked.
func (s *Service) ValidateDeviceToken(ctx context.Context, rawToken string) (*Device, error) {
	row, err := s.queries.GetDeviceByTokenHash(ctx, HashToken(rawToken))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDeviceNotFound
		}
		return nil, fmt.Errorf("lookup device: %w", err)
	}

	dev, err := deviceFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("convert device: %w", err)
	}
	if dev.IsRevoked() {
		return nil, ErrDeviceRevoked
	}
	return &dev, nil
}

// MarkDeviceSeen updates last_seen_at on the device, but only if the previous
// last_seen_at is older than the configured throttle interval (or NULL). A
// throttled call that updates zero rows is not an error -- only true SQL
// failures are surfaced.
func (s *Service) MarkDeviceSeen(ctx context.Context, deviceID string) error {
	seconds := int64(s.config.DeviceLastSeenInterval.Truncate(time.Second).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	interval := fmt.Sprintf("-%d seconds", seconds)

	if _, err := s.queries.TouchDeviceSeen(ctx, db.TouchDeviceSeenParams{
		ID:       deviceID,
		Datetime: interval,
	}); err != nil {
		return fmt.Errorf("touch device seen: %w", err)
	}
	return nil
}

// RevokeDevice marks the device as revoked. Returns ErrDeviceNotFound when no
// device with the given id exists OR when the device row is present but its
// revoked_at column is already non-NULL. Treating both cases identically lets
// the UI surface a single "Device not found" flash and prevents a misleading
// "revoked" success message on a no-op double-click.
func (s *Service) RevokeDevice(ctx context.Context, deviceID string) error {
	row, err := s.queries.GetDeviceByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		return fmt.Errorf("lookup device: %w", err)
	}
	if row.RevokedAt.Valid {
		return ErrDeviceNotFound
	}
	if err := s.queries.RevokeDevice(ctx, deviceID); err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	return nil
}

// ListDevices returns all devices, including revoked ones, in created_at
// order.
func (s *Service) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.queries.ListDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	devices := make([]Device, 0, len(rows))
	for _, row := range rows {
		d, err := deviceFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("convert device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, nil
}

// RotateDeviceToken issues a fresh raw token for the given device, replacing
// the existing token_hash. Returns the raw token on success. Returns
// ErrDeviceNotFound when the device id is unknown OR when the device has been
// revoked -- both cases are equivalent for the caller (refuse the operation).
// The returned raw token is the only opportunity for the caller to capture
// it; it is not persisted in plaintext anywhere.
func (s *Service) RotateDeviceToken(ctx context.Context, deviceID string) (string, error) {
	rawToken, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generate device token: %w", err)
	}
	res, err := s.queries.RotateDeviceToken(ctx, db.RotateDeviceTokenParams{
		TokenHash: HashToken(rawToken),
		ID:        deviceID,
	})
	if err != nil {
		return "", fmt.Errorf("rotate device token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("rotate device token rows: %w", err)
	}
	if n == 0 {
		return "", ErrDeviceNotFound
	}
	return rawToken, nil
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
