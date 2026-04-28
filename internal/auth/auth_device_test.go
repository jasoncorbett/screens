package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// newDeviceTestService builds a Service backed by a fresh in-memory database
// and a creator user with the given role. The interval controls the
// MarkDeviceSeen throttle.
func newDeviceTestService(t *testing.T, interval time.Duration) (*Service, *db.Queries, db.User) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	cfg := Config{
		AdminEmail:             "admin@example.com",
		SessionDuration:        time.Hour,
		CookieName:             "test_session",
		SecureCookie:           false,
		DeviceCookieName:       "test_device",
		DeviceLastSeenInterval: interval,
		DeviceLandingURL:       "/device/",
	}
	svc := NewService(sqlDB, cfg)
	q := db.New(sqlDB)
	creator := createTestUser(t, q, "creator@example.com", "admin")
	return svc, q, creator
}

func TestCreateDevice(t *testing.T) {
	t.Parallel()

	t.Run("persists hashed token and returns raw", func(t *testing.T) {
		t.Parallel()
		svc, q, creator := newDeviceTestService(t, time.Minute)

		dev, rawToken, err := svc.CreateDevice(context.Background(), "kitchen", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() error: %v", err)
		}
		if rawToken == "" {
			t.Fatal("CreateDevice() raw token is empty")
		}
		if len(rawToken) != 64 {
			t.Errorf("raw token length = %d, want 64", len(rawToken))
		}
		if dev.ID == "" {
			t.Error("device ID is empty")
		}
		if dev.Name != "kitchen" {
			t.Errorf("device name = %q, want %q", dev.Name, "kitchen")
		}
		if dev.CreatedBy != creator.ID {
			t.Errorf("CreatedBy = %q, want %q", dev.CreatedBy, creator.ID)
		}
		if dev.CreatedAt.IsZero() {
			t.Error("CreatedAt is zero -- expected DB-assigned timestamp")
		}

		// Hash in DB matches HashToken(rawToken); raw token never appears in
		// any column of the row.
		row, err := q.GetDeviceByID(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("GetDeviceByID() error: %v", err)
		}
		wantHash := HashToken(rawToken)
		if row.TokenHash != wantHash {
			t.Errorf("token_hash = %q, want %q", row.TokenHash, wantHash)
		}
		if row.ID == rawToken || row.Name == rawToken || row.CreatedBy == rawToken || row.TokenHash == rawToken {
			t.Error("raw token leaked into a stored column")
		}
	})

	t.Run("rejects empty and whitespace names without inserting", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{"", "   ", "\t\n"} {
			t.Run("name="+name, func(t *testing.T) {
				svc, _, creator := newDeviceTestService(t, time.Minute)

				_, _, err := svc.CreateDevice(context.Background(), name, creator.ID)
				if err == nil {
					t.Fatalf("CreateDevice(%q) expected error, got nil", name)
				}

				devices, err := svc.ListDevices(context.Background())
				if err != nil {
					t.Fatalf("ListDevices() error: %v", err)
				}
				if len(devices) != 0 {
					t.Errorf("ListDevices() returned %d devices, want 0", len(devices))
				}
			})
		}
	})

	t.Run("two calls return distinct raw tokens", func(t *testing.T) {
		t.Parallel()
		svc, _, creator := newDeviceTestService(t, time.Minute)

		_, t1, err := svc.CreateDevice(context.Background(), "a", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() #1 error: %v", err)
		}
		_, t2, err := svc.CreateDevice(context.Background(), "b", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() #2 error: %v", err)
		}
		if t1 == t2 {
			t.Error("CreateDevice() returned the same raw token twice")
		}
	})
}

