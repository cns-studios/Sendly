package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/services"
	"sendly/internal/storage"
)

// The CNS Accounts app as a Sendly approver device, against the real schema:
// the kind is stored once and can't be dropped, the device can withdraw its
// own enrollment, remove itself (and only itself), and never mint or recover
// the identity key.
func TestLiveApproverDevice(t *testing.T) {
	if os.Getenv("SENDLY_LIVE_INTEGRATION") != "1" {
		t.Skip("set SENDLY_LIVE_INTEGRATION=1 to run against live Postgres")
	}

	root := t.TempDir()
	t.Setenv("DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("CHUNK_DIR", filepath.Join(root, "chunks"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.NewPostgres(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.RunMigrations(ctx, cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}

	user := time.Now().UnixNano()
	id := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8%03d-%012d", n, user%1000000000000) }
	browser, phone := id(1), id(2)
	req := func(deviceID, kind string) models.DeviceRegisterRequest {
		return models.DeviceRegisterRequest{
			DeviceID: deviceID, DeviceLabel: kind, DeviceKind: kind, KeyAlgorithm: "RSA-OAEP-2048",
			PublicKeyJWK: json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, deviceID)),
		}
	}
	identity := services.DeviceIdentity{DB: db}

	first := req(browser, "")
	first.IdentityPublicKeyJWK = json.RawMessage(`{"kty":"RSA","n":"identity","e":"AQAB"}`)
	first.WrappedIdentityPrivateKeyB64 = "d3JhcHBlZA=="
	first.IdentityKeyWrapAlg = "RSA-OAEP-2048+AES-GCM-256-v1"
	if _, err := identity.Register(ctx, user, first, false); err != nil {
		t.Fatalf("browser registration: %v", err)
	}

	result, err := identity.Register(ctx, user, req(phone, models.DeviceKindApprover), false)
	if err != nil || !result.NeedsEnrollment {
		t.Fatalf("approver registration: %#v %v", result, err)
	}
	if kind, _ := db.GetDeviceKind(ctx, user, phone); kind != models.DeviceKindApprover {
		t.Fatalf("stored kind %q", kind)
	}
	if _, err := identity.Register(ctx, user, req(phone, ""), true); err != models.ErrApproverDeviceRestricted {
		t.Fatalf("approver recovered by dropping its kind: %v", err)
	}

	enrollment, err := identity.CreateEnrollment(ctx, user, phone)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.CancelEnrollment(ctx, user, enrollment.ID, browser); err != models.ErrEnrollmentNotPending {
		t.Fatalf("another device cancelled the enrollment: %v", err)
	}
	if err := identity.CancelEnrollment(ctx, user, enrollment.ID, phone); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if pending, _ := db.GetPendingEnrollmentForDevice(ctx, user, phone); pending != nil {
		t.Fatal("cancelled enrollment still pending")
	}

	enrollment, err = identity.CreateEnrollment(ctx, user, phone)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Approve(ctx, user, enrollment.ID, models.ApproveEnrollmentRequest{
		ApproverDeviceID: browser, VerificationCode: enrollment.VerificationCode, IdentityKeyVersion: 1,
		WrappedIdentityPrivateKeyB64: "Zm9yLXBob25l", IdentityKeyWrapAlg: "RSA-OAEP-2048+AES-GCM-256-v1",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if trusted, _ := identity.IsTrusted(ctx, user, phone); !trusted {
		t.Fatal("approved approver not trusted")
	}

	if err := identity.RemoveApprover(ctx, user, browser); err != models.ErrDeviceNotFound {
		t.Fatalf("standard device removed through the approver path: %v", err)
	}
	if err := identity.RemoveApprover(ctx, user+1, phone); err != models.ErrDeviceNotFound {
		t.Fatalf("another account removed the approver: %v", err)
	}
	if err := identity.RemoveApprover(ctx, user, phone); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if trusted, _ := identity.IsTrusted(ctx, user, phone); trusted {
		t.Fatal("removed approver still trusted")
	}
	if _, err := db.GetUserIdentityKeyDeviceEnvelope(ctx, user, phone, 1); err != models.ErrDeviceEnvelopeNotFound {
		t.Fatalf("removed approver kept its identity key copy: %v", err)
	}
	if trusted, _ := identity.IsTrusted(ctx, user, browser); !trusted {
		t.Fatal("removing the approver affected the browser")
	}
}
