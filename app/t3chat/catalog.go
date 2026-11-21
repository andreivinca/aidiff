package t3chat

import (
	"context"
	"sync"

	"github.com/umputun/revdiff/app/t3client"
)

// catalog is a server's models and provider commands as its backends share
// them: fetched once, and again after a failure only when a command needs
// it, never from rendering. One fetch at a time serves every backend
// sharing it, a second pane or the thread switched to mid-fetch included.
type catalog struct {
	mu      sync.Mutex
	value   *t3client.Catalog // nil until fetched
	err     error             // why the last fetch failed
	loading bool              // a fetch is in flight, whichever backend started it
	waiting []func(error)     // what the backends that asked do once it ends
}

// unknown reports a catalog never fetched, or being fetched.
func (c *catalog) unknown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value == nil && c.err == nil
}

// get is the catalog, nil when there is none, and why the last fetch
// failed.
func (c *catalog) get() (*t3client.Catalog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, c.err
}

// fetched keeps what a fetch returned.
func (c *catalog) fetched(cat t3client.Catalog, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.err = err
		return
	}
	c.value, c.err = &cat, nil
}

// request fetches the catalog from srv in the background when it is not
// known: never fetched, or, with retry, failed last time. A fetch already
// in flight is joined rather than started again. done runs once the fetch
// ends, with its error, off every lock.
func (c *catalog) request(srv server, retry bool, done func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != nil || (c.err != nil && !retry) {
		return
	}
	c.waiting = append(c.waiting, done)
	if c.loading {
		return
	}
	c.loading, c.err = true, nil
	go c.fetch(srv)
}

// fetch asks srv for the catalog and tells every backend waiting for it.
func (c *catalog) fetch(srv server) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	cat, err := srv.FetchCatalog(ctx)
	c.fetched(cat, err)
	c.mu.Lock()
	waiting := c.waiting
	c.loading, c.waiting = false, nil
	c.mu.Unlock()
	for _, done := range waiting {
		done(err)
	}
}

// catalogArrived shows the conversation with the catalog a fetch brought,
// or says why there is none.
func (b *Backend) catalogArrived(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.noteLocked("server catalog: " + err.Error())
	}
	b.showLocked()
}

// catalogLocked is the catalog, or why it is not there; mu must be held.
func (b *Backend) catalogLocked() (*t3client.Catalog, string) {
	cat, err := b.catalog.get()
	switch {
	case cat != nil:
		return cat, ""
	case err != nil:
		return nil, "the model list could not be loaded: " + err.Error()
	}
	return nil, "the model list is still loading; try again in a moment"
}

// retryCatalogLocked fetches the catalog again after a failed fetch. A
// command that found no catalog calls it, so the next try can succeed;
// rendering never does. mu must be held.
func (b *Backend) retryCatalogLocked() {
	b.catalog.request(b.srv, true, b.catalogArrived)
}
