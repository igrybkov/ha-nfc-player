// Package store keeps one YAML document per file, with atomic writes (temp
// file, fsync, rename) under a mutex and reload when the file mtime changes.
//
// Owner: track C (docs/PLAN.md section 10).
package store

import "sync"

// File is a YAML file holding one value of type T.
type File[T any] struct {
	path string
	def  func() T // value when the file does not exist

	mu sync.Mutex
}

// Open returns a File for path. def builds the value used while the file is missing.
func Open[T any](path string, def func() T) *File[T] {
	return &File[T]{path: path, def: def}
}

// Load returns the current value, re-reading the file if its mtime changed.
func (f *File[T]) Load() (T, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = f.path, f.def
	panic("TODO: track C")
}

// Save writes v atomically.
func (f *File[T]) Save(v T) error { panic("TODO: track C") }

// Update loads, applies fn and saves under one lock.
func (f *File[T]) Update(fn func(*T) error) error { panic("TODO: track C") }

// WriteFileAtomic writes data to a temp file in the same dir, fsyncs and renames.
func WriteFileAtomic(path string, data []byte) error { panic("TODO: track C") }
