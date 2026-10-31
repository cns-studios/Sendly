package services

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

// UserCacheStore is the local persistence this service needs for the CNS
// user cache; implemented by *storage.Postgres.
type UserCacheStore interface {
	GetUser(ctx context.Context, cnsUserID int64) (*models.User, error)
	RecordCNSProfile(ctx context.Context, profile models.CNSUserProfile, locked *bool) error
	MarkUserDeleted(ctx context.Context, cnsUserID int64) (bool, error)
	ListUsersToVerify(ctx context.Context, syncedBefore time.Time, after storage.UserSyncCursor, limit int) ([]models.User, error)
	DeactivateStaleUsers(ctx context.Context, staleBefore time.Time) (int64, error)
	PurgeCNSWebhookEvents(ctx context.Context, cutoff time.Time) (int64, error)
	RunJob(ctx context.Context, name string, interval time.Duration, fn func(context.Context)) (bool, error)
}

const (
	// directoryMaxBatches bounds one pass to 20 directory calls (2000 users)
	// plus the single lookups for missing users, far below CNS's directory
	// limit of 900 requests per 15 minutes. A larger cache catches up over
	// the following passes.
	directoryMaxBatches = 20
	// webhookEventRetention outlasts CNS's retries of one delivery (~1 day).
	webhookEventRetention = 7 * 24 * time.Hour
	reconcileJobName      = "user-cache-reconcile"
)

// UserCache keeps the local Postgres cache of CNS users (users table) in
// step with CNS. Four things write to it:
//
//   - SyncIfStale, from request handling once a bearer token has been
//     validated, refreshes the user's own row from GET /api/service/me when
//     it is missing or older than cfg.UserCacheTTL. This is the only source
//     that knows whether the account is locked.
//   - CNS webhooks (handlers.CNSWebhookHandler): user.updated for renames and
//     avatar changes, user.deleted for deletions. They arrive within seconds,
//     but only for users with a live Sendly session, and can be lost.
//   - The reconciliation job re-checks every row against the users:read
//     directory once per cfg.UserCacheRecheck. This catches whatever the
//     webhooks missed, including users who signed out long ago.
//   - When CNS hasn't confirmed a row for cfg.UserCacheStaleAfter (the
//     directory is unreachable or the key lacks users:read), the job marks
//     it inactive so it leaves recipient search until CNS confirms it again.
//
// storage.RecordCNSProfile and storage.MarkUserDeleted decide what wins when
// these disagree; see internal/storage/users.go.
type UserCache struct {
	cfg      *config.Config
	db       UserCacheStore
	cns      *CNSClient
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewUserCache(cfg *config.Config, db UserCacheStore, cns *CNSClient) *UserCache {
	return &UserCache{
		cfg:      cfg,
		db:       db,
		cns:      cns,
		stopChan: make(chan struct{}),
	}
}

// SyncIfStale refreshes the local row for cnsUserID from the user's own CNS
// service profile if it's missing or last confirmed more than
// cfg.UserCacheTTL ago. Best-effort: any failure is logged and swallowed so
// a CNS or DB hiccup never fails the caller's real request, which has
// already been authenticated by the time this runs.
func (uc *UserCache) SyncIfStale(ctx context.Context, cnsUserID int64, accessToken string) {
	existing, err := uc.db.GetUser(ctx, cnsUserID)
	if err != nil && !errors.Is(err, models.ErrUserNotFound) {
		log.Printf("user cache: failed to read local row for cns_user_id=%d: %v", cnsUserID, err)
		return
	}
	if existing != nil && (existing.Status == models.UserStatusDeleted || time.Since(existing.LastSyncedAt) < uc.cfg.UserCacheTTL) {
		return
	}

	syncCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Taken before the request: a webhook CNS created while it was in
	// flight may carry newer data and must still win.
	observedAt := time.Now()
	profile, err := uc.cns.GetServiceProfile(syncCtx, accessToken)
	if err != nil {
		log.Printf("user cache: failed to fetch cns service profile for cns_user_id=%d: %v", cnsUserID, err)
		return
	}

	locked := !profile.IsActive || profile.Locked
	if err := uc.db.RecordCNSProfile(ctx, models.CNSUserProfile{
		CNSUserID:  profile.ID,
		Username:   profile.Username,
		AvatarURL:  profile.Profile.Avatar,
		ObservedAt: observedAt,
	}, &locked); err != nil {
		log.Printf("user cache: failed to upsert local row for cns_user_id=%d: %v", cnsUserID, err)
		return
	}

	status := models.UserStatusActive
	if locked {
		status = models.UserStatusInactive
	}
	if err := uc.cns.PatchServiceData(syncCtx, accessToken, map[string]any{
		"status":         status,
		"last_synced_at": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		// Best-effort mirror only; Postgres is the source of truth this
		// service reads from, so a failed CNS-side write is not fatal.
		log.Printf("user cache: failed to patch cns service data for cns_user_id=%d: %v", cnsUserID, err)
	}
}

func (uc *UserCache) Start() {
	uc.wg.Add(1)
	go uc.run()
}

func (uc *UserCache) Stop() {
	close(uc.stopChan)
	uc.wg.Wait()
}

// run checks every minute whether reconciliation is due. Every replica
// checks, but only one runs it per cfg.UserCacheReconcileInterval.
func (uc *UserCache) run() {
	defer uc.wg.Done()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uc.runIfDue()
		case <-uc.stopChan:
			log.Println("User cache reconciliation service stopping...")
			return
		}
	}
}

func (uc *UserCache) runIfDue() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := uc.db.RunJob(ctx, reconcileJobName, uc.cfg.UserCacheReconcileInterval, uc.reconcile); err != nil {
		log.Printf("user cache: could not claim the reconciliation run: %v", err)
	}
}

