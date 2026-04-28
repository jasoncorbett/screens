package auth

import (
	"context"
	"testing"
)

func TestIdentityFromContext(t *testing.T) {
	t.Parallel()

	t.Run("absent returns nil", func(t *testing.T) {
		t.Parallel()
		if got := IdentityFromContext(context.Background()); got != nil {
			t.Errorf("IdentityFromContext() = %v, want nil", got)
		}
	})

	t.Run("round-trip preserves value", func(t *testing.T) {
		t.Parallel()
		id := &Identity{Kind: IdentityAdmin, User: &User{ID: "u-1"}}
		ctx := ContextWithIdentity(context.Background(), id)
		got := IdentityFromContext(ctx)
		if got == nil {
			t.Fatal("IdentityFromContext() = nil, want non-nil")
		}
		if got != id {
			t.Errorf("IdentityFromContext() returned a different pointer")
		}
		if !got.IsAdmin() {
			t.Errorf("IsAdmin() = false, want true")
		}
		if got.User == nil || got.User.ID != "u-1" {
			t.Errorf("User round-trip lost data: %+v", got.User)
		}
	})
}

func TestDeviceFromContext(t *testing.T) {
	t.Parallel()

	t.Run("absent returns nil", func(t *testing.T) {
		t.Parallel()
		if got := DeviceFromContext(context.Background()); got != nil {
			t.Errorf("DeviceFromContext() = %v, want nil", got)
		}
	})

	t.Run("round-trip preserves value", func(t *testing.T) {
		t.Parallel()
		dev := &Device{ID: "dev-1", Name: "kitchen"}
		ctx := ContextWithDevice(context.Background(), dev)
		got := DeviceFromContext(ctx)
		if got == nil {
			t.Fatal("DeviceFromContext() = nil, want non-nil")
		}
		if got != dev {
			t.Errorf("DeviceFromContext() returned a different pointer")
		}
		if got.Name != "kitchen" {
			t.Errorf("Name round-trip lost data: %q", got.Name)
		}
	})
}
