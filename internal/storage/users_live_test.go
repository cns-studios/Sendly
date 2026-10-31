package storage

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sendly/internal/models"
)

var liveUserSeq atomic.Int64

// liveUserID returns an id no other test run uses.
func liveUserID() int64 {
	return 1_000_000_000_000 + time.Now().UnixNano()%1_000_000_000_000 + liveUserSeq.Add(1)
}

func liveUsername(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%1e12+liveUserSeq.Add(1))
}

func mustGetUser(t *testing.T, ctx context.Context, db *Postgres, id int64) *models.User {
	t.Helper()
	u, err := db.GetUser(ctx, id)
	if err != nil {
		t.Fatalf("get user %d: %v", id, err)
	}
	return u
}

func cleanupUsers(t *testing.T, db *Postgres, ids ...int64) {
	t.Cleanup(func() {
		for _, id := range ids {
			db.db.Exec(`DELETE FROM users WHERE cns_user_id = $1`, id)
		}
	})
}

func TestLiveRecordCNSProfileKeepsNewestObservation(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	id := liveUserID()
	cleanupUsers(t, db, id)
	now := time.Now()
	name, renamed := liveUsername("order"), liveUsername("order-renamed")

	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: name, AvatarURL: "https://a", ObservedAt: now.Add(-time.Hour)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: renamed, ObservedAt: now.Add(-time.Minute)}, nil); err != nil {
		t.Fatal(err)
	}
	// A webhook delayed by retries arrives after the newer data.
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: name, AvatarURL: "https://a", ObservedAt: now.Add(-30 * time.Minute)}, nil); err != nil {
		t.Fatal(err)
	}
	u := mustGetUser(t, ctx, db, id)
	if u.Username != renamed || u.AvatarURL.Valid || u.Status != models.UserStatusActive {
		t.Fatalf("expected the newest profile to win: %+v", u)
	}
	if d := u.ProfileObservedAt.Time.Sub(now.Add(-time.Minute)); d > time.Millisecond || d < -time.Millisecond {
		t.Fatalf("profile_observed_at = %v", u.ProfileObservedAt.Time)
	}

	locked := true
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: renamed, ObservedAt: time.Now()}, &locked); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, id); u.Status != models.UserStatusInactive || u.InactiveReason.String != models.InactiveReasonLocked || !u.DeactivatedAt.Valid {
		t.Fatalf("expected locked: %+v", u)
	}
	// The directory knows nothing about locks and must not lift one.
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: renamed, ObservedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, id); u.Status != models.UserStatusInactive {
		t.Fatalf("directory confirmation unlocked the user: %+v", u)
	}
	locked = false
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: renamed, ObservedAt: time.Now()}, &locked); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, id); u.Status != models.UserStatusActive || u.InactiveReason.Valid || u.DeactivatedAt.Valid {
		t.Fatalf("expected unlocked: %+v", u)
	}
}