func (uc *UserCache) reconcile(ctx context.Context) {
	uc.VerifyWithDirectory(ctx, time.Now().Add(-uc.cfg.UserCacheRecheck), uc.cfg.UserCacheMaxDeletionsPerRun).log()

	staleBefore := time.Now().Add(-uc.cfg.UserCacheStaleAfter)
	count, err := uc.db.DeactivateStaleUsers(ctx, staleBefore)
	if err != nil {
		log.Printf("user cache: deactivating stale users failed: %v", err)
	} else if count > 0 {
		log.Printf("user cache: deactivated %d user(s) CNS hasn't confirmed since %s", count, staleBefore.UTC().Format(time.RFC3339))
	}

	if _, err := uc.db.PurgeCNSWebhookEvents(ctx, time.Now().Add(-webhookEventRetention)); err != nil {
		log.Printf("user cache: purging old webhook event ids failed: %v", err)
	}
}

// ForceReconcile runs one reconciliation pass immediately, on this replica,
// without claiming the job; used by tests rather than waiting for the ticker.
func (uc *UserCache) ForceReconcile() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	uc.reconcile(ctx)
}

// DirectoryResult summarizes one VerifyWithDirectory pass.
type DirectoryResult struct {
	Skipped   bool // the directory isn't configured
	Confirmed int  // rows CNS returned, profile refreshed
	Deleted   int  // rows CNS confirmed deleted, now tombstones
	// Missing counts rows the batch lookup didn't return. When it exceeds
	// the deletion limit nothing is deleted (MassDeletionHeld).
	Missing          int
	MassDeletionHeld bool
	Err              error
}

func (r DirectoryResult) log() {
	if r.Skipped {
		return
	}
	if r.MassDeletionHeld {
		log.Printf("user cache: CNS directory no longer returns %d cached user(s), more than one run may delete; "+
			"deleted none. If CNS really deleted them, run `admin sync-users --max-deletions %d`.", r.Missing, r.Missing)
	}
	if r.Err != nil {
		log.Printf("user cache: directory check stopped early: %v", r.Err)
	}
	if r.Confirmed > 0 || r.Deleted > 0 {
		log.Printf("user cache: directory check confirmed %d user(s), recorded %d deletion(s)", r.Confirmed, r.Deleted)
	}
}

// VerifyWithDirectory re-checks rows CNS hasn't confirmed since
// syncedBefore against the users:read directory: found users get
// their current profile, users CNS says are gone become tombstones. A user
// counts as gone only when a single lookup returns CNS's not_found; the
// batch lookup just leaves them out, which a CNS fault could also do. If
// more than maxDeletions users are missing in one pass, none are deleted
// (a negative maxDeletions means no limit). Any CNS error ends the pass;
// the rows it didn't reach go first next time. One pass covers at most
// directoryMaxBatches batches.
func (uc *UserCache) VerifyWithDirectory(ctx context.Context, syncedBefore time.Time, maxDeletions int) DirectoryResult {
	var result DirectoryResult
	if !uc.cns.DirectoryConfigured() {
		result.Skipped = true
		return result
	}

	var cursor storage.UserSyncCursor
	var missing []int64
	for batch := 0; batch < directoryMaxBatches && ctx.Err() == nil; batch++ {
		rows, err := uc.db.ListUsersToVerify(ctx, syncedBefore, cursor, cnsDirectoryBatchMax)
		if err != nil {
			result.Err = err
			return result
		}
		if len(rows) == 0 {
			break
		}
		last := rows[len(rows)-1]
		cursor = storage.UserSyncCursor{LastSyncedAt: last.LastSyncedAt, CNSUserID: last.CNSUserID}

		ids := make([]int64, len(rows))
		for i, row := range rows {
			ids[i] = row.CNSUserID
		}
		observedAt := time.Now()
		found, err := uc.cns.GetPublicUsers(ctx, ids)
		if err != nil {
			result.Err = err
			return result
		}
		for _, id := range ids {
			profile, ok := found[id]
			if !ok {
				missing = append(missing, id)
				continue
			}
			if err := uc.db.RecordCNSProfile(ctx, publicProfileToCNS(profile, observedAt), nil); err != nil {
				result.Err = err
				return result
			}
			result.Confirmed++
		}
		if len(rows) < cnsDirectoryBatchMax {
			break
		}
	}

	result.Missing = len(missing)
	if maxDeletions >= 0 && len(missing) > maxDeletions {
		result.MassDeletionHeld = true
		return result
	}
	for _, id := range missing {
		observedAt := time.Now()
		profile, err := uc.cns.GetPublicUser(ctx, id)
		switch {
		case errors.Is(err, ErrCNSUserNotFound):
			deleted, err := uc.db.MarkUserDeleted(ctx, id)
			if err != nil {
				result.Err = err
				return result
			}
			if deleted {
				log.Printf("user cache: CNS no longer has cns_user_id=%d; erased the cached profile", id)
				result.Deleted++
			}
		case err != nil:
			result.Err = err
			return result
		default:
			if err := uc.db.RecordCNSProfile(ctx, publicProfileToCNS(*profile, observedAt), nil); err != nil {
				result.Err = err
				return result
			}
			result.Confirmed++
		}
	}
	return result
}

func publicProfileToCNS(p CNSPublicProfile, observedAt time.Time) models.CNSUserProfile {
	profile := models.CNSUserProfile{CNSUserID: p.ID, Username: p.Username, ObservedAt: observedAt}
	if p.Avatar != nil {
		profile.AvatarURL = *p.Avatar
	}
	return profile
}
