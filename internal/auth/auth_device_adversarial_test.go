package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// TestCreateDevice_RejectsNonExistentCreator verifies that the FK constraint on
// devices.created_by is surfaced as an error and no row is inserted. A bug
// here would let a stale or fabricated user id "own" a device.
func TestCreateDevice_RejectsNonExistentCreator(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	_, _, err := svc.CreateDevice(context.Background(), "ghost-owner", "no-such-user")
	if err == nil {
		t.Fatal("CreateDevice with unknown createdBy: expected error, got nil")
	}

	devices, err := svc.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("ListDevices len = %d, want 0 (no row should be inserted on FK failure)", len(devices))
	}
}

// TestCreateDevice_RejectsEmptyCreator confirms that an empty createdBy is
// also rejected by the FK (empty string is not a valid user id).
func TestCreateDevice_RejectsEmptyCreator(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	_, _, err := svc.CreateDevice(context.Background(), "no-creator", "")
	if err == nil {
		t.Fatal("CreateDevice with empty createdBy: expected error, got nil")
	}

	devices, _ := svc.ListDevices(context.Background())
	if len(devices) != 0 {
		t.Errorf("ListDevices len = %d, want 0", len(devices))
	}
}

// TestCreateDevice_LongName confirms a 1MB name does not crash and is
// preserved verbatim in the row.
func TestCreateDevice_LongName(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	longName := strings.Repeat("x", 1<<20) // 1 MiB
	dev, _, err := svc.CreateDevice(context.Background(), longName, creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice with 1MB name: %v", err)
	}

	row, err := q.GetDeviceByID(context.Background(), dev.ID)
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if len(row.Name) != len(longName) {
		t.Errorf("persisted name length = %d, want %d", len(row.Name), len(longName))
	}
}

// TestCreateDevice_UnicodeName checks that emoji and right-to-left text are
// stored intact -- the trim-then-store path must not mangle multi-byte runes.
func TestCreateDevice_UnicodeName(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	name := "kitchen \U0001F4FA \u202Eright-to-left\u202C"
	dev, _, err := svc.CreateDevice(context.Background(), name, creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice unicode name: %v", err)
	}
	row, err := q.GetDeviceByID(context.Background(), dev.ID)
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if row.Name != name {
		t.Errorf("persisted name = %q, want %q", row.Name, name)
	}
}

// TestCreateDevice_NameWithNullBytes verifies SQLite accepts the embedded NUL
// and returns it intact. (The middleware should not be blindly logging the
// device name because it could carry control bytes, but persistence-layer
// behaviour is well-defined.)
func TestCreateDevice_NameWithNullBytes(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	name := "name\x00with\x00nulls"
	dev, _, err := svc.CreateDevice(context.Background(), name, creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice name-with-nulls: %v", err)
	}
	row, _ := q.GetDeviceByID(context.Background(), dev.ID)
	if row.Name != name {
		t.Errorf("persisted name = %q, want %q", row.Name, name)
	}
}

// TestCreateDevice_TimestampsAreUTC verifies the parsed CreatedAt is in UTC,
// not local. Cross-zone deployments could otherwise see drift in the
// management UI.
func TestCreateDevice_TimestampsAreUTC(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, _, err := svc.CreateDevice(context.Background(), "tz", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if dev.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt location = %s, want UTC", dev.CreatedAt.Location())
	}
}

// TestValidateDeviceToken_EmptyToken proves that hashing an empty string is
// safe -- it produces a deterministic hash that cannot match any device, so
// the call returns ErrDeviceNotFound without a panic and without leaking the
// (empty) input.
func TestValidateDeviceToken_EmptyToken(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	// Seed at least one device so the lookup table isn't trivially empty.
	if _, _, err := svc.CreateDevice(context.Background(), "victim", creator.ID); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	_, err := svc.ValidateDeviceToken(context.Background(), "")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("ValidateDeviceToken(\"\") = %v, want ErrDeviceNotFound", err)
	}
}

