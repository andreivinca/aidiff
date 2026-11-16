package t3client

import (
	"context"
	"errors"
	"strings"
)

// refsLimit is the most refs vcs.listRefs returns in one call.
const refsLimit = 200

// Ref is a branch of a project's repository, local or remote.
type Ref struct {
	Name        string `json:"name"` // "main", or "origin/main" for a remote one
	IsRemote    bool   `json:"isRemote"`
	UpstreamRef string `json:"upstreamRef"` // "refs/remotes/origin/main"; "" without an upstream
	Current     bool   `json:"current"`
	IsDefault   bool   `json:"isDefault"`
}

// Upstream is the ref's upstream as a diff base names it ("origin/main"),
// "" when it has none.
func (r Ref) Upstream() string {
	return strings.TrimPrefix(r.UpstreamRef, "refs/remotes/")
}

// ErrNotRepository is ListRefs' answer for a folder that is no repository.
var ErrNotRepository = errors.New("not a git repository")

// ListRefs lists the branches of the repository at cwd on the server's
// machine, local ones and remote ones, as far as one call returns them. A
// folder that is no repository gets ErrNotRepository.
func (c *Conn) ListRefs(ctx context.Context, cwd string) ([]Ref, error) {
	var res struct {
		IsRepo bool  `json:"isRepo"`
		Refs   []Ref `json:"refs"`
	}
	payload := map[string]any{"cwd": cwd, "refKind": "all", "limit": refsLimit}
	if err := c.call(ctx, "vcs.listRefs", payload, &res); err != nil {
		return nil, err
	}
	if !res.IsRepo {
		return nil, ErrNotRepository
	}
	return res.Refs, nil
}
