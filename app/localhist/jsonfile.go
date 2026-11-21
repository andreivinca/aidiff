package localhist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/umputun/revdiff/app/fsutil"
)

// jsonFile is a list kept as indented JSON at path; what names it in errors
// ("history", "connections"). Both lists of the package are one, so they
// read, lock and write the same way.
type jsonFile struct {
	path string
	what string
}

// load reads the file into v, which a missing file leaves as it is. It never
// changes the file: one it cannot parse is an error.
func (j jsonFile) load(v any) error {
	b, err := j.read()
	if err != nil || b == nil {
		return err
	}
	return j.decode(b, v)
}

// read is the file's content, nil when there is no file.
func (j jsonFile) read() ([]byte, error) {
	b, err := os.ReadFile(j.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", j.what, err)
	}
	return b, nil
}

func (j jsonFile) decode(b []byte, v any) error {
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parse %s %s: %w", j.what, j.path, err)
	}
	return nil
}

// update rewrites the file with edit, under an exclusive lock on a file
// beside it, so two aidiff instances saving at once both keep their change.
// A file that does not parse is moved aside to .corrupt and the list starts
// afresh: kept, it would fail every later change and the list would silently
// stop updating.
func update[T any](j jsonFile, edit func(*T)) error {
	if j.path == "" {
		return fmt.Errorf("no %s file", j.what)
	}
	unlock, err := j.lock()
	if err != nil {
		return err
	}
	defer unlock()
	var v T
	b, err := j.read()
	if err != nil {
		return err
	}
	if b != nil {
		if err := j.decode(b, &v); err != nil {
			if rerr := os.Rename(j.path, j.path+".corrupt"); rerr != nil {
				return fmt.Errorf("%w; move it aside: %w", err, rerr)
			}
			log.Printf("[WARN] %v; moved to %s.corrupt, starting afresh", err, j.path)
			var fresh T
			v = fresh
		}
	}
	edit(&v)
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", j.what, err)
	}
	if err := fsutil.AtomicWriteFile(j.path, append(out, '\n')); err != nil {
		return fmt.Errorf("save %s: %w", j.what, err)
	}
	return nil
}

// lock creates the directory and takes the file's exclusive lock, returning
// its release.
func (j jsonFile) lock() (unlock func(), err error) {
	if err = os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return nil, fmt.Errorf("create %s directory: %w", j.what, err)
	}
	lf, err := os.OpenFile(j.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s lock: %w", j.what, err)
	}
	if err = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		_ = lf.Close()
		return nil, fmt.Errorf("lock %s: %w", j.what, err)
	}
	return func() {
		_ = lf.Close() // closing the descriptor releases the lock
	}, nil
}

// clock is now as a list dates its entries: now when set, else time.Now.
func clock(now func() time.Time) time.Time {
	if now == nil {
		return time.Now()
	}
	return now()
}
