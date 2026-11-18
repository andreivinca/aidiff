// Package localhist keeps the folders of this machine aidiff reviewed, most
// recently opened first: the rows of the history panel. Changes take an
// exclusive lock on a file beside the list, so two aidiff instances saving
// at once both keep their folder.
package localhist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/umputun/revdiff/app/fsutil"
)

// maxEntries is how many folders the list keeps; the oldest go first.
const maxEntries = 50

// Store is the history file at Path.
type Store struct {
	Path string
	now  func() time.Time // the clock entries are dated by; time.Now when nil
}

// Entry is a folder and when it was last opened.
type Entry struct {
	Dir    string    `json:"dir"`
	Opened time.Time `json:"opened"`
}

type historyFile struct {
	Folders []Entry `json:"folders"`
}

// List returns the folders, most recently opened first. A missing file is
// an empty history.
func (s Store) List() ([]Entry, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return f.Folders, nil
}

// Add records dir as opened now: it moves to the top, and the list keeps
// the newest maxEntries.
func (s Store) Add(dir string) error {
	dir = filepath.Clean(dir)
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	f.Folders = slices.DeleteFunc(f.Folders, func(e Entry) bool { return e.Dir == dir })
	f.Folders = slices.Insert(f.Folders, 0, Entry{Dir: dir, Opened: s.clock().UTC()})
	if len(f.Folders) > maxEntries {
		f.Folders = f.Folders[:maxEntries]
	}
	return s.save(f)
}

func (s Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// lock creates the directory and takes the store's exclusive lock,
// returning its release.
func (s Store) lock() (unlock func(), err error) {
	if err = os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return nil, fmt.Errorf("create history directory: %w", err)
	}
	lf, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open history lock: %w", err)
	}
	if err = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		_ = lf.Close()
		return nil, fmt.Errorf("lock history: %w", err)
	}
	return func() {
		_ = lf.Close() // closing the descriptor releases the lock
	}, nil
}

func (s Store) save(f historyFile) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}
	if err := fsutil.AtomicWriteFile(s.Path, append(b, '\n')); err != nil {
		return fmt.Errorf("save history: %w", err)
	}
	return nil
}

func (s Store) load() (historyFile, error) {
	var f historyFile
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read history: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return historyFile{}, fmt.Errorf("parse history %s: %w", s.Path, err)
	}
	return f, nil
}
