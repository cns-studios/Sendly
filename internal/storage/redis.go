package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"shareit/internal/config"
	"shareit/internal/models"

	"github.com/go-redis/redis/v8"
)

type Redis struct {
	client *redis.Client
}

const (
	prefixUploadSession  = "upload:session:"
	prefixUploadChunks   = "upload:chunks:"
	prefixPendingFile    = "pending:file:"
	prefixAssemblyStatus = "assembly:status:"
	prefixRateLimit      = "ratelimit:"
)

const (
	sessionTTL = 1 * time.Hour
	pendingTTL = 10 * time.Minute
)

func NewRedis(cfg *config.Config) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:        cfg.RedisAddr(),
		DB:          0,
		DialTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &Redis{client: client}, nil
}

func (r *Redis) Close() error {
	return r.client.Close()
}

func (r *Redis) Client() *redis.Client {
	return r.client
}

func (r *Redis) CreateUploadSession(ctx context.Context, session *models.UploadSession) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	key := prefixUploadSession + session.SessionID
	return r.client.Set(ctx, key, data, sessionTTL).Err()
}

func (r *Redis) GetUploadSession(ctx context.Context, sessionID string) (*models.UploadSession, error) {
	key := prefixUploadSession + sessionID
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, models.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}

	var session models.UploadSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}

	return &session, nil
}

func (r *Redis) DeleteUploadSession(ctx context.Context, sessionID string) error {
	key := prefixUploadSession + sessionID
	return r.client.Del(ctx, key).Err()
}

func (r *Redis) ExtendUploadSession(ctx context.Context, sessionID string) error {
	key := prefixUploadSession + sessionID
	return r.client.Expire(ctx, key, sessionTTL).Err()
}

func (r *Redis) SetUploadSessionTTL(ctx context.Context, sessionID string, ttl time.Duration) error {
	key := prefixUploadSession + sessionID
	return r.client.Expire(ctx, key, ttl).Err()
}

func (r *Redis) MarkChunkUploaded(ctx context.Context, sessionID string, chunkIndex int) error {
	key := prefixUploadChunks + sessionID
	pipe := r.client.Pipeline()
	pipe.SAdd(ctx, key, chunkIndex)
	pipe.Expire(ctx, key, sessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) IsChunkUploaded(ctx context.Context, sessionID string, chunkIndex int) (bool, error) {
	key := prefixUploadChunks + sessionID
	return r.client.SIsMember(ctx, key, chunkIndex).Result()
}

func (r *Redis) GetUploadedChunks(ctx context.Context, sessionID string) ([]int, error) {
	key := prefixUploadChunks + sessionID
	members, err := r.client.SMembers(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	chunks := make([]int, 0, len(members))
	for _, m := range members {
		var idx int
		fmt.Sscanf(m, "%d", &idx)
		chunks = append(chunks, idx)
	}
	return chunks, nil
}

func (r *Redis) GetUploadedChunkCount(ctx context.Context, sessionID string) (int64, error) {
	key := prefixUploadChunks + sessionID
	return r.client.SCard(ctx, key).Result()
}

func (r *Redis) DeleteChunkTracking(ctx context.Context, sessionID string) error {
	key := prefixUploadChunks + sessionID
	return r.client.Del(ctx, key).Err()
}

func (r *Redis) SetChunkTrackingTTL(ctx context.Context, sessionID string) error {
	key := prefixUploadChunks + sessionID
	return r.client.Expire(ctx, key, sessionTTL).Err()
}

func (r *Redis) MarkFilePending(ctx context.Context, fileID, sessionID string) error {
	key := prefixPendingFile + fileID
	return r.client.Set(ctx, key, sessionID, pendingTTL).Err()
}

func (r *Redis) IsFilePending(ctx context.Context, fileID string) (bool, error) {
	key := prefixPendingFile + fileID
	exists, err := r.client.Exists(ctx, key).Result()
	return exists > 0, err
}

func (r *Redis) GetPendingSessionID(ctx context.Context, fileID string) (string, error) {
	key := prefixPendingFile + fileID
	sessionID, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", models.ErrUploadNotPending
	}
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

func (r *Redis) PendingTTL() time.Duration {
	return pendingTTL
}

func (r *Redis) RemovePendingFile(ctx context.Context, fileID string) error {
	key := prefixPendingFile + fileID
	return r.client.Del(ctx, key).Err()
}

func (r *Redis) GetAllPendingFiles(ctx context.Context) ([]string, error) {
	var fileIDs []string
	var cursor uint64
	for {
		var keys []string
		var err error
		keys, cursor, err = r.client.Scan(ctx, cursor, prefixPendingFile+"*", 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			fileIDs = append(fileIDs, key[len(prefixPendingFile):])
		}
		if cursor == 0 {
			break
		}
	}
	return fileIDs, nil
}

func (r *Redis) CheckRateLimit(ctx context.Context, keySuffix string, maxRequests int64, ttl time.Duration) (bool, error) {
	key := prefixRateLimit + keySuffix

	pipe := r.client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	return incr.Val() <= maxRequests, nil
}

func (r *Redis) GetRateLimitCount(ctx context.Context, ip string) (int64, error) {
	key := prefixRateLimit + ip
	count, err := r.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

func (r *Redis) SetAssemblyStatus(ctx context.Context, sessionID, status string) error {
	key := prefixAssemblyStatus + sessionID
	return r.client.Set(ctx, key, status, 30*time.Minute).Err()
}

func (r *Redis) GetAssemblyStatus(ctx context.Context, sessionID string) (string, error) {
	key := prefixAssemblyStatus + sessionID
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", models.ErrSessionNotFound
	}
	return val, err
}

func (r *Redis) DeleteAssemblyStatus(ctx context.Context, sessionID string) error {
	key := prefixAssemblyStatus + sessionID
	return r.client.Del(ctx, key).Err()
}

func (r *Redis) CleanupSession(ctx context.Context, sessionID string) error {

	session, err := r.GetUploadSession(ctx, sessionID)
	if err != nil && err != models.ErrSessionNotFound {
		return err
	}

	if err := r.DeleteUploadSession(ctx, sessionID); err != nil {
		return err
	}

	if err := r.DeleteChunkTracking(ctx, sessionID); err != nil {
		return err
	}

	if session != nil {
		if err := r.RemovePendingFile(ctx, session.FileID); err != nil {
			return err
		}
	}

	return nil
}

func (r *Redis) GetAllActiveSessions(ctx context.Context) ([]string, error) {
	var sessionIDs []string
	var cursor uint64
	for {
		var keys []string
		var err error
		keys, cursor, err = r.client.Scan(ctx, cursor, prefixUploadSession+"*", 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			sessionIDs = append(sessionIDs, key[len(prefixUploadSession):])
		}
		if cursor == 0 {
			break
		}
	}
	return sessionIDs, nil
}
