package services

import (
	"context"
	"log"

	"shareit/internal/storage"
)

type Tracker struct {
	db *storage.Postgres
}

func NewTracker(db *storage.Postgres) *Tracker {
	return &Tracker{db: db}
}

func (t *Tracker) RecordUpload(ctx context.Context, bytes int64, ip string) {
	if bytes <= 0 {
		return
	}
	if err := t.db.RecordUpload(ctx, bytes, ip); err != nil {
		log.Printf("ERROR tracking upload: %v", err)
	}
}

func (t *Tracker) RecordDownload(ctx context.Context, bytes int64) {
	if bytes <= 0 {
		return
	}
	if err := t.db.RecordDownload(ctx, bytes); err != nil {
		log.Printf("ERROR tracking download: %v", err)
	}
}
