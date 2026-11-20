package localhist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/umputun/revdiff/app/fsutil"
)

// Connections is the list of T3 servers aidiff connected to, kept the way
// the folders are: the rows the connections panel lists while they are not
// connected, so one is reached again with a single key.
type Connections struct {
	Path string
	now  func() time.Time // the clock entries are dated by; time.Now when nil
}

// Connection is a server, as it is dialed (local, ssh://host or an URL),
// the machine it runs on, and when it was last connected to.
type Connection struct {
	Spec      string    `json:"spec"`
	Machine   string    `json:"machine,omitempty"`
	Connected time.Time `json:"connected"`
}

type connectionsFile struct {
	Servers []Connection `json:"servers"`
}

// List returns the servers, most recently connected first. A missing file
// is an empty list.
func (c Connections) List() ([]Connection, error) {
	f, err := c.load()
	if err != nil {
		return nil, err
	}
	return f.Servers, nil
}

// Add records spec, on machine, as connected now: it moves to the top, and
// the list keeps the newest maxEntries.
func (c Connections) Add(spec, machine string) error {
	return c.change(func(list []Connection) []Connection {
		list = slices.DeleteFunc(list, func(e Connection) bool { return e.Spec == spec })
		list = slices.Insert(list, 0, Connection{Spec: spec, Machine: machine, Connected: c.clock().UTC()})
		return list[:min(len(list), maxEntries)]
	})
}

// Remove forgets spec.
func (c Connections) Remove(spec string) error {
	return c.change(func(list []Connection) []Connection {
		return slices.DeleteFunc(list, func(e Connection) bool { return e.Spec == spec })
	})
}

// change rewrites the list with edit, under the lock the folders' list
// takes, so two aidiff instances saving at once both keep their change.
func (c Connections) change(edit func([]Connection) []Connection) error {
	if c.Path == "" {
		return errors.New("no connections file")
	}
	unlock, err := Store{Path: c.Path}.lock()
	if err != nil {
		return err
	}
	defer unlock()
	f, err := c.load()
	if err != nil {
		return err
	}
	f.Servers = edit(f.Servers)
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode connections: %w", err)
	}
	if err := fsutil.AtomicWriteFile(c.Path, append(b, '\n')); err != nil {
		return fmt.Errorf("save connections: %w", err)
	}
	return nil
}

func (c Connections) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

func (c Connections) load() (connectionsFile, error) {
	var f connectionsFile
	b, err := os.ReadFile(c.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read connections: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return connectionsFile{}, fmt.Errorf("parse connections %s: %w", c.Path, err)
	}
	return f, nil
}
