package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sendly/internal/config"
)

func newTestFilesystem(t *testing.T, dataDir, chunkDir string) *Filesystem {
	t.Helper()
	fs, err := NewFilesystem(&config.Config{DataDir: dataDir, ChunkDir: chunkDir})
	if err != nil {
		t.Fatalf("NewFilesystem: %v", err)
	}
	return fs
}

func TestClaimStorageClaimsEmptyDirs(t *testing.T) {
	root := t.TempDir()
	fs := newTestFilesystem(t, filepath.Join(root, "data"), filepath.Join(root, "chunks"))

	if err := fs.ClaimStorage("db-a", false); err != nil {
		t.Fatalf("claim empty storage: %v", err)
	}
	for _, dir := range []string{fs.dataDir, fs.chunkDir} {
		if owner, err := readInstanceMarker(dir); err != nil || owner != "db-a" {
			t.Fatalf("marker in %s = %q, %v; want db-a", dir, owner, err)
		}
	}
	// Claiming again with the same database is a no-op.
	if err := fs.ClaimStorage("db-a", false); err != nil {
		t.Fatalf("reclaim by owner: %v", err)
	}
}

func TestClaimStorageRejectsOtherInstance(t *testing.T) {
	root := t.TempDir()
	fs := newTestFilesystem(t, filepath.Join(root, "data"), filepath.Join(root, "chunks"))
	if err := fs.ClaimStorage("db-a", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Adopting never overrides another database's marker.
	for _, adopt := range []bool{false, true} {
		err := fs.ClaimStorage("db-b", adopt)
		if err == nil || !strings.Contains(err.Error(), "another Sendly instance") {
			t.Fatalf("adopt=%v: err = %v; want instance mismatch", adopt, err)
		}
	}
	if err := fs.VerifyStorage("db-b"); err == nil {
		t.Fatal("VerifyStorage accepted another instance's storage")
	}
}

func TestClaimStorageNeedsAdoptForExistingData(t *testing.T) {
	root := t.TempDir()
	fs := newTestFilesystem(t, filepath.Join(root, "data"), filepath.Join(root, "chunks"))
	if err := os.WriteFile(fs.GetFilePath("existingblob00001"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := fs.ClaimStorage("db-a", false); err == nil {
		t.Fatal("claimed unmarked storage holding data without adopt")
	}
	if _, err := readInstanceMarker(fs.dataDir); !os.IsNotExist(err) {
		t.Fatalf("refused claim still wrote a marker: %v", err)
	}
	if err := fs.ClaimStorage("db-a", true); err != nil {
		t.Fatalf("adopt existing storage: %v", err)
	}
	if err := fs.VerifyStorage("db-a"); err != nil {
		t.Fatalf("verify after adopt: %v", err)
	}
}

func TestClaimStorageNestedChunkDirSharesDataMarker(t *testing.T) {
	fs := newTestFilesystem(t, t.TempDir(), "")
	if err := fs.ClaimStorage("db-a", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := readInstanceMarker(fs.chunkDir); !os.IsNotExist(err) {
		t.Fatalf("nested chunk dir got its own marker: %v", err)
	}
}

func TestVerifyStorageRequiresMarker(t *testing.T) {
	fs := newTestFilesystem(t, t.TempDir(), "")
	if err := fs.VerifyStorage("db-a"); err == nil {
		t.Fatal("VerifyStorage accepted unclaimed storage")
	}
}

func TestIsWithin(t *testing.T) {
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"/data", "/data", true},
		{"/data/chunks", "/data", true},
		{"/data/../chunks", "/data", false},
		{"/chunks", "/data", false},
		{"/data-other", "/data", false},
		{"/..data", "/", true},
	}
	for _, c := range cases {
		if got := isWithin(c.path, c.dir); got != c.want {
			t.Errorf("isWithin(%q, %q) = %v; want %v", c.path, c.dir, got, c.want)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	root := t.TempDir()
	fs := newTestFilesystem(t, filepath.Join(root, "data"), filepath.Join(root, "chunks"))
	if err := fs.ClaimStorage("db-a", false); err != nil {
		t.Fatal(err)
	}
	if err := fs.CheckHealth("db-a"); err != nil {
		t.Fatalf("healthy storage: %v", err)
	}
	if entries, _ := os.ReadDir(fs.dataDir); len(entries) != 2 { // files/ and the marker
		t.Fatalf("probe left files behind: %v", entries)
	}

	if err := fs.CheckHealth("db-b"); err == nil {
		t.Fatal("accepted storage owned by another instance")
	}

	// A vanished mount: the marker is gone, so health must fail.
	if err := os.Remove(filepath.Join(fs.dataDir, instanceMarkerName)); err != nil {
		t.Fatal(err)
	}
	if err := fs.CheckHealth("db-a"); err == nil {
		t.Fatal("accepted storage without its marker")
	}
}
