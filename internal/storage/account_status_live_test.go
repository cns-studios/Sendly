package storage

import (
	"testing"
	"time"

	"sendly/internal/accountstatus"
	"sendly/internal/models"
)

func TestLiveAccountStatusIsStoredAndHidesFromSearch(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	id := liveUserID()
	cleanupUsers(t, db, id)
	name := liveUsername("Restricted")

	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: name, ObservedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, id); u.AccountStatus != string(accountstatus.Active) {
		t.Fatalf("new rows start active: %+v", u)
	}

	observed := time.Now()
	if err := db.RecordAccountStatus(ctx, id, models.CNSAccountStatus{Status: accountstatus.Limited, ObservedAt: observed}); err != nil {
		t.Fatal(err)
	}
	if found, _ := db.SearchUsersByUsername(ctx, name, 10); len(found) != 0 {
		t.Fatalf("limited users must not show up in search: %+v", found)
	}

	if err := db.RecordAccountStatus(ctx, id, models.CNSAccountStatus{Status: accountstatus.Active, ObservedAt: observed.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, id); u.EffectiveAccountStatus(time.Now()) != accountstatus.Limited {
		t.Fatalf("an older observation must not overwrite a newer one: %+v", u)
	}

	past := time.Now().Add(-time.Second)
	if err := db.RecordAccountStatus(ctx, id, models.CNSAccountStatus{Status: accountstatus.Limited, ExpiresAt: &past, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if found, _ := db.SearchUsersByUsername(ctx, name, 10); len(found) != 1 {
		t.Fatalf("an expired restriction no longer hides the user: %+v", found)
	}
}

func TestLiveWebhookAppliesAccountStatus(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	id := liveUserID()
	cleanupUsers(t, db, id)
	name := liveUsername("Hooked")

	applied, err := db.ApplyCNSUserUpdated(ctx, "evt-status-"+name, models.CNSUserProfile{
		CNSUserID: id, Username: name, ObservedAt: time.Now(),
		AccountStatus: &models.CNSAccountStatus{Status: accountstatus.VeryLimited, ObservedAt: time.Now()},
	})
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if u := mustGetUser(t, ctx, db, id); u.AccountStatus != string(accountstatus.VeryLimited) {
		t.Fatalf("webhook status not stored: %+v", u)
	}
}
