package auth

import "testing"

func TestIdentity(t *testing.T) {
	t.Parallel()

	user := &User{ID: "user-id-123"}
	device := &Device{ID: "device-id-abc"}

	tests := []struct {
		name      string
		identity  Identity
		wantID    string
		wantAdmin bool
		wantDev   bool
	}{
		{
			name:      "admin",
			identity:  Identity{Kind: IdentityAdmin, User: user},
			wantID:    "user:user-id-123",
			wantAdmin: true,
			wantDev:   false,
		},
		{
			name:      "device",
			identity:  Identity{Kind: IdentityDevice, Device: device},
			wantID:    "device:device-id-abc",
			wantAdmin: false,
			wantDev:   true,
		},
		{
			name:      "none",
			identity:  Identity{Kind: IdentityNone},
			wantID:    "",
			wantAdmin: false,
			wantDev:   false,
		},
		{
			name:      "admin with nil user",
			identity:  Identity{Kind: IdentityAdmin, User: nil},
			wantID:    "",
			wantAdmin: true,
			wantDev:   false,
		},
		{
			name:      "device with nil device",
			identity:  Identity{Kind: IdentityDevice, Device: nil},
			wantID:    "",
			wantAdmin: false,
			wantDev:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.identity.ID(); got != tt.wantID {
				t.Errorf("ID() = %q, want %q", got, tt.wantID)
			}
			if got := tt.identity.IsAdmin(); got != tt.wantAdmin {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.wantAdmin)
			}
			if got := tt.identity.IsDevice(); got != tt.wantDev {
				t.Errorf("IsDevice() = %v, want %v", got, tt.wantDev)
			}
		})
	}
}