// TestValidateDeviceToken_LongToken verifies a 1MB token does not panic and
// just maps to ErrDeviceNotFound.
func TestValidateDeviceToken_LongToken(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	huge := strings.Repeat("a", 1<<20)
	_, err := svc.ValidateDeviceToken(context.Background(), huge)
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("ValidateDeviceToken(huge) = %v, want ErrDeviceNotFound", err)
	}
}

// TestValidateDeviceToken_SQLMetacharactersDoNotInject confirms the lookup is
// parameterised. A bug here (string concatenation) would let the input drop
// the table or return spurious rows. We assert the call returns
// ErrDeviceNotFound (no row matched) AND that the devices table is intact
// afterwards.
func TestValidateDeviceToken_SQLMetacharactersDoNotInject(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	// Seed a device so we can prove the table survives the malicious input.
	if _, _, err := svc.CreateDevice(context.Background(), "intact", creator.ID); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	for _, evil := range []string{
		"' OR 1=1 --",
		"'; DROP TABLE devices; --",
		"\"; DELETE FROM devices; --",
		"\x00admin",
	} {
		_, err := svc.ValidateDeviceToken(context.Background(), evil)
		if !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("ValidateDeviceToken(%q) = %v, want ErrDeviceNotFound", evil, err)
		}
	}

	// Table still has exactly one row.
	devices, err := svc.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices after injection attempts: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("ListDevices len = %d, want 1 (table corrupted by injection?)", len(devices))
	}
}

// TestErrorMessages_DoNotLeakRawTokenOrHash verifies that none of the wrapped
// errors include the raw token or the token hash. A bug here could put a
// usable credential into a log line or an HTTP response.
func TestErrorMessages_DoNotLeakRawTokenOrHash(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	// Trigger validation against an unknown token.
	rawToken, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	_, err = svc.ValidateDeviceToken(context.Background(), rawToken)
	if err == nil {
		t.Fatal("expected ErrDeviceNotFound, got nil")
	}
	if strings.Contains(err.Error(), rawToken) {
		t.Errorf("error message leaks raw token: %v", err)
	}
	if strings.Contains(err.Error(), HashToken(rawToken)) {
		t.Errorf("error message leaks token hash: %v", err)
	}

	// Trigger create with bad creator -- the wrapped error must not contain
	// the raw or hashed token either.
	_, raw2, err := svc.CreateDevice(context.Background(), "bad-creator", "no-such-user")
	if err == nil {
		t.Fatal("expected FK error, got nil")
	}
	if raw2 != "" {
		t.Errorf("CreateDevice returned non-empty raw token despite error: %q", raw2)
	}

	// Trigger rotate on an unknown id; we have no raw to compare so we just
	// confirm the error is exactly ErrDeviceNotFound (no extra context that
	// might leak in the future).
	_, err = svc.RotateDeviceToken(context.Background(), "no-such-id")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("RotateDeviceToken(unknown) = %v, want ErrDeviceNotFound", err)
	}
}

