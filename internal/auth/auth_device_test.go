package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// seedTestScreen inserts a theme and a screen via raw SQL so device-assignment
// tests can satisfy the screen_id FK without importing internal/screens (which
// would create a package cycle: screens depends on auth).
func seedTestScreen(t *testing.T, sqlDB *sql.DB, themeID, screenID, screenName string) {
	t.Helper()
	if _, err := sqlDB.Exec(
		`INSERT INTO themes
		 (id, name, is_default, color_bg, color_surface, color_border, color_text,
		  color_text_muted, color_accent, font_family, font_family_mono, radius)
		 VALUES (?, ?, 0, '#000000', '#111111', '#222222', '#ffffff',
		         '#cccccc', '#7b93ff', 'system-ui', '', '10px')`,
		themeID, themeID,
	); err != nil {
		t.Fatalf("seed theme %s: %v", themeID, err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO screens (id, name, theme_id, rotation_interval_seconds) VALUES (?, ?, ?, 30)",
		screenID, screenName, themeID,
	); err != nil {
		t.Fatalf("seed screen %s: %v", screenID, err)
	}
}

// newDeviceTestService builds a Service backed by a fresh in-memory database
// and a creator user with the given role. The interval controls the
// MarkDeviceSeen throttle.
//
// MaxOpenConns is pinned to 1 because modernc.org/sqlite gives each new
// connection its own private :memory: database; without this cap, parallel
// goroutines that hit a fresh connection see "no such table: devices".
func newDeviceTestService(t *testing.T, interval time.Duration) (*Service, *db.Queries, db.User) {
	t.Helper()
	svc, q, _, creator := newDeviceTestServiceWithDB(t, interval)
	return svc, q, creator
}

// newDeviceTestServiceWithDB is the same as newDeviceTestService but also
// returns the underlying *sql.DB so callers can drive raw SQL (e.g., to seed
// a screen row that satisfies the screen_id FK without importing
// internal/screens, which would create a package cycle).
func newDeviceTestServiceWithDB(t *testing.T, interval time.Duration) (*Service, *db.Queries, *sql.DB, db.User) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	sqlDB.SetMaxOpenConns(1)
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
	return svc, q, sqlDB, creator
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

// findDeviceByID is a tiny helper that scans ListDevices for a particular id.
// We use ListDevices (rather than a direct DB read) so the test exercises the
// same row-mapping path the rest of the codebase uses.
func findDeviceByID(t *testing.T, svc *Service, id string) Device {
	t.Helper()
	devices, err := svc.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	for _, d := range devices {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("device %q not found in ListDevices result", id)
	return Device{}
}

func TestAssignDeviceToScreen(t *testing.T) {
	t.Parallel()

	t.Run("assigns screen id and ListDevices reflects it", func(t *testing.T) {
		t.Parallel()
		svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
		seedTestScreen(t, sqlDB, "theme-assign", "screen-assign", "screen-assign-name")

		dev, _, err := svc.CreateDevice(context.Background(), "kitchen", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice: %v", err)
		}
		// Fresh device has no screen.
		if dev.ScreenID != nil {
			t.Fatalf("fresh device ScreenID = %v, want nil", dev.ScreenID)
		}

		if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, "screen-assign"); err != nil {
			t.Fatalf("AssignDeviceToScreen: %v", err)
		}

		got := findDeviceByID(t, svc, dev.ID)
		if got.ScreenID == nil {
			t.Fatalf("after assign, ScreenID = nil, want %q", "screen-assign")
		}
		if *got.ScreenID != "screen-assign" {
			t.Errorf("ScreenID = %q, want %q", *got.ScreenID, "screen-assign")
		}
	})

	t.Run("empty screen id clears existing assignment", func(t *testing.T) {
		t.Parallel()
		svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
		seedTestScreen(t, sqlDB, "theme-clear", "screen-clear", "screen-clear-name")

		dev, _, err := svc.CreateDevice(context.Background(), "clearable", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice: %v", err)
		}
		if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, "screen-clear"); err != nil {
			t.Fatalf("AssignDeviceToScreen(set): %v", err)
		}
		// Sanity: the assignment took.
		got := findDeviceByID(t, svc, dev.ID)
		if got.ScreenID == nil || *got.ScreenID != "screen-clear" {
			t.Fatalf("pre-clear ScreenID = %+v, want %q", got.ScreenID, "screen-clear")
		}

		// Now clear with empty string.
		if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, ""); err != nil {
			t.Fatalf("AssignDeviceToScreen(clear): %v", err)
		}
		got = findDeviceByID(t, svc, dev.ID)
		if got.ScreenID != nil {
			t.Errorf("post-clear ScreenID = %q, want nil", *got.ScreenID)
		}
	})

	t.Run("unknown device id returns ErrDeviceNotFound", func(t *testing.T) {
		t.Parallel()
		svc, _, _ := newDeviceTestService(t, time.Minute)

		err := svc.AssignDeviceToScreen(context.Background(), "no-such-device-id", "")
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("AssignDeviceToScreen(unknown, empty) = %v, want ErrDeviceNotFound", err)
		}

		// Also covers the non-empty screen id path: the device lookup misses
		// before the FK check ever fires.
		err = svc.AssignDeviceToScreen(context.Background(), "no-such-device-id", "anything")
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("AssignDeviceToScreen(unknown, nonempty) = %v, want ErrDeviceNotFound", err)
		}
	})

	t.Run("screen delete cascades to NULL on every assigned device", func(t *testing.T) {
		t.Parallel()
		svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
		seedTestScreen(t, sqlDB, "theme-casc", "screen-casc", "screen-casc-name")

		devA, _, err := svc.CreateDevice(context.Background(), "cascade-a", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice(a): %v", err)
		}
		devB, _, err := svc.CreateDevice(context.Background(), "cascade-b", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice(b): %v", err)
		}
		if err := svc.AssignDeviceToScreen(context.Background(), devA.ID, "screen-casc"); err != nil {
			t.Fatalf("AssignDeviceToScreen(a): %v", err)
		}
		if err := svc.AssignDeviceToScreen(context.Background(), devB.ID, "screen-casc"); err != nil {
			t.Fatalf("AssignDeviceToScreen(b): %v", err)
		}

		// Delete the screen via raw SQL (we cannot import internal/screens).
		if _, err := sqlDB.Exec("DELETE FROM screens WHERE id = ?", "screen-casc"); err != nil {
			t.Fatalf("DELETE screen: %v", err)
		}

		gotA := findDeviceByID(t, svc, devA.ID)
		if gotA.ScreenID != nil {
			t.Errorf("device A ScreenID = %q after screen delete, want nil", *gotA.ScreenID)
		}
		gotB := findDeviceByID(t, svc, devB.ID)
		if gotB.ScreenID != nil {
			t.Errorf("device B ScreenID = %q after screen delete, want nil", *gotB.ScreenID)
		}
	})
}

