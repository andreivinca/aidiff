package t3client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/umputun/revdiff/app/fsutil"
)

// TokenStore keeps one bearer token per T3 server origin in a 0600 JSON
// file. Changes take an exclusive lock on a file beside it, so two aidiff
// instances saving at once both keep their token.
type TokenStore struct {
	Path string
	now  func() time.Time // the clock tokens are dated by; time.Now when nil
}

type tokenFile struct {
	Servers map[string]storedToken `json:"servers"`
}

type storedToken struct {
	Token   string    `json:"token"`
	SavedAt time.Time `json:"savedAt"`
}

// Get returns the saved token for baseURL, or "" when none is saved.
func (s TokenStore) Get(baseURL string) (string, error) {
	f, err := s.load()
	if err != nil {
		return "", err
	}
	return f.Servers[normalizeURL(baseURL)].Token, nil
}

// Put saves token for baseURL, replacing any earlier one.
func (s TokenStore) Put(baseURL, token string) error {
	saved := storedToken{Token: token, SavedAt: s.clock().UTC()}
	return s.update(func(f tokenFile) bool {
		f.Servers[normalizeURL(baseURL)] = saved
		return true
	})
}

// Servers lists the servers with a saved token, most recently saved first.
func (s TokenStore) Servers() ([]string, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(f.Servers))
	for k := range f.Servers {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := f.Servers[keys[i]].SavedAt, f.Servers[keys[j]].SavedAt
		if a.Equal(b) {
			return keys[i] < keys[j]
		}
		return a.After(b)
	})
	return keys, nil
}

// remove forgets the token for baseURL.
func (s TokenStore) remove(baseURL string) error {
	key := normalizeURL(baseURL)
	return s.update(func(f tokenFile) bool {
		if _, ok := f.Servers[key]; !ok {
			return false
		}
		delete(f.Servers, key)
		return true
	})
}

func (s TokenStore) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// update changes the file under its lock: change edits the loaded servers
// and reports whether there is anything to save.
func (s TokenStore) update(change func(tokenFile) bool) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	if !change(f) {
		return nil
	}
	return s.save(f)
}

// lock creates the token directory and takes the store's exclusive lock,
// returning its release. The directory may be shared with other settings, so
// an existing one keeps its mode: the token file itself is written 0600.
func (s TokenStore) lock() (unlock func(), err error) {
	if err = os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return nil, fmt.Errorf("create token directory: %w", err)
	}
	lf, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open token lock: %w", err)
	}
	if err = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		_ = lf.Close()
		return nil, fmt.Errorf("lock T3 tokens: %w", err)
	}
	return func() {
		_ = lf.Close() // closing the descriptor releases the lock
	}, nil
}

func (s TokenStore) save(f tokenFile) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode T3 tokens: %w", err)
	}
	if err := fsutil.AtomicWriteFile(s.Path, append(b, '\n')); err != nil {
		return fmt.Errorf("save T3 token: %w", err)
	}
	return nil
}

func (s TokenStore) load() (tokenFile, error) {
	f := tokenFile{Servers: map[string]storedToken{}}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read T3 tokens: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("parse T3 tokens %s: %w", s.Path, err)
	}
	if f.Servers == nil {
		f.Servers = map[string]storedToken{}
	}
	return f, nil
}