// TestRevokeDevice_Idempotent documents that calling RevokeDevice twice does
// not error -- the second call is a no-op because GetDeviceByID still finds
// the (now-revoked) row and the UPDATE has WHERE revoked_at IS NULL. This is
// the desired behaviour: the spec does not require a "double-revoke" error.
func TestRevokeDevice_Idempotent(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	dev, _, err := svc.CreateDevice(context.Background(), "two-revokes", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	if err := svc.RevokeDevice(context.Background(), dev.ID); err != nil {
		t.Fatalf("first RevokeDevice: %v", err)
	}
	row1, _ := q.GetDeviceByID(context.Background(), dev.ID)
	first := row1.RevokedAt

	// Sleep long enough that a second call would visibly bump revoked_at if
	// the UPDATE were not gated on revoked_at IS NULL.
	time.Sleep(1100 * time.Millisecond)

	if err := svc.RevokeDevice(context.Background(), dev.ID); err != nil {
		t.Errorf("second RevokeDevice: %v, want nil (idempotent)", err)
	}
	row2, _ := q.GetDeviceByID(context.Background(), dev.ID)
	if row2.RevokedAt.String != first.String {
		t.Errorf("revoked_at moved on second revoke: %q -> %q (UPDATE did not honour WHERE revoked_at IS NULL)",
			first.String, row2.RevokedAt.String)
	}
}

// TestMarkDeviceSeen_UnknownDevice exercises the best-effort contract: a
// missing id returns nil, not an error. (Otherwise the auth path would log
// noise on every cleanup-race.)
func TestMarkDeviceSeen_UnknownDevice(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	if err := svc.MarkDeviceSeen(context.Background(), "no-such-device"); err != nil {
		t.Errorf("MarkDeviceSeen(unknown) = %v, want nil (best-effort)", err)
	}
}

// TestMarkDeviceSeen_NegativeIntervalClampsToZero verifies the implementation
// does not pass through a positive seconds value to the SQL when the
// configured duration is negative -- that would make the throttle window
// extend INTO THE FUTURE and silently disable updates.
func TestMarkDeviceSeen_NegativeIntervalClampsToZero(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, -time.Hour)

	dev, _, err := svc.CreateDevice(context.Background(), "neg", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	if err := svc.MarkDeviceSeen(context.Background(), dev.ID); err != nil {
		t.Fatalf("MarkDeviceSeen: %v", err)
	}
	row, _ := q.GetDeviceByID(context.Background(), dev.ID)
	if !row.LastSeenAt.Valid {
		t.Errorf("last_seen_at not set with negative throttle interval -- did the seconds value pass through unclamped?")
	}
}

// TestMarkDeviceSeen_OnClosedDB returns a wrapped SQL error rather than
// silently swallowing the failure. The auth middleware treats this as
// "best-effort" but a database failure must still be observable.
func TestMarkDeviceSeen_OnClosedDB(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, _, err := svc.CreateDevice(context.Background(), "to-close", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	// Close the DB out from under the service.
	if err := svc.sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	err = svc.MarkDeviceSeen(context.Background(), dev.ID)
	if err == nil {
		t.Fatal("MarkDeviceSeen on closed DB: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "touch device seen") {
		t.Errorf("error not wrapped with context: %v", err)
	}
}

// TestRotateDeviceToken_EmptyDeviceID treats an empty id the same as an
// unknown one (the WHERE clause matches no rows).
func TestRotateDeviceToken_EmptyDeviceID(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	_, err := svc.RotateDeviceToken(context.Background(), "")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("RotateDeviceToken(\"\") = %v, want ErrDeviceNotFound", err)
	}
}

// TestRotateDeviceToken_AfterRotateThenRevoke verifies the second rotation is
// rejected once the device is revoked (defence-in-depth on top of TASK-012's
// single-rotation test).
func TestRotateDeviceToken_AfterRotateThenRevoke(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, _, err := svc.CreateDevice(context.Background(), "rrr", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if _, err := svc.RotateDeviceToken(context.Background(), dev.ID); err != nil {
		t.Fatalf("first rotate: %v", err)
	}
	if err := svc.RevokeDevice(context.Background(), dev.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.RotateDeviceToken(context.Background(), dev.ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("rotate after revoke = %v, want ErrDeviceNotFound", err)
	}
}

// TestListDevices_EmptyDatabaseReturnsEmptySliceNotNil documents the contract
// that callers may safely range over the returned slice without a nil check.
func TestListDevices_EmptyDatabaseReturnsEmptySliceNotNil(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	devices, err := svc.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if devices == nil {
		t.Error("ListDevices on empty DB returned nil slice; want empty slice")
	}
	if len(devices) != 0 {
		t.Errorf("ListDevices on empty DB len = %d, want 0", len(devices))
	}
}

// TestListDevices_OnClosedDBReturnsError verifies the listing surface fails
// loudly on a broken database connection rather than swallowing the error.
func TestListDevices_OnClosedDBReturnsError(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)
	if err := svc.sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if _, err := svc.ListDevices(context.Background()); err == nil {
		t.Error("ListDevices on closed DB: expected error, got nil")
	}
}

// TestDeviceFromRow_MalformedCreatedAt verifies the parser rejects garbage
// rather than producing a Device with a zero CreatedAt that downstream code
// might display as "0001-01-01" in the UI.
func TestDeviceFromRow_MalformedCreatedAt(t *testing.T) {
	t.Parallel()
	row := db.Device{
		ID:        "x",
		Name:      "x",
		TokenHash: "h",
		CreatedBy: "u",
		CreatedAt: "this-is-not-a-timestamp",
	}
	if _, err := deviceFromRow(row); err == nil {
		t.Error("deviceFromRow: expected error on malformed CreatedAt, got nil")
	}
}

// TestDeviceFromRow_MalformedLastSeenAt likewise rejects an invalid
// last_seen_at string. (sqlc gives us sql.NullString; if Valid is true the
// String had better parse.)
func TestDeviceFromRow_MalformedLastSeenAt(t *testing.T) {
	t.Parallel()
	row := db.Device{
		ID:         "x",
		Name:       "x",
		TokenHash:  "h",
		CreatedBy:  "u",
		CreatedAt:  "2026-04-25 10:00:00",
		LastSeenAt: nullValid("bogus"),
	}
	if _, err := deviceFromRow(row); err == nil {
		t.Error("deviceFromRow: expected error on malformed LastSeenAt, got nil")
	}
}

// TestDeviceFromRow_MalformedRevokedAt likewise rejects an invalid revoked_at.
func TestDeviceFromRow_MalformedRevokedAt(t *testing.T) {
	t.Parallel()
	row := db.Device{
		ID:        "x",
		Name:      "x",
		TokenHash: "h",
		CreatedBy: "u",
		CreatedAt: "2026-04-25 10:00:00",
		RevokedAt: nullValid("nope"),
	}
	if _, err := deviceFromRow(row); err == nil {
		t.Error("deviceFromRow: expected error on malformed RevokedAt, got nil")
	}
}

// TestConcurrent_CreateDevice_50Goroutines exercises the race detector and
// confirms 50 parallel CreateDevice calls all succeed with distinct tokens
// and distinct ids. A bug in the random source or the ID generator would
// surface as a UNIQUE constraint failure on token_hash.
func TestConcurrent_CreateDevice_50Goroutines(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	const N = 50
	tokens := make([]string, N)
	ids := make([]string, N)
	errs := make([]error, N)

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			dev, raw, err := svc.CreateDevice(context.Background(), "dev", creator.ID)
			tokens[i] = raw
			ids[i] = dev.ID
			errs[i] = err
		}()
	}
	wg.Wait()

	tokenSet := make(map[string]struct{}, N)
	idSet := make(map[string]struct{}, N)
	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d CreateDevice: %v", i, e)
			continue
		}
		if _, dup := tokenSet[tokens[i]]; dup {
			t.Errorf("duplicate raw token at index %d", i)
		}
		tokenSet[tokens[i]] = struct{}{}
		if _, dup := idSet[ids[i]]; dup {
			t.Errorf("duplicate device id at index %d", i)
		}
		idSet[ids[i]] = struct{}{}
	}

	devices, err := svc.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != N {
		t.Errorf("ListDevices len = %d, want %d", len(devices), N)
	}
}