// TestAssignDeviceToScreen_AdversarialInputs pins the documented behaviour
// of the service for unusual inputs the upcoming admin handler (TASK-029) and
// render handler (TASK-030) will need to rely on.
func TestAssignDeviceToScreen_AdversarialInputs(t *testing.T) {
	t.Parallel()

	t.Run("empty deviceID returns ErrDeviceNotFound (does not no-op-update all rows)", func(t *testing.T) {
		t.Parallel()
		// Empty deviceID against a non-empty database. The UPDATE has WHERE
		// id = ?; if some future change relaxed that, an empty string could
		// match every row. The expected behaviour is zero rows affected ->
		// ErrDeviceNotFound, and no other device is mutated.
		svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
		seedTestScreen(t, sqlDB, "theme-empty-id", "screen-empty-id", "screen-empty-id")
		dev, _, err := svc.CreateDevice(context.Background(), "untouched", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice: %v", err)
		}
		if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, "screen-empty-id"); err != nil {
			t.Fatalf("seed-assign: %v", err)
		}

		err = svc.AssignDeviceToScreen(context.Background(), "", "")
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("AssignDeviceToScreen(empty, empty) = %v, want ErrDeviceNotFound", err)
		}
		err = svc.AssignDeviceToScreen(context.Background(), "", "screen-empty-id")
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("AssignDeviceToScreen(empty, nonempty) = %v, want ErrDeviceNotFound", err)
		}

		// The pre-existing device must STILL be assigned to its screen.
		got := findDeviceByID(t, svc, dev.ID)
		if got.ScreenID == nil || *got.ScreenID != "screen-empty-id" {
			t.Errorf("pre-existing device assignment was mutated by empty-id call: ScreenID=%v", got.ScreenID)
		}
	})

	t.Run("nonexistent screen ID returns a non-nil error (FK secondary defence)", func(t *testing.T) {
		t.Parallel()
		// The architecture (ADR-009 + task) treats the FK as a secondary
		// defence: callers MUST pre-validate the screen exists. If a future
		// change drops the FK, we want this test to FAIL loudly so the
		// secondary defence is preserved. We do NOT pin the error type --
		// the task documents that the caller pre-validates, so the wrapped
		// constraint failure is acceptable here. We pin only "non-nil".
		svc, _, _, creator := newDeviceTestServiceWithDB(t, time.Minute)
		dev, _, err := svc.CreateDevice(context.Background(), "fk-defence", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice: %v", err)
		}

		err = svc.AssignDeviceToScreen(context.Background(), dev.ID, "no-such-screen")
		if err == nil {
			t.Fatal("AssignDeviceToScreen(valid device, nonexistent screen) = nil, want a non-nil error from the FK secondary defence")
		}

		// And the device row must NOT have been updated.
		got := findDeviceByID(t, svc, dev.ID)
		if got.ScreenID != nil {
			t.Errorf("device ScreenID = %q after rejected FK assign, want nil", *got.ScreenID)
		}
	})

	t.Run("nonexistent screen ID error does not leak the screen id verbatim", func(t *testing.T) {
		t.Parallel()
		// Defence in depth: a future caller might log err.Error() at info
		// level. If the FK constraint message ever started including the
		// rejected value, a 1MB malicious screen id (or PII) would land in
		// the log. SQLite's current behaviour is a constant-length
		// "FOREIGN KEY constraint failed (787)" -- pin that.
		svc, _, _, creator := newDeviceTestServiceWithDB(t, time.Minute)
		dev, _, err := svc.CreateDevice(context.Background(), "leak-check", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice: %v", err)
		}
		// A clearly-identifiable token that would be obvious if it leaked.
		canary := "CANARY-DO-NOT-LEAK-THIS-STRING"
		err = svc.AssignDeviceToScreen(context.Background(), dev.ID, canary)
		if err == nil {
			t.Fatal("expected error for nonexistent screen")
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("error message leaked the rejected screen id verbatim: %q", err.Error())
		}
	})

	t.Run("idempotent: re-assigning the same screen succeeds; clearing already-cleared succeeds", func(t *testing.T) {
		t.Parallel()
		// SPEC-007 commentary + task R6 docstring: "a no-op re-assign is not
		// an error". We pin that for both directions (set->set, clear->clear).
		svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
		seedTestScreen(t, sqlDB, "theme-idem", "screen-idem", "screen-idem")
		dev, _, err := svc.CreateDevice(context.Background(), "idem", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice: %v", err)
		}
		// Clear-while-already-NULL must succeed.
		if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, ""); err != nil {
			t.Errorf("first clear: %v", err)
		}
		// Assign, then re-assign same value.
		if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, "screen-idem"); err != nil {
			t.Errorf("first assign: %v", err)
		}
		if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, "screen-idem"); err != nil {
			t.Errorf("second assign (same value): %v", err)
		}
		got := findDeviceByID(t, svc, dev.ID)
		if got.ScreenID == nil || *got.ScreenID != "screen-idem" {
			t.Errorf("post-idempotent ScreenID = %v, want %q", got.ScreenID, "screen-idem")
		}
	})

	t.Run("sql metacharacters in deviceID do not corrupt the UPDATE", func(t *testing.T) {
		t.Parallel()
		// The query is parameterized; this test pins that nothing slips
		// through string concatenation. If any layer were to switch to
		// fmt.Sprintf, this would either drop the devices table or report
		// rows affected != 0 against an unrelated device.
		svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
		seedTestScreen(t, sqlDB, "theme-sql", "screen-sql", "screen-sql")
		safe, _, err := svc.CreateDevice(context.Background(), "safe", creator.ID)
		if err != nil {
			t.Fatalf("CreateDevice safe: %v", err)
		}
		if err := svc.AssignDeviceToScreen(context.Background(), safe.ID, "screen-sql"); err != nil {
			t.Fatalf("safe assign: %v", err)
		}

		evil := "ev'il'); DROP TABLE devices;--"
		err = svc.AssignDeviceToScreen(context.Background(), evil, "")
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("AssignDeviceToScreen(evil, empty) = %v, want ErrDeviceNotFound", err)
		}

		// Pre-existing device must still be there and still assigned.
		got := findDeviceByID(t, svc, safe.ID)
		if got.ScreenID == nil || *got.ScreenID != "screen-sql" {
			t.Errorf("safe device assignment mutated by metachar input: ScreenID=%v", got.ScreenID)
		}
	})
}

