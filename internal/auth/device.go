package auth

import (
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// Device represents a registered display device. The raw bearer token is never
// stored on this struct; only the SHA-256 hash lives in the database.
type Device struct {
	ID         string
	Name       string
	TokenHash  string
	CreatedBy  string
	CreatedAt  time.Time
	LastSeenAt *time.Time
	RevokedAt  *time.Time
}

// IsRevoked reports whether the device has been revoked.
func (d Device) IsRevoked() bool {
	return d.RevokedAt != nil
}

// deviceFromRow converts a sqlc-generated db.Device row to an auth.Device.
func deviceFromRow(row db.Device) (Device, error) {
	createdAt, err := time.Parse("2006-01-02 15:04:05", row.CreatedAt)
	if err != nil {
		return Device{}, err
	}

	dev := Device{
		ID:        row.ID,
		Name:      row.Name,
		TokenHash: row.TokenHash,
		CreatedBy: row.CreatedBy,
		CreatedAt: createdAt,
	}

	if row.LastSeenAt.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", row.LastSeenAt.String)
		if err != nil {
			return Device{}, err
		}
		dev.LastSeenAt = &t
	}

	if row.RevokedAt.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", row.RevokedAt.String)
		if err != nil {
			return Device{}, err
		}
		dev.RevokedAt = &t
	}

	return dev, nil
}