// TestConcurrent_RotateDeviceToken documents the lost-update race: 20
// concurrent rotations on the same device all return without error, but only
// the LAST writer's token validates afterwards. This is the documented
// behaviour (the spec does not promise serialisation), but the test holds the
// invariant that no goroutine returns a non-empty token alongside an error.
func TestConcurrent_RotateDeviceToken(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, _, err := svc.CreateDevice(context.Background(), "racy", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	const N = 20
	tokens := make([]string, N)
	errs := make([]error, N)

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			tok, err := svc.RotateDeviceToken(context.Background(), dev.ID)
			tokens[i] = tok
			errs[i] = err
		}()
	}
	wg.Wait()

	// Invariants: no error, every returned token is non-empty and 64 chars.
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Errorf("goroutine %d rotate: %v", i, errs[i])
		}
		if len(tokens[i]) != 64 {
			t.Errorf("goroutine %d returned token of length %d, want 64", i, len(tokens[i]))
		}
	}

	// Exactly one of the returned tokens validates successfully.
	validCount := 0
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if _, err := svc.ValidateDeviceToken(context.Background(), tok); err == nil {
			validCount++
		}
	}
	if validCount != 1 {
		t.Errorf("validating all rotation outputs found %d valid tokens, want exactly 1 (the last writer)", validCount)
	}
}