// TestValidateDeviceToken_ReturnsScreenIDAfterAssignment pins that the
// GetDeviceByTokenHash SELECT includes screen_id and that the mapper preserves
// it. The render handler (TASK-030) authenticates a device via its bearer
// token and reads dev.ScreenID directly off the returned struct -- if the
// SELECT list ever loses the column, every device silently appears
// "unassigned" and the entire render pipeline degrades to the placeholder
// without any test surface catching it.
func TestValidateDeviceToken_ReturnsScreenIDAfterAssignment(t *testing.T) {
	t.Parallel()
	svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
	seedTestScreen(t, sqlDB, "theme-vt", "screen-vt", "screen-vt")
	dev, rawToken, err := svc.CreateDevice(context.Background(), "vt-device", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	// Before assignment, ValidateDeviceToken must return ScreenID=nil.
	got, err := svc.ValidateDeviceToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("ValidateDeviceToken before assign: %v", err)
	}
	if got.ScreenID != nil {
		t.Errorf("pre-assign ValidateDeviceToken ScreenID = %q, want nil", *got.ScreenID)
	}

	// After assignment, the token-hash lookup must include screen_id.
	if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, "screen-vt"); err != nil {
		t.Fatalf("AssignDeviceToScreen: %v", err)
	}
	got, err = svc.ValidateDeviceToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("ValidateDeviceToken after assign: %v", err)
	}
	if got.ScreenID == nil {
		t.Fatal("post-assign ValidateDeviceToken ScreenID = nil; GetDeviceByTokenHash SELECT may have lost screen_id")
	}
	if *got.ScreenID != "screen-vt" {
		t.Errorf("post-assign ScreenID = %q, want %q", *got.ScreenID, "screen-vt")
	}

	// And after clearing, it must go back to nil (i.e., NULL round-trips
	// through ValidateDeviceToken, not just the original create path).
	if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, ""); err != nil {
		t.Fatalf("AssignDeviceToScreen clear: %v", err)
	}
	got, err = svc.ValidateDeviceToken(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("ValidateDeviceToken after clear: %v", err)
	}
	if got.ScreenID != nil {
		t.Errorf("post-clear ValidateDeviceToken ScreenID = %q, want nil", *got.ScreenID)
	}
}

