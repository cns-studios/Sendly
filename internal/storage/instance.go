package storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// instanceMarkerName is the file in DATA_DIR (and a separate CHUNK_DIR) that
// records which database owns the directory. Orphan cleanup deletes every
// blob and chunk session its own database and Redis don't know about, so two
// instances sharing storage would silently delete each other's files.
const instanceMarkerName = ".sendly-instance"

type guardedDir struct {
	path    string
	hasData func() (bool, error)
}

// ClaimStorage ties the storage directories to the database identified by
// instanceID. An unmarked directory is claimed if it holds no data yet; one
// that already holds data is claimed only with adopt, the explicit one-time
// switch for deployments that predate the marker. A directory marked by a
// different database is always an error.
func (fs *Filesystem) ClaimStorage(instanceID string, adopt bool) error {
	for _, d := range fs.guardedDirs() {
		if err := claimDir(d, instanceID, adopt); err != nil {
			return err
		}
	}
	return nil
}

// VerifyStorage checks that the storage directories are already claimed by
// instanceID, without claiming anything. For tools that delete files but must
// never take ownership of storage themselves, such as the admin CLI.
func (fs *Filesystem) VerifyStorage(instanceID string) error {
	for _, d := range fs.guardedDirs() {
		owner, err := readInstanceMarker(d.path)
		if os.IsNotExist(err) {
			return fmt.Errorf("%s has no instance marker; start the server once to claim it", d.path)
		}
		if err != nil {
			return err
		}
		if owner != instanceID {
			return instanceMismatchError(d.path, owner, instanceID)
		}
	}
	return nil
}

func (fs *Filesystem) guardedDirs() []guardedDir {
	dirs := []guardedDir{{path: fs.dataDir, hasData: fs.hasFiles}}
	if !isWithin(fs.chunkDir, fs.dataDir) {
		dirs = append(dirs, guardedDir{path: fs.chunkDir, hasData: fs.hasChunkSessions})
	}
	return dirs
}

func (fs *Filesystem) hasFiles() (bool, error) {
	ids, err := fs.GetAllFileIDs()
	return len(ids) > 0, err
}

func (fs *Filesystem) hasChunkSessions() (bool, error) {
	ids, err := fs.GetAllSessionIDs()
	return len(ids) > 0, err
}

func claimDir(d guardedDir, instanceID string, adopt bool) error {
	owner, err := readInstanceMarker(d.path)
	if err == nil {
		if owner != instanceID {
			return instanceMismatchError(d.path, owner, instanceID)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read instance marker in %s: %w", d.path, err)
	}

	hasData, err := d.hasData()
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", d.path, err)
	}
	if hasData && !adopt {
		return fmt.Errorf("%s holds data but has no instance marker; if it belongs to this database, "+
			"start once with SENDLY_ADOPT_DATA_DIR=true to claim it", d.path)
	}

	if err := writeInstanceMarker(d.path, instanceID); err != nil {
		if os.IsExist(err) {
			// Another instance claimed it between our read and write.
			return claimDir(d, instanceID, false)
		}
		return fmt.Errorf("failed to write instance marker in %s: %w", d.path, err)
	}
	if hasData {
		log.Printf("Adopted existing storage %s for instance %s", d.path, instanceID)
	} else {
		log.Printf("Claimed storage %s for instance %s", d.path, instanceID)
	}
	return nil
}

func readInstanceMarker(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, instanceMarkerName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeInstanceMarker(dir, instanceID string) error {
	f, err := os.OpenFile(filepath.Join(dir, instanceMarkerName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(instanceID + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func instanceMismatchError(dir, owner, instanceID string) error {
	return fmt.Errorf("%s belongs to another Sendly instance (marker %q, this database is %q): "+
		"instances sharing storage delete each other's files. Give this instance its own DATA_DIR/CHUNK_DIR; "+
		"only if this database really owns the directory, delete %s and start with SENDLY_ADOPT_DATA_DIR=true",
		dir, owner, instanceID, filepath.Join(dir, instanceMarkerName))
}

// isWithin reports whether path is dir or lies inside it.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// CheckHealth proves the storage directories are usable right now: each must
// accept a durable write, and still carry the marker of instanceID. A bind
// mount that dropped out would otherwise leave the app writing to the
// container's own filesystem, and a full or read-only disk fails uploads.
func (fs *Filesystem) CheckHealth(instanceID string) error {
	if err := fs.VerifyStorage(instanceID); err != nil {
		return err
	}
	dirs := []string{fs.dataDir, fs.finalDir}
	if !isWithin(fs.chunkDir, fs.dataDir) {
		dirs = append(dirs, fs.chunkDir)
	}
	for _, dir := range dirs {
		if err := probeWritable(dir); err != nil {
			return fmt.Errorf("%s is not writable: %w", dir, err)
		}
	}
	return nil
}

func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".healthcheck-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString("ok"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