// TestConcurrent_MarkDeviceSeen confirms that 50 simultaneous touches on the
// same device do not error and produce a single non-NULL last_seen_at
// timestamp. The throttle is a safety net, not a correctness primitive, so we
// don't assert how many UPDATEs actually fired.
func TestConcurrent_MarkDeviceSeen(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	dev, _, err := svc.CreateDevice(context.Background(), "ms", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	const N = 50
	var failures atomic.Int64
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := svc.MarkDeviceSeen(context.Background(), dev.ID); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Errorf("%d concurrent MarkDeviceSeen calls failed", failures.Load())
	}

	row, err := q.GetDeviceByID(context.Background(), dev.ID)
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if !row.LastSeenAt.Valid {
		t.Error("last_seen_at not set after 50 concurrent MarkDeviceSeen calls")
	}
}

// TestConcurrent_ValidateAndRevoke runs validate-device-token in parallel with
// revoke. Either result is acceptable -- pre-revoke calls return the device,
// post-revoke calls return ErrDeviceRevoked -- but neither path should crash
// or return a wrapped/internal error, and the device must end up revoked.
func TestConcurrent_ValidateAndRevoke(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	dev, raw, err := svc.CreateDevice(context.Background(), "race", creator.ID)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N + 1)

	// One revoker.
	go func() {
		defer wg.Done()
		if err := svc.RevokeDevice(context.Background(), dev.ID); err != nil {
			t.Errorf("RevokeDevice: %v", err)
		}
	}()

	// N validators.
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.ValidateDeviceToken(context.Background(), raw)
			if err != nil && !errors.Is(err, ErrDeviceRevoked) && !errors.Is(err, ErrDeviceNotFound) {
				t.Errorf("ValidateDeviceToken: unexpected error %v", err)
			}
		}()
	}
	wg.Wait()

	row, err := q.GetDeviceByID(context.Background(), dev.ID)
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if !row.RevokedAt.Valid {
		t.Error("device not revoked after concurrent revoke")
	}
}

// TestIdentity_OutOfRangeKindIsSafe documents that an Identity with a Kind
// value outside the iota range does not crash and reports false for both
// IsAdmin and IsDevice. This guards against future maintainers adding a new
// kind without updating callers.
func TestIdentity_OutOfRangeKindIsSafe(t *testing.T) {
	t.Parallel()
	id := Identity{Kind: IdentityKind(99)}
	if id.IsAdmin() {
		t.Error("out-of-range Kind reported IsAdmin=true")
	}
	if id.IsDevice() {
		t.Error("out-of-range Kind reported IsDevice=true")
	}
	if got := id.ID(); got != "" {
		t.Errorf("out-of-range Kind ID() = %q, want \"\"", got)
	}
}

// TestContext_KeyTypeIsolation confirms that the unexported key types make
// it impossible for an external package's context value (under any string,
// int, or struct key) to collide with our keys.
func TestContext_KeyTypeIsolation(t *testing.T) {
	t.Parallel()

	type fakeKey struct{}
	ctx := context.WithValue(context.Background(), fakeKey{}, &Device{ID: "fake"})
	if got := DeviceFromContext(ctx); got != nil {
		t.Errorf("DeviceFromContext(ctx with foreign key) = %v, want nil", got)
	}
	if got := IdentityFromContext(ctx); got != nil {
		t.Errorf("IdentityFromContext(ctx with foreign key) = %v, want nil", got)
	}
}

// nullValid wraps a non-nil string in a sql.NullString.
func nullValid(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}
