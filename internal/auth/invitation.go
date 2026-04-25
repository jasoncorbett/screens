package auth

import "time"

// Invitation represents a pending invitation for a new user.
type Invitation struct {
	ID        string
	Email     string
	Role      Role
	InvitedBy string
	CreatedAt time.Time
}
