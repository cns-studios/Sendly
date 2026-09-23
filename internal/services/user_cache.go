package services

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
)

// UserCacheStore is the local persistence this service needs for the CNS
// user cache; implemented by *storage.Postgres.
type UserCacheStore interface {
	GetUser(ctx context.Context, cnsUserID int64) (*models.User, error)
	UpsertUser(ctx context.Context, user *models.User) error
	DeactivateStaleUsers(ctx context.Context, staleBefore time.Time) (int64, error)
}

// UserCache keeps a local Postgres cache of "which CNS users are known to
// this service" in sync with CNS. Two mechanisms populate it:
//
//   - SyncIfStale, called from request handling once a bearer token has
//     already been validated, upserts the row when it's missing or older
//     than cfg.UserCacheTTL. There is no distinct "login" event on this
//     service (CNS owns the login flow; this service only ever sees
//     already-authenticated requests via auth cookies), so "upsert at
//     login" and "upsert on request" collapse into the same thing here.
//     Gating on a TTL keeps this from calling CNS on every request while
//     still being simple: no separate login hook to keep in sync with the
//     auth cookie lifecycle.
//   - The background reconciliation loop (Start/Stop) deactivates users
//     locally once they've gone longer than cfg.UserCacheStaleAfter without
//     a successful sync. CNS has no webhooks and no service-scoped "check
//     user X's status" endpoint that works without that user's own bearer
//     token, so this service cannot proactively re-verify a quiet user
//     against CNS. Aging them out locally is the best available fallback;
//     the user flips back to active automatically the next time they make
//     a real request. Redis isn't used here: this is a single periodic SQL
//     statement with no need for cross-instance coordination or a cursor.
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

// SyncIfStale upserts the local cache row for cnsUserID if it's missing or
// last synced more than cfg.UserCacheTTL ago. Best-effort: any failure is
// logged and swallowed so a CNS or DB hiccup never fails the caller's real
// request, which has already been authenticated by the time this runs.
func (uc *UserCache) SyncIfStale(ctx context.Context, cnsUserID int64, accessToken string) {
	existing, err := uc.db.GetUser(ctx, cnsUserID)
	if err != nil && !errors.Is(err, models.ErrUserNotFound) {
		log.Printf("user cache: failed to read local row for cns_user_id=%d: %v", cnsUserID, err)
		return
	}
	if existing != nil && time.Since(existing.LastSyncedAt) < uc.cfg.UserCacheTTL {
		return
	}

	syncCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	profile, err := uc.cns.GetServiceProfile(syncCtx, accessToken)
	if err != nil {
		log.Printf("user cache: failed to fetch cns service profile for cns_user_id=%d: %v", cnsUserID, err)
		return
	}

	status := models.UserStatusActive
	if !profile.IsActive || profile.Locked {
		status = models.UserStatusInactive
	}

	user := &models.User{
		CNSUserID: profile.ID,
		Username:  profile.Username,
		Status:    status,
	}
	if profile.Profile.Avatar != "" {
		user.AvatarURL = sql.NullString{String: profile.Profile.Avatar, Valid: true}
	}

	if err := uc.db.UpsertUser(ctx, user); err != nil {
		log.Printf("user cache: failed to upsert local row for cns_user_id=%d: %v", cnsUserID, err)
		return
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

func (uc *UserCache) run() {
	defer uc.wg.Done()

	ticker := time.NewTicker(uc.cfg.UserCacheReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uc.reconcile()
		case <-uc.stopChan:
			log.Println("User cache reconciliation service stopping...")
			return
		}
	}
}

func (uc *UserCache) reconcile() {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	staleBefore := time.Now().Add(-uc.cfg.UserCacheStaleAfter)
	count, err := uc.db.DeactivateStaleUsers(ctx, staleBefore)
	if err != nil {
		log.Printf("user cache: reconciliation failed: %v", err)
		return
	}
	if count > 0 {
		log.Printf("user cache: deactivated %d stale local user(s)", count)
	}
}

// ForceReconcile runs one reconciliation pass immediately; used by tests and
// the admin CLI rather than waiting for the ticker.
func (uc *UserCache) ForceReconcile() {
	uc.reconcile()
}