func TestLiveDeletionIsFinalAndErasesProfile(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	id, unseen := liveUserID(), liveUserID()
	cleanupUsers(t, db, id, unseen)
	name := liveUsername("gone")
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: name, AvatarURL: "https://a", ObservedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	fileID := "usrdel" + randomSuffix()
	insertTestFile(t, ctx, db, fileID, false, time.Time{}, time.Now().Add(time.Hour))
	t.Cleanup(func() { db.db.Exec(`DELETE FROM files WHERE id = $1`, fileID) })
	if _, err := db.db.ExecContext(ctx, `UPDATE files SET owner_cns_user_id = $1, owner_cns_username = $2 WHERE id = $3`, id, name, fileID); err != nil {
		t.Fatal(err)
	}

	deleted, err := db.MarkUserDeleted(ctx, id)
	if err != nil || !deleted {
		t.Fatalf("MarkUserDeleted = %v, %v", deleted, err)
	}
	u := mustGetUser(t, ctx, db, id)
	if u.Status != models.UserStatusDeleted || u.Username != "" || u.AvatarURL.Valid || !u.DeletedAt.Valid {
		t.Fatalf("not a tombstone: %+v", u)
	}
	if n := count(t, ctx, db, `SELECT COUNT(*) FROM files WHERE id = $1 AND owner_cns_username IS NULL`, fileID); n != 1 {
		t.Fatal("the file's copy of the username was kept")
	}
	if found, _ := db.SearchUsersByUsername(ctx, name, 10); len(found) != 0 {
		t.Fatalf("deleted user still searchable: %+v", found)
	}

	// Nothing brings it back: not the user's own profile, not a webhook.
	unlocked := false
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: name, ObservedAt: time.Now().Add(time.Second)}, &unlocked); err != nil {
		t.Fatal(err)
	}
	if applied, err := db.ApplyCNSUserUpdated(ctx, "evt-"+randomSuffix(), models.CNSUserProfile{CNSUserID: id, Username: name, ObservedAt: time.Now()}); err != nil || !applied {
		t.Fatalf("ApplyCNSUserUpdated = %v, %v", applied, err)
	}
	if u := mustGetUser(t, ctx, db, id); u.Status != models.UserStatusDeleted || u.Username != "" {
		t.Fatalf("tombstone was overwritten: %+v", u)
	}
	if again, err := db.MarkUserDeleted(ctx, id); err != nil || again {
		t.Fatalf("second MarkUserDeleted = %v, %v", again, err)
	}

	// A deletion for a user never seen leaves a tombstone, so a user.updated
	// that CNS sent earlier but delivered later can't create them.
	if _, err := db.ApplyCNSUserDeleted(ctx, "evt-del-"+randomSuffix(), unseen); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyCNSUserUpdated(ctx, "evt-upd-"+randomSuffix(), models.CNSUserProfile{CNSUserID: unseen, Username: liveUsername("late"), ObservedAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, unseen); u.Status != models.UserStatusDeleted || u.Username != "" {
		t.Fatalf("late update recreated a deleted user: %+v", u)
	}
}

func TestLiveWebhookEventsApplyExactlyOnce(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	id := liveUserID()
	cleanupUsers(t, db, id)
	eventID := "evt-once-" + randomSuffix()
	t.Cleanup(func() { db.db.Exec(`DELETE FROM cns_webhook_events WHERE event_id LIKE 'evt-once-%'`) })

	var applied atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := db.ApplyCNSUserUpdated(ctx, eventID, models.CNSUserProfile{CNSUserID: id, Username: liveUsername("once"), ObservedAt: time.Now()})
			if err != nil {
				errs <- err
				return
			}
			if ok {
				applied.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if applied.Load() != 1 {
		t.Fatalf("event applied %d times", applied.Load())
	}
	if n := count(t, ctx, db, `SELECT COUNT(*) FROM cns_webhook_events WHERE event_id = $1`, eventID); n != 1 {
		t.Fatalf("event recorded %d times", n)
	}
}

func TestLiveConcurrentFirstSyncsDontConflict(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	id := liveUserID()
	cleanupUsers(t, db, id)
	name := liveUsername("race")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: name, ObservedAt: time.Now()}, nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if u := mustGetUser(t, ctx, db, id); u.Username != name {
		t.Fatalf("unexpected row: %+v", u)
	}
}

