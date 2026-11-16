package t3chat

import (
	"context"
	"sync"

	"github.com/umputun/revdiff/app/t3client"
)

// catalog is a server's models and provider commands as its backends share
// them: fetched once, and again after a failure only when a command needs
// it, never from rendering.
type catalog struct {
	mu    sync.Mutex
	value *t3client.Catalog // nil until fetched
	err   error             // why the last fetch failed
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

// fetching forgets a failure, for a fetch starting again.
func (c *catalog) fetching() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = nil
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

// fetchCatalogLocked asks the server for its catalog in the background and
// shows the result. mu must be held.
func (b *Backend) fetchCatalogLocked() {
	b.loading = true
	b.catalog.fetching()
	go b.fetchCatalog()
}

// fetchCatalog fetches the catalog and shows the conversation with it, or
// says why there is none.
func (b *Backend) fetchCatalog() {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	cat, err := b.srv.FetchCatalog(ctx)
	b.catalog.fetched(cat, err)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loading = false
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
	if _, err := b.catalog.get(); err != nil && !b.loading {
		b.fetchCatalogLocked()
	}
}
