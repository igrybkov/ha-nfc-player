// Package registry is the tag registry (tags.yaml): validation, slugs and CRUD.
// It implements model.EntryLookup.
//
// Owner: track C (docs/PLAN.md section 10).
package registry

import (
	"errors"

	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// TagsFile is the content of tags.yaml.
type TagsFile struct {
	Version int           `yaml:"version" json:"version"`
	Tags    []model.Entry `yaml:"tags" json:"tags"`
}

var (
	ErrNotFound  = errors.New("entry not found")
	ErrDuplicate = errors.New("duplicate entry")
)

// Registry is the tag registry backed by a store file.
type Registry struct {
	file *store.File[TagsFile]
}

var _ model.EntryLookup = (*Registry)(nil)

// New returns a Registry on f.
func New(f *store.File[TagsFile]) *Registry { return &Registry{file: f} }

func (r *Registry) ByTagID(tagID string) (model.Entry, bool) {
	_ = r.file
	panic("TODO: track C")
}

func (r *Registry) ByName(name string) (model.Entry, bool) { panic("TODO: track C") }

func (r *Registry) All() []model.Entry { panic("TODO: track C") }

func (r *Registry) Get(id string) (model.Entry, error) { panic("TODO: track C") }

// Create validates e, fills ID from the name if empty, and stores it.
func (r *Registry) Create(e model.Entry) (model.Entry, error) { panic("TODO: track C") }

func (r *Registry) Update(id string, e model.Entry) (model.Entry, error) { panic("TODO: track C") }

func (r *Registry) Delete(id string) error { panic("TODO: track C") }

// Validate checks required fields and value ranges.
func Validate(e model.Entry) error { panic("TODO: track C") }

// Slug makes a stable id from a name ("Metallica — Essential" → "metallica-essential").
func Slug(name string) string { panic("TODO: track C") }
