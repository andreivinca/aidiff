package localhist

import (
	"errors"
	"slices"
	"time"
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
	var f connectionsFile
	if err := c.file().load(&f); err != nil {
		return nil, err
	}
	return f.Servers, nil
}

// Add records spec, on machine, as connected now: it moves to the top, and
// the list keeps the newest maxEntries. A spec is kept as it is dialed, not
// cleaned as a folder is: ssh://host is no path.
func (c Connections) Add(spec, machine string) error {
	if spec == "" {
		return errors.New("save connection: no server")
	}
	return c.change(func(list []Connection) []Connection {
		list = slices.DeleteFunc(list, func(e Connection) bool { return e.Spec == spec })
		list = slices.Insert(list, 0, Connection{Spec: spec, Machine: machine, Connected: clock(c.now).UTC()})
		return list[:min(len(list), maxEntries)]
	})
}

// Remove forgets spec.
func (c Connections) Remove(spec string) error {
	return c.change(func(list []Connection) []Connection {
		return slices.DeleteFunc(list, func(e Connection) bool { return e.Spec == spec })
	})
}

// change rewrites the list with edit, under the exclusive lock beside
// connections.json (update).
func (c Connections) change(edit func([]Connection) []Connection) error {
	return update(c.file(), func(f *connectionsFile) {
		f.Servers = edit(f.Servers)
	})
}

func (c Connections) file() jsonFile {
	return jsonFile{path: c.Path, what: "connections"}
}
