package t3client

import "context"

// DirEntry is a folder on the server's machine that can be browsed into.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"fullPath"`
}

// Browse lists folders on the server's machine: a path ending in a slash
// lists that folder, any other path the entries of its parent whose names
// start with its last part. It returns the folder that was listed.
func (c *Conn) Browse(ctx context.Context, partialPath string) (parent string, entries []DirEntry, err error) {
	var res struct {
		ParentPath string     `json:"parentPath"`
		Entries    []DirEntry `json:"entries"`
	}
	if err := c.call(ctx, "filesystem.browse", map[string]any{"partialPath": partialPath}, &res); err != nil {
		return "", nil, err
	}
	return res.ParentPath, res.Entries, nil
}

// CreateProject registers a folder on the server's machine as a project.
func (c *Conn) CreateProject(ctx context.Context, title, workspaceRoot string) (Project, error) {
	payload := map[string]any{
		"type":          "project.create",
		"commandId":     NewID(),
		"projectId":     NewID(),
		"title":         title,
		"workspaceRoot": workspaceRoot,
	}
	var p Project
	if err := c.call(ctx, "projects.mutate", payload, &p); err != nil {
		return Project{}, err
	}
	return p, nil
}
