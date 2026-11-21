package chat

// rowKey names the rows of one message as rendered: its kind ('u' the
// user's, 'a' the agent's, 'p' a plan), its text and the width.
type rowKey struct {
	kind rune
	text string
	w    int
}

// rowCache keeps the rendered rows of the transcript's messages, which do
// not change once written, so a scroll or a keystroke does not render the
// whole conversation again: only a new message, one still streaming, or a
// resize does. Each transcript render keeps the rows it used and drops the
// rest, so it holds no more than the conversation on screen. The rows, and
// how a copy reads them, are the cache's own; callers must not change them.
type rowCache struct {
	used, prev map[rowKey]block
}

// rows is the rows of key, rendered by render when the cache does not hold
// them.
func (c *rowCache) rows(key rowKey, render func() block) block {
	if c.used == nil {
		c.used = map[rowKey]block{}
	}
	if r, ok := c.used[key]; ok {
		return r
	}
	r, ok := c.prev[key]
	if !ok {
		r = render()
	}
	c.used[key] = r
	return r
}

// sweep ends a transcript render: what it used is kept, the rest dropped.
func (c *rowCache) sweep() {
	c.prev, c.used = c.used, nil
}