// TestAssignDeviceToScreen_Concurrent verifies the service serialises
// concurrent assignments without deadlock or data race. The render handler
// will be called concurrently by every paired device on a busy household; if
// the admin reassigns a device while a render is in flight, the assign path
// must not collide with the read path on the same row.
func TestAssignDeviceToScreen_Concurrent(t *testing.T) {
	t.Parallel()
	svc, _, sqlDB, creator := newDeviceTestServiceWithDB(t, time.Minute)
	seedTestScreen(t, sqlDB, "theme-cc", "screen-cc-A", "screen-cc-A")
	if _, err := sqlDB.Exec(
		"INSERT INTO screens (id, name, theme_id, rotation_interval_seconds) VALUES (?, ?, ?, 30)",
		"screen-cc-B", "screen-cc-B", "theme-cc",
	); err != nil {
		t.Fatalf("seed second screen: %v", err)
	}
	dev, _, err := svc.CreateDevice(context.Background(), "concurrent", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	const N = 32
	var wg sync.WaitGroup
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			screen := "screen-cc-A"
			if i%2 == 0 {
				screen = "screen-cc-B"
			}
			if err := svc.AssignDeviceToScreen(context.Background(), dev.ID, screen); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent assign returned error: %v", err)
	}

	// The final value must be one of the two valid screens (not corrupted).
	got := findDeviceByID(t, svc, dev.ID)
	if got.ScreenID == nil {
		t.Fatal("post-concurrent ScreenID = nil; race reverted the row")
	}
	if *got.ScreenID != "screen-cc-A" && *got.ScreenID != "screen-cc-B" {
		t.Errorf("post-concurrent ScreenID = %q, want one of screen-cc-A or screen-cc-B", *got.ScreenID)
	}
}
