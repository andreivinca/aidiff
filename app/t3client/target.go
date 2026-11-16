package t3client

import (
	"errors"
	"fmt"
	"strings"
)

// Target is what a review looks at: a directory on the T3 host, and the
// thread it belongs to when there is one.
type Target struct {
	Cwd     string
	Thread  *Thread
	Project Project
}

// Label describes the target for the status line and errors.
func (t Target) Label() string {
	if t.Thread != nil {
		return fmt.Sprintf("%s / %s", t.Project.Title, t.Thread.Title)
	}
	return t.Project.Title
}

// Resolve picks the review target. With a thread id (or a unique prefix of
// one) it reviews that thread's worktree, or its project root when the thread
// has none. Without one it takes the most recently updated thread, and with
// no threads the only project.
func Resolve(sh Shell, threadID string) (Target, error) {
	if threadID != "" {
		t, err := findThread(sh, threadID)
		if err != nil {
			return Target{}, err
		}
		return targetFor(sh, t)
	}
	if len(sh.Threads) > 0 {
		threads := SortedThreads(sh)
		return targetFor(sh, &threads[0])
	}
	switch len(sh.Projects) {
	case 0:
		return Target{}, errors.New("T3 server has no projects")
	case 1:
		return Target{Cwd: sh.Projects[0].WorkspaceRoot, Project: sh.Projects[0]}, nil
	}
	return Target{}, errors.New("T3 server has several projects and no threads; pick a thread with --t3-thread")
}

// findThread matches a thread by id, or by a prefix that names one thread.
func findThread(sh Shell, id string) (*Thread, error) {
	var match *Thread
	for i := range sh.Threads {
		t := &sh.Threads[i]
		if t.ID == id {
			return t, nil
		}
		if strings.HasPrefix(t.ID, id) {
			if match != nil {
				return nil, fmt.Errorf("T3 thread prefix %q is ambiguous", id)
			}
			match = t
		}
	}
	if match == nil {
		return nil, fmt.Errorf("T3 thread %q not found (archived threads are not listed)", id)
	}
	return match, nil
}

func targetFor(sh Shell, t *Thread) (Target, error) {
	p, ok := sh.Project(t.ProjectID)
	if !ok {
		return Target{}, fmt.Errorf("T3 thread %s belongs to an unknown project", t.ID)
	}
	cwd := p.WorkspaceRoot
	if t.WorktreePath != nil && *t.WorktreePath != "" {
		cwd = *t.WorktreePath
	}
	th := *t
	return Target{Cwd: cwd, Thread: &th, Project: p}, nil
}
