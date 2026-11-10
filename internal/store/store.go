// Package store keeps one YAML document per file, with atomic writes (temp
// file, fsync, rename) under a mutex and reload when the file changes.
//
// Owner: track C (docs/PLAN.md section 10).
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"go.yaml.in/yaml/v3"
)

// File is a YAML file holding one value of type T.
//
// The file is re-read when its mtime, size or inode changes, so an edit made
// outside the process (a git pull on HA) is picked up on the next Load. Load
// decodes the cached bytes on every call, so each caller gets its own value
// and can change it freely.
type File[T any] struct {
	path string
	def  func() T // value when the file does not exist

	mu   sync.Mutex
	stat fs.FileInfo // stat when the file was last read or written; nil = never
	data []byte      // last content that decoded; nil = none yet
	err  error       // decode error of the content on disk, if any
}

// Open returns a File for path. def builds the value used while the file is missing.
func Open[T any](path string, def func() T) *File[T] {
	return &File[T]{path: path, def: def}
}

// Path returns the file path.
func (f *File[T]) Path() string { return f.path }

// Load returns the current value, re-reading the file if it changed.
//
// A missing file gives the default value and is not created: reads never
// write, so the HA config repo gets no files nobody chose to save. The file
// appears on the first Save or Update. If the file does not decode, Load
// returns the last good value (or the default) together with the error, so
// callers can keep working and report the problem.
func (f *File[T]) Load() (T, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}

// Save writes v atomically.
func (f *File[T]) Save(v T) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.save(v)
}

// Update loads, applies fn and saves under one lock. It refuses to run when
// the file on disk does not decode, so a broken hand edit is never
// overwritten. If fn returns an error, nothing is written.
func (f *File[T]) Update(fn func(*T) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, err := f.load()
	if err != nil {
		return err
	}
	if err := fn(&v); err != nil {
		return err
	}
	return f.save(v)
}

// load requires f.mu.
func (f *File[T]) load() (T, error) {
	st, err := os.Stat(f.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Forget what was read before, so a file copied in later (a seed
		// file, a git pull) is read in full on the next Load.
		f.stat, f.data, f.err = nil, nil, nil
		return f.def(), nil
	case err != nil:
		return f.last(), fmt.Errorf("stat %s: %w", f.path, err)
	}
	if !f.changed(st) {
		return f.last(), f.err
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return f.last(), fmt.Errorf("read %s: %w", f.path, err)
	}
	f.stat = st
	if _, err := f.decode(raw); err != nil {
		f.err = fmt.Errorf("parse %s: %w", f.path, err)
		return f.last(), f.err
	}
	f.data, f.err = raw, nil
	return f.decode(raw)
}

// last decodes the last good content. It requires f.mu.
func (f *File[T]) last() T {
	v, _ := f.decode(f.data)
	return v
}

// save requires f.mu.
func (f *File[T]) save(v T) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode %s: %w", f.path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode %s: %w", f.path, err)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	if err := WriteFileAtomic(f.path, buf.Bytes()); err != nil {
		return err
	}
	st, err := os.Stat(f.path)
	if err != nil {
		return err
	}
	f.stat, f.data, f.err = st, buf.Bytes(), nil
	return nil
}

func (f *File[T]) changed(st fs.FileInfo) bool {
	return f.stat == nil ||
		!os.SameFile(f.stat, st) ||
		!st.ModTime().Equal(f.stat.ModTime()) ||
		st.Size() != f.stat.Size()
}

// decode turns raw YAML into a value. Empty input gives the default.
func (f *File[T]) decode(raw []byte) (T, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return f.def(), nil
	}
	var v T
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return f.def(), err
	}
	return v, nil
}

// WriteFileAtomic writes data to a temp file in the same dir, fsyncs and
// renames. The file gets mode 0644: these files are shared with HA and its
// git repo.
func WriteFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(0o644); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir makes the rename durable. Some filesystems do not support fsync on
// a directory; that is not worth failing a save for.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