func TestValidateDeviceToken(t *testing.T) {
	t.Parallel()

	t.Run("returns matching device for valid token", func(t *testing.T) {
		t.Parallel()
		svc, _, creator := newDeviceTestService(t, time.Minute)

		dev, rawToken, err := svc.CreateDevice(context.Background(), "kitchen", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() error: %v", err)
		}

		got, err := svc.ValidateDeviceToken(context.Background(), rawToken)
		if err != nil {
			t.Fatalf("ValidateDeviceToken() error: %v", err)
		}
		if got == nil {
			t.Fatal("ValidateDeviceToken() returned nil device")
		}
		if got.ID != dev.ID {
			t.Errorf("device ID = %q, want %q", got.ID, dev.ID)
		}
	})

	t.Run("unknown token returns ErrDeviceNotFound", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newDeviceTestService(t, time.Minute)

		_, err := svc.ValidateDeviceToken(context.Background(), "garbage-token-value")
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("ValidateDeviceToken() error = %v, want ErrDeviceNotFound", err)
		}
	})

	t.Run("revoked device returns ErrDeviceRevoked but other devices still validate", func(t *testing.T) {
		t.Parallel()
		svc, _, creator := newDeviceTestService(t, time.Minute)

		devA, tokenA, err := svc.CreateDevice(context.Background(), "a", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() A error: %v", err)
		}
		_, tokenB, err := svc.CreateDevice(context.Background(), "b", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() B error: %v", err)
		}

		if err := svc.RevokeDevice(context.Background(), devA.ID); err != nil {
			t.Fatalf("RevokeDevice() error: %v", err)
		}

		_, err = svc.ValidateDeviceToken(context.Background(), tokenA)
		if !errors.Is(err, ErrDeviceRevoked) {
			t.Errorf("ValidateDeviceToken(A) after revoke = %v, want ErrDeviceRevoked", err)
		}

		gotB, err := svc.ValidateDeviceToken(context.Background(), tokenB)
		if err != nil {
			t.Fatalf("ValidateDeviceToken(B) after revoking A error: %v", err)
		}
		if gotB == nil {
			t.Fatal("ValidateDeviceToken(B) returned nil")
		}
	})
}

func TestRevokeDevice_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	err := svc.RevokeDevice(context.Background(), "no-such-device-id")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("RevokeDevice(unknown) = %v, want ErrDeviceNotFound", err)
	}
}

func TestMarkDeviceSeen(t *testing.T) {
	t.Parallel()

	t.Run("zero throttle updates every call", func(t *testing.T) {
		t.Parallel()
		svc, q, creator := newDeviceTestService(t, 0)

		dev, _, err := svc.CreateDevice(context.Background(), "always", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() error: %v", err)
		}

		if err := svc.MarkDeviceSeen(context.Background(), dev.ID); err != nil {
			t.Fatalf("MarkDeviceSeen() #1 error: %v", err)
		}
		row1, err := q.GetDeviceByID(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("GetDeviceByID() #1 error: %v", err)
		}
		if !row1.LastSeenAt.Valid {
			t.Fatal("LastSeenAt not set after first MarkDeviceSeen")
		}

		// Sleep a hair over a second so SQLite's datetime('now') (1-second
		// resolution) advances, then mark again. The throttle is zero so
		// the row must update.
		time.Sleep(1100 * time.Millisecond)
		if err := svc.MarkDeviceSeen(context.Background(), dev.ID); err != nil {
			t.Fatalf("MarkDeviceSeen() #2 error: %v", err)
		}
		row2, err := q.GetDeviceByID(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("GetDeviceByID() #2 error: %v", err)
		}
		if row2.LastSeenAt.String == row1.LastSeenAt.String {
			t.Errorf("LastSeenAt unchanged with zero throttle: %q", row2.LastSeenAt.String)
		}
	})

	t.Run("large throttle leaves second call as no-op", func(t *testing.T) {
		t.Parallel()
		svc, q, creator := newDeviceTestService(t, time.Hour)

		dev, _, err := svc.CreateDevice(context.Background(), "throttled", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() error: %v", err)
		}

		if err := svc.MarkDeviceSeen(context.Background(), dev.ID); err != nil {
			t.Fatalf("MarkDeviceSeen() #1 error: %v", err)
		}
		row1, err := q.GetDeviceByID(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("GetDeviceByID() #1 error: %v", err)
		}
		if !row1.LastSeenAt.Valid {
			t.Fatal("LastSeenAt not set after first MarkDeviceSeen")
		}

		// Sleep just past one second of wall-clock to make sure that
		// datetime('now') would have advanced if the throttle didn't apply.
		time.Sleep(1100 * time.Millisecond)
		if err := svc.MarkDeviceSeen(context.Background(), dev.ID); err != nil {
			t.Fatalf("MarkDeviceSeen() #2 error: %v", err)
		}
		row2, err := q.GetDeviceByID(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("GetDeviceByID() #2 error: %v", err)
		}
		if row2.LastSeenAt.String != row1.LastSeenAt.String {
			t.Errorf("LastSeenAt changed under 1h throttle: was %q, now %q",
				row1.LastSeenAt.String, row2.LastSeenAt.String)
		}
	})
}

