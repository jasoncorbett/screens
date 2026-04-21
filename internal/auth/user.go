package auth

import (
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// Role represents a user's authorization level.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// User represents an authenticated user account.
type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        Role
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// userFromRow converts a sqlc-generated db.User row to an auth.User.
func userFromRow(row db.User) (User, error) {
	createdAt, err := time.Parse("2006-01-02 15:04:05", row.CreatedAt)
	if err != nil {
		return User{}, err
	}
	updatedAt, err := time.Parse("2006-01-02 15:04:05", row.UpdatedAt)
	if err != nil {
		return User{}, err
	}
	return User{
		ID:          row.ID,
		Email:       row.Email,
		DisplayName: row.DisplayName,
		Role:        Role(row.Role),
		Active:      row.Active == 1,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}