func TestLiveTakenUsernameLeavesTheOldHolder(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	oldHolder, newHolder := liveUserID(), liveUserID()
	cleanupUsers(t, db, oldHolder, newHolder)
	name := liveUsername("Shared")

	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: oldHolder, Username: name, ObservedAt: time.Now().Add(-time.Hour)}, nil); err != nil {
		t.Fatal(err)
	}
	// CNS let someone else take the name (the old holder renamed or was
	// deleted, and Sendly missed it). Case differs: CNS compares names
	// case-insensitively.
	upper := "s" + name[1:]
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: newHolder, Username: upper, ObservedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}

	found, err := db.SearchUsersByUsername(ctx, name, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].CNSUserID != newHolder {
		t.Fatalf("search should show only the current holder: %+v", found)
	}
	old := mustGetUser(t, ctx, db, oldHolder)
	if old.Status != models.UserStatusInactive || old.InactiveReason.String != models.InactiveReasonStale || old.LastSyncedAt.Year() != 1970 {
		t.Fatalf("old holder should be inactive and first in line for a re-check: %+v", old)
	}
	due, err := db.ListUsersToVerify(ctx, time.Now().Add(-time.Hour), UserSyncCursor{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].LastSyncedAt.Year() > 1970 {
		t.Fatalf("expected an epoch row first in the re-check queue: %+v", due)
	}

	// The directory then confirms the old holder under their new name.
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: oldHolder, Username: name + "-new", ObservedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, oldHolder); u.Status != models.UserStatusActive || u.Username != name+"-new" {
		t.Fatalf("re-check should restore the old holder: %+v", u)
	}
	if u := mustGetUser(t, ctx, db, newHolder); u.Status != models.UserStatusActive {
		t.Fatalf("the current holder must stay active: %+v", u)
	}
}

func TestLiveListUsersToVerifyPagesWithoutRepeats(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	same := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Microsecond)
	var ids []int64
	for i := 0; i < 5; i++ {
		id := liveUserID()
		ids = append(ids, id)
		if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: liveUsername("page"), ObservedAt: same}, nil); err != nil {
			t.Fatal(err)
		}
	}
	cleanupUsers(t, db, ids...)
	deletedID := ids[2]
	if _, err := db.MarkUserDeleted(ctx, deletedID); err != nil {
		t.Fatal(err)
	}

	mine := map[int64]bool{}
	for _, id := range ids {
		mine[id] = true
	}
	seen := map[int64]int{}
	var cursor UserSyncCursor
	for page := 0; page < 10000; page++ {
		rows, err := db.ListUsersToVerify(ctx, same.Add(time.Second), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			if mine[r.CNSUserID] {
				seen[r.CNSUserID]++
			}
		}
		last := rows[len(rows)-1]
		cursor = UserSyncCursor{LastSyncedAt: last.LastSyncedAt, CNSUserID: last.CNSUserID}
	}
	for _, id := range ids {
		want := 1
		if id == deletedID {
			want = 0
		}
		if seen[id] != want {
			t.Fatalf("user %d listed %d times, want %d (all: %v)", id, seen[id], want, seen)
		}
	}
}

func TestLiveDeactivateStaleUsersRecordsReason(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	id := liveUserID()
	cleanupUsers(t, db, id)
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: liveUsername("stale"), ObservedAt: time.Now().Add(-40 * 24 * time.Hour)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeactivateStaleUsers(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	u := mustGetUser(t, ctx, db, id)
	if u.Status != models.UserStatusInactive || u.InactiveReason.String != models.InactiveReasonStale {
		t.Fatalf("expected inactive/stale: %+v", u)
	}
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: u.Username, ObservedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if u := mustGetUser(t, ctx, db, id); u.Status != models.UserStatusActive {
		t.Fatalf("a fresh confirmation should revive a stale row: %+v", u)
	}
}

func TestLivePurgeCNSWebhookEvents(t *testing.T) {
	ctx, db := liveRetentionDB(t)
	oldID, newID := "evt-purge-old-"+randomSuffix(), "evt-purge-new-"+randomSuffix()
	t.Cleanup(func() { db.db.Exec(`DELETE FROM cns_webhook_events WHERE event_id = ANY($1)`, "{"+oldID+","+newID+"}") })
	if _, err := db.db.ExecContext(ctx, `INSERT INTO cns_webhook_events (event_id, event_type, received_at) VALUES ($1, 'ping', NOW() - INTERVAL '8 days'), ($2, 'ping', NOW())`, oldID, newID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeCNSWebhookEvents(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, ctx, db, `SELECT COUNT(*) FROM cns_webhook_events WHERE event_id = $1`, oldID); n != 0 {
		t.Fatal("old event id kept")
	}
	if n := count(t, ctx, db, `SELECT COUNT(*) FROM cns_webhook_events WHERE event_id = $1`, newID); n != 1 {
		t.Fatal("recent event id purged")
	}
}
