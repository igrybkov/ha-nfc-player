// Package pools is the pool store (pools.yaml): validation, slugs and CRUD.
// It implements model.PoolLookup and wraps artwork for pool entries.
//
// Owner: track A (docs/PLAN.md section 13).
package pools

import (
	"context"
	"errors"
	"image"

	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// File is the content of pools.yaml.
type File struct {
	Version int          `yaml:"version" json:"version"`
	Pools   []model.Pool `yaml:"pools" json:"pools"`
}

// DefaultFile is the content of a new pools.yaml.
func DefaultFile() File { return File{Version: 1, Pools: []model.Pool{}} }

var (
	ErrNotFound  = errors.New("pool not found")
	ErrDuplicate = errors.New("duplicate pool")
	ErrInvalid   = errors.New("invalid pool")
)

// Store is the pool store backed by a store file.
type Store struct {
	file    *store.File[File]
	entries model.EntryLookup
}

var _ model.PoolLookup = (*Store)(nil)

// New returns a Store on f. entries backs tag items and the "all" pool.
func New(f *store.File[File], entries model.EntryLookup) *Store {
	return &Store{file: f, entries: entries}
}

var errTODO = errors.New("pools: not implemented")

func (s *Store) Pool(id string) (model.Pool, bool)                  { return model.Pool{}, false }
func (s *Store) All() []model.Pool                                  { return []model.Pool{} }
func (s *Store) Get(id string) (model.Pool, error)                  { return model.Pool{}, errTODO }
func (s *Store) Create(p model.Pool) (model.Pool, error)            { return model.Pool{}, errTODO }
func (s *Store) Update(id string, p model.Pool) (model.Pool, error) { return model.Pool{}, errTODO }
func (s *Store) Delete(id string) error                             { return errTODO }

// Artwork wraps an ArtworkSource: a pool entry without label.artwork gets
// the art of its pool's first item with a URL.
type Artwork struct {
	Pools  *Store
	Source model.ArtworkSource
}

var _ model.ArtworkSource = (*Artwork)(nil)

func (a *Artwork) Artwork(ctx context.Context, e model.Entry, refresh bool) (image.Image, error) {
	return a.Source.Artwork(ctx, e, refresh)
}
