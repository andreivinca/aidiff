package localhist

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/umputun/revdiff/app/fsutil"
)

// Tabs is the file of the tabs aidiff had open when it last exited, in
// their order, with the one on screen: what reopens each, nothing of what
// was in it. It is written whole at exit, so of two aidiff processes the
// last to exit wins; the tabs of one window are what comes back, never a
// mix of both.
type Tabs struct {
	Path string
}

// The kinds of tab.
const (
	TabLocal = "local" // a folder of this machine
	TabT3    = "t3"    // a project of a T3 server
)

// MaxTabs is how many tabs the file keeps; the ones after it are dropped.
const MaxTabs = 50

// tabsVersion is the file's format. A file of another version is ignored,
// as one that does not parse is, and replaced at the next exit.
const tabsVersion = 1

// tabsLockWait bounds the wait for the file's lock at exit: an aidiff hung
// while holding it must not hold up this one's exit. A variable for the
// tests.
var tabsLockWait = 2 * time.Second

// Tab is a tab as the file keeps it: a local folder, or a T3 server's
// project with the thread on screen in it.
type Tab struct {
	Kind    string `json:"kind"`
	Dir     string `json:"dir,omitempty"`     // local: the folder, absolute
	Server  string `json:"server,omitempty"`  // t3: the server, as a saved connection dials it
	Machine string `json:"machine,omitempty"` // t3: the server's machine, shown until it is connected to
	Project string `json:"project,omitempty"` // t3: the project's id
	Title   string `json:"title,omitempty"`   // t3: the project's title, shown until it is connected to
	Thread  string `json:"thread,omitempty"`  // t3: the thread on screen in it; "" for the project's root
}

type tabsFile struct {
	Version int   `json:"version"`
	Current int   `json:"current"` // the tab on screen, an index into Tabs; -1 for none
	Tabs    []Tab `json:"tabs"`
}

// valid reports whether tab names something to reopen.
func (tab Tab) valid() bool {
	switch tab.Kind {
	case TabLocal: // a folder of this machine, or one on an SSH machine (ssh:host:path, t3client.RemoteDir)
		return tab.Dir != "" && (filepath.IsAbs(tab.Dir) || strings.HasPrefix(tab.Dir, "ssh:"))
	case TabT3:
		return tab.Server != "" && tab.Project != ""
	}
	return false
}

// Load returns the tabs, in order, and the index of the one that was on
// screen, -1 for none. A missing file is no tabs. A file that does not
// parse, or of another version, is an error and no tabs; it is left as it
// is, for the next Save to replace. A tab that names nothing to reopen is
// left out, and only the first MaxTabs are kept.
func (t Tabs) Load() (tabs []Tab, current int, err error) {
	j := t.file()
	b, err := j.read()
	if err != nil || b == nil {
		return nil, -1, err
	}
	var f tabsFile
	if err := j.decode(b, &f); err != nil {
		return nil, -1, err
	}
	if f.Version != tabsVersion {
		return nil, -1, fmt.Errorf("tabs %s: format %d, not %d", t.Path, f.Version, tabsVersion)
	}
	current = -1
	for i, tab := range f.Tabs {
		if !tab.valid() || len(tabs) == MaxTabs {
			continue
		}
		if i == f.Current {
			current = len(tabs)
		}
		tabs = append(tabs, tab)
	}
	return tabs, current, nil
}

// Save replaces the file with tabs, the one at current on screen (-1 for
// none), keeping the first MaxTabs. It waits for the lock beside the file
// for tabsLockWait at most, then gives up: the exit must not hang on it.
func (t Tabs) Save(tabs []Tab, current int) error {
	if t.Path == "" {
		return errors.New("no tabs file")
	}
	if len(tabs) > MaxTabs {
		tabs = tabs[:MaxTabs]
	}
	if current < 0 || current >= len(tabs) {
		current = -1
	}
	if tabs == nil {
		tabs = []Tab{} // "tabs": [], not null
	}
	out, err := json.MarshalIndent(tabsFile{Version: tabsVersion, Current: current, Tabs: tabs}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tabs: %w", err)
	}
	j := t.file()
	unlock, err := j.lockWithin(tabsLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	if err := fsutil.AtomicWriteFile(j.path, append(out, '\n')); err != nil {
		return fmt.Errorf("save tabs: %w", err)
	}
	return nil
}

func (t Tabs) file() jsonFile {
	return jsonFile{path: t.Path, what: "tabs"}
}
