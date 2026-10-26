package services

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

// Two replicas share one database and one volume. The replica assembling an
// upload dies; the other one resumes it, and retried requests from the client
// (chunk, complete, finalize) get the same answers as the first attempt.
func TestLiveUploadSurvivesReplicaLoss(t *testing.T) {
	ctx, db := liveDB(t)

	root := t.TempDir()
	t.Setenv("DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("CHUNK_DIR", filepath.Join(root, "chunks"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	replica := func(name string) *Upload {
		c := *cfg
		c.InstanceName = name
		return NewUpload(&c, db, fs, NewTracker(db))
	}
	podA, podB := replica("pod-a"), replica("pod-b")
	t.Cleanup(func() { podA.Stop(0); podB.Stop(0) })

	chunks := []string{"first-", "second-", "third"}
	init, err := podA.InitUpload(ctx, &models.UploadInitRequest{
		FileName: "x.bin", FileSize: 18, TotalChunks: len(chunks), ChunkSize: 7,
	}, "192.0.2.1", 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	// Chunks land on either replica; one is sent twice after a lost response.
	for i, chunk := range chunks {
		pod := podA
		if i%2 == 1 {
			pod = podB
		}
		if err := pod.UploadChunk(ctx, init.SessionID, i, strings.NewReader(chunk)); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	if err := podB.UploadChunk(ctx, init.SessionID, 1, strings.NewReader(chunks[1])); err != nil {
		t.Fatalf("repeated chunk: %v", err)
	}

	// pod-a accepts complete and takes the assembly, then dies before doing
	// it: only its short lease is left behind.
	if _, err := db.StartAssembly(ctx, init.SessionID, uploadPendingTTL); err != nil {
		t.Fatal(err)
	}
	if s, err := db.ClaimAssembly(ctx, init.SessionID, "pod-a", time.Second); err != nil || s == nil {
		t.Fatalf("pod-a claim: %v, %v", s, err)
	}
	if status, _ := podB.GetAssemblyStatus(ctx, init.SessionID); status != "pending" {
		t.Fatalf("status while pod-a holds the lease = %q, want pending", status)
	}

	// The client retries complete against pod-b: same answer, no second assembly.
	if _, err := podB.CompleteUpload(ctx, init.SessionID); err != nil {
		t.Fatalf("repeated complete: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	podB.resumeOrphanedAssemblies()

	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := podA.GetAssemblyStatus(ctx, init.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if status == "done" {
			break
		}
		if strings.HasPrefix(status, "error:") || time.Now().After(deadline) {
			t.Fatalf("assembly status = %q", status)
		}
		time.Sleep(50 * time.Millisecond)
	}

	blob, err := fs.OpenFile(init.FileID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(blob)
	blob.Close()
	if !bytes.Equal(got, []byte(strings.Join(chunks, ""))) {
		t.Fatalf("assembled blob = %q", got)
	}
	if n, _ := fs.GetChunkCount(init.SessionID); n != 0 {
		t.Fatalf("%d chunks left after assembly", n)
	}

	first, err := podA.FinalizeUpload(ctx, init.SessionID, models.Duration24Hours)
	if err != nil {
		t.Fatal(err)
	}
	// The finalize response was lost; the client asks pod-b again.
	second, err := podB.FinalizeUpload(ctx, init.SessionID, models.Duration24Hours)
	if err != nil {
		t.Fatalf("repeated finalize: %v", err)
	}
	if *first != *second {
		t.Fatalf("repeated finalize = %+v, want %+v", second, first)
	}
	if _, err := podB.CompleteUpload(ctx, init.SessionID); err != nil {
		t.Fatalf("complete after finalize: %v", err)
	}
}