func TestListDevices(t *testing.T) {
	t.Parallel()

	t.Run("returns devices in created_at order including revoked", func(t *testing.T) {
		t.Parallel()
		svc, _, creator := newDeviceTestService(t, time.Minute)

		devA, _, err := svc.CreateDevice(context.Background(), "a", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice(a) error: %v", err)
		}
		// Sleep so the second device gets a later created_at (1s SQLite
		// resolution).
		time.Sleep(1100 * time.Millisecond)
		devB, _, err := svc.CreateDevice(context.Background(), "b", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice(b) error: %v", err)
		}

		if err := svc.RevokeDevice(context.Background(), devA.ID); err != nil {
			t.Fatalf("RevokeDevice(a) error: %v", err)
		}

		list, err := svc.ListDevices(context.Background())
		if err != nil {
			t.Fatalf("ListDevices() error: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("ListDevices() len = %d, want 2", len(list))
		}
		if list[0].ID != devA.ID {
			t.Errorf("first = %q, want %q (created_at order)", list[0].ID, devA.ID)
		}
		if list[1].ID != devB.ID {
			t.Errorf("second = %q, want %q", list[1].ID, devB.ID)
		}
		if !list[0].IsRevoked() {
			t.Error("revoked device A not flagged as revoked in ListDevices result")
		}
		if list[1].IsRevoked() {
			t.Error("non-revoked device B reported as revoked")
		}
	})
}

func TestRotateDeviceToken(t *testing.T) {
	t.Parallel()

	t.Run("new token validates and old token is gone", func(t *testing.T) {
		t.Parallel()
		svc, _, creator := newDeviceTestService(t, time.Minute)

		dev, oldToken, err := svc.CreateDevice(context.Background(), "rotator", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() error: %v", err)
		}

		newToken, err := svc.RotateDeviceToken(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("RotateDeviceToken() error: %v", err)
		}
		if newToken == "" {
			t.Fatal("RotateDeviceToken() returned empty token")
		}
		if newToken == oldToken {
			t.Error("RotateDeviceToken() returned the same token as before")
		}

		// New token validates.
		got, err := svc.ValidateDeviceToken(context.Background(), newToken)
		if err != nil {
			t.Fatalf("ValidateDeviceToken(new) error: %v", err)
		}
		if got == nil || got.ID != dev.ID {
			t.Errorf("ValidateDeviceToken(new) returned %+v, want device %s", got, dev.ID)
		}

		// Old token no longer validates.
		_, err = svc.ValidateDeviceToken(context.Background(), oldToken)
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("ValidateDeviceToken(old) = %v, want ErrDeviceNotFound", err)
		}
	})

	t.Run("two consecutive rotations return distinct tokens", func(t *testing.T) {
		t.Parallel()
		svc, _, creator := newDeviceTestService(t, time.Minute)

		dev, _, err := svc.CreateDevice(context.Background(), "rotator", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() error: %v", err)
		}

		t1, err := svc.RotateDeviceToken(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("RotateDeviceToken() #1 error: %v", err)
		}
		t2, err := svc.RotateDeviceToken(context.Background(), dev.ID)
		if err != nil {
			t.Fatalf("RotateDeviceToken() #2 error: %v", err)
		}
		if t1 == t2 {
			t.Error("RotateDeviceToken() returned the same token twice in a row")
		}
	})

	t.Run("unknown id returns ErrDeviceNotFound", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newDeviceTestService(t, time.Minute)

		_, err := svc.RotateDeviceToken(context.Background(), "no-such-device-id")
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("RotateDeviceToken(unknown) = %v, want ErrDeviceNotFound", err)
		}
	})

	t.Run("revoked device returns ErrDeviceNotFound", func(t *testing.T) {
		t.Parallel()
		svc, _, creator := newDeviceTestService(t, time.Minute)

		dev, _, err := svc.CreateDevice(context.Background(), "to-be-revoked", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice() error: %v", err)
		}
		if err := svc.RevokeDevice(context.Background(), dev.ID); err != nil {
			t.Fatalf("RevokeDevice() error: %v", err)
		}

		_, err = svc.RotateDeviceToken(context.Background(), dev.ID)
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("RotateDeviceToken(revoked) = %v, want ErrDeviceNotFound", err)
		}
	})
}
