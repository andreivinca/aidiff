// Package localhist keeps the folders of this machine aidiff reviewed, most
// recently opened first: the rows of the history panel. Changes take an
// exclusive lock on a file beside the list, so two aidiff instances saving
// at once both keep their folder.
package localhist

import (
	"errors"
	"path/filepath"
	"slices"
	"time"
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
	var f historyFile
	if err := s.file().load(&f); err != nil {
		return nil, err
	}
	return f.Folders, nil
}

// Add records dir as opened now: it moves to the top, and the list keeps
// the newest maxEntries.
func (s Store) Add(dir string) error {
	if dir == "" {
		return errors.New("add to history: no folder")
	}
	dir = filepath.Clean(dir)
	return update(s.file(), func(f *historyFile) {
		f.Folders = slices.DeleteFunc(f.Folders, func(e Entry) bool { return e.Dir == dir })
		f.Folders = slices.Insert(f.Folders, 0, Entry{Dir: dir, Opened: clock(s.now).UTC()})
		f.Folders = f.Folders[:min(len(f.Folders), maxEntries)]
	})
}

func (s Store) file() jsonFile {
	return jsonFile{path: s.Path, what: "history"}
}
