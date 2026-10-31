package storage

import (
	"database/sql"
	"testing"
	"time"

	"sendly/internal/models"
)

func boolPtr(v bool) *bool { return &v }

func TestNextUserStateNewUser(t *testing.T) {
	now := time.Now()
	got := NextUserState(nil, models.CNSUserProfile{CNSUserID: 5, Username: "alice", AvatarURL: "https://a", ObservedAt: now.Add(-time.Second)}, nil, now)
	if got.CNSUserID != 5 || got.Username != "alice" || got.AvatarURL.String != "https://a" || got.Status != models.UserStatusActive {
		t.Fatalf("unexpected new row: %+v", got)
	}
	if !got.LastSyncedAt.Equal(now.Add(-time.Second)) || !got.ProfileObservedAt.Time.Equal(now.Add(-time.Second)) {
		t.Fatalf("timestamps should be the observation time: %+v", got)
	}

	locked := NextUserState(nil, models.CNSUserProfile{CNSUserID: 6, Username: "bob", ObservedAt: now}, boolPtr(true), now)
	if locked.Status != models.UserStatusInactive || locked.InactiveReason.String != models.InactiveReasonLocked || !locked.DeactivatedAt.Valid {
		t.Fatalf("locked user should start inactive/locked: %+v", locked)
	}
}

func TestNextUserStateIgnoresOlderProfiles(t *testing.T) {
	now := time.Now()
	cur := &models.User{
		CNSUserID: 1, Username: "new-name", Status: models.UserStatusActive,
		LastSyncedAt:      now.Add(-time.Minute),
		ProfileObservedAt: sql.NullTime{Time: now.Add(-time.Minute), Valid: true},
	}
	got := NextUserState(cur, models.CNSUserProfile{CNSUserID: 1, Username: "old-name", ObservedAt: now.Add(-time.Hour)}, nil, now)
	if got.Username != "new-name" || !got.ProfileObservedAt.Time.Equal(now.Add(-time.Minute)) {
		t.Fatalf("an older observation overwrote the profile: %+v", got)
	}
	if !got.LastSyncedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("an older observation moved last_synced_at: %v", got.LastSyncedAt)
	}

	same := NextUserState(cur, models.CNSUserProfile{CNSUserID: 1, Username: "same-instant", ObservedAt: now.Add(-time.Minute)}, nil, now)
	if same.Username != "same-instant" {
		t.Fatalf("an equally recent observation should apply: %+v", same)
	}
}

func TestNextUserStateClearsAvatar(t *testing.T) {
	now := time.Now()
	cur := &models.User{CNSUserID: 1, Username: "a", AvatarURL: sql.NullString{String: "https://old", Valid: true}, Status: models.UserStatusActive}
	got := NextUserState(cur, models.CNSUserProfile{CNSUserID: 1, Username: "a", ObservedAt: now}, nil, now)
	if got.AvatarURL.Valid {
		t.Fatalf("a profile without avatar should clear it: %+v", got.AvatarURL)
	}
}

func TestNextUserStateCapsFutureObservations(t *testing.T) {
	now := time.Now()
	got := NextUserState(nil, models.CNSUserProfile{CNSUserID: 1, Username: "a", ObservedAt: now.Add(time.Hour)}, nil, now)
	if !got.LastSyncedAt.Equal(now) || !got.ProfileObservedAt.Time.Equal(now) {
		t.Fatalf("a CNS clock ahead of ours must not confirm the future: %+v", got)
	}
}

func TestNextUserStateStatusTransitions(t *testing.T) {
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	stale := models.User{
		CNSUserID: 1, Username: "a", Status: models.UserStatusInactive, LastSyncedAt: old,
		InactiveReason: sql.NullString{String: models.InactiveReasonStale, Valid: true},
		DeactivatedAt:  sql.NullTime{Time: old, Valid: true},
	}
	lockedRow := stale
	lockedRow.InactiveReason = sql.NullString{String: models.InactiveReasonLocked, Valid: true}
	profile := models.CNSUserProfile{CNSUserID: 1, Username: "a", ObservedAt: now}

	cases := []struct {
		name       string
		cur        models.User
		profile    models.CNSUserProfile
		locked     *bool
		wantStatus string
		wantReason string
	}{
		{"directory confirms a stale row", stale, profile, nil, models.UserStatusActive, ""},
		{"an older event doesn't revive a stale row", stale, models.CNSUserProfile{CNSUserID: 1, Username: "a", ObservedAt: old.Add(-time.Hour)}, nil, models.UserStatusInactive, models.InactiveReasonStale},
		{"directory doesn't unlock a locked row", lockedRow, profile, nil, models.UserStatusInactive, models.InactiveReasonLocked},
		{"own profile unlocks", lockedRow, profile, boolPtr(false), models.UserStatusActive, ""},
		{"own profile locks an active row", models.User{CNSUserID: 1, Username: "a", Status: models.UserStatusActive}, profile, boolPtr(true), models.UserStatusInactive, models.InactiveReasonLocked},
		{"own profile turns stale into locked", stale, profile, boolPtr(true), models.UserStatusInactive, models.InactiveReasonLocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := tc.cur
			got := NextUserState(&cur, tc.profile, tc.locked, now)
			if got.Status != tc.wantStatus || got.InactiveReason.String != tc.wantReason {
				t.Fatalf("got %s/%q, want %s/%q", got.Status, got.InactiveReason.String, tc.wantStatus, tc.wantReason)
			}
			if got.Status == models.UserStatusActive && (got.DeactivatedAt.Valid || got.InactiveReason.Valid) {
				t.Fatalf("active row kept inactive fields: %+v", got)
			}
			if got.Status == models.UserStatusInactive && !got.DeactivatedAt.Valid {
				t.Fatalf("inactive row has no deactivated_at: %+v", got)
			}
		})
	}
}

func TestDeletedUserStateErasesProfile(t *testing.T) {
	now := time.Now()
	created := now.Add(-100 * time.Hour)
	cur := &models.User{
		CNSUserID: 3, Username: "carol", AvatarURL: sql.NullString{String: "https://c", Valid: true},
		Status: models.UserStatusActive, CreatedAt: created, LastSyncedAt: now.Add(-time.Hour),
		ProfileObservedAt: sql.NullTime{Time: now.Add(-time.Hour), Valid: true},
	}
	got := DeletedUserState(cur, 3, now)
	if got.Username != "" || got.AvatarURL.Valid || got.ProfileObservedAt.Valid {
		t.Fatalf("profile not erased: %+v", got)
	}
	if got.Status != models.UserStatusDeleted || !got.DeletedAt.Time.Equal(now) || !got.DeactivatedAt.Valid {
		t.Fatalf("not a tombstone: %+v", got)
	}

	fresh := DeletedUserState(nil, 9, now)
	if fresh.CNSUserID != 9 || fresh.Status != models.UserStatusDeleted || fresh.Username != "" {
		t.Fatalf("tombstone for an unseen user: %+v", fresh)
	}
}
