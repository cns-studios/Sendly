package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"sendly/internal/config"
	"sendly/internal/storage"
)

const (
	statsReportSource = "sendly-service"
	statsReportUnit   = "bytes"

	statsReportMetricUploaded  = "sendly.uploaded_bytes"
	statsReportMetricProcessed = "sendly.processed_bytes"

	statsReportMaxRetries = 3
)

type StatsReporter struct {
	cfg      *config.Config
	db       *storage.Postgres
	http     *http.Client
	stopChan chan struct{}
	wg       sync.WaitGroup
}

type statsReportEntry struct {
	MetricKey string `json:"metric_key"`
	Value     string `json:"value"`
	Unit      string `json:"unit"`
	Source    string `json:"source"`
}

type statsReportBatch struct {
	Reports []statsReportEntry `json:"reports"`
}

func NewStatsReporter(cfg *config.Config, db *storage.Postgres) *StatsReporter {
	return &StatsReporter{
		cfg:      cfg,
		db:       db,
		http:     &http.Client{Timeout: 15 * time.Second},
		stopChan: make(chan struct{}),
	}
}

func (r *StatsReporter) IsConfigured() bool {
	return r.cfg.ReportBotURL != "" && r.cfg.SendlyBotAPIKey != ""
}

func (r *StatsReporter) Start() {
	if !r.IsConfigured() {
		log.Println("Stats reporter disabled: REPORT_BOT_URL or SENDLY_BOT_API_KEY not set")
		return
	}
	r.wg.Add(1)
	go r.run()
}

func (r *StatsReporter) Stop() {
	close(r.stopChan)
	r.wg.Wait()
}

func (r *StatsReporter) run() {
	defer r.wg.Done()

	r.report()

	ticker := time.NewTicker(r.cfg.StatsReportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.report()
		case <-r.stopChan:
			log.Println("Stats reporter stopping...")
			return
		}
	}
}

func (r *StatsReporter) report() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	counters, err := r.db.GetTrackingCounters(ctx)
	if err != nil {
		log.Printf("Stats reporter: failed to read tracking counters: %v", err)
		return
	}

	payload := statsReportBatch{
		Reports: []statsReportEntry{
			{
				MetricKey: statsReportMetricProcessed,
				Value:     strconv.FormatInt(counters.TotalProcessedBytes, 10),
				Unit:      statsReportUnit,
				Source:    statsReportSource,
			},
			{
				MetricKey: statsReportMetricUploaded,
				Value:     strconv.FormatInt(counters.TotalUploadedBytes, 10),
				Unit:      statsReportUnit,
				Source:    statsReportSource,
			},
		},
	}

	if err := r.sendWithRetry(ctx, payload); err != nil {
		log.Printf("Stats reporter: failed to report counters: %v", err)
		return
	}

	log.Printf("Stats reporter: reported processed=%d uploaded=%d bytes",
		counters.TotalProcessedBytes, counters.TotalUploadedBytes)
}

func (r *StatsReporter) sendWithRetry(ctx context.Context, payload statsReportBatch) error {
	var lastErr error

	for attempt := 1; attempt <= statsReportMaxRetries; attempt++ {
		status, err := r.send(ctx, payload)
		if err == nil {
			return nil
		}
		lastErr = err

		if !r.shouldRetry(status) || attempt == statsReportMaxRetries {
			break
		}

		backoff := time.Duration(1<<(attempt-1)) * 30 * time.Second
		log.Printf("Stats reporter: attempt %d failed (status %d), retrying in %s: %v",
			attempt, status, backoff, err)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return lastErr
}

func (r *StatsReporter) shouldRetry(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func (r *StatsReporter) send(ctx context.Context, payload statsReportBatch) (int, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.cfg.ReportBotURL+"/api/report/batch", bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.cfg.SendlyBotAPIKey)

	resp, err := r.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("bot returned status %d", resp.StatusCode)
	}

	return resp.StatusCode, nil
}
