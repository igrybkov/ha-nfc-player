// Package pools is the pool store (pools.yaml): validation, slugs and CRUD.
// It implements model.PoolLookup and wraps artwork for pool entries.
//
// Owner: track A (docs/PLAN.md section 13).
package pools

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
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

// AllName is the name of the built-in model.PoolAll pool.
const AllName = "All tags"

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

// pools loads the file and applies defaults. A read error is logged and the
// last good content is used: a broken hand edit must not stop playback.
func (s *Store) pools() []model.Pool {
	f, err := s.file.Load()
	if err != nil {
		slog.Warn("pools: load pools", "path", s.file.Path(), "err", err)
	}
	for i := range f.Pools {
		applyDefaults(&f.Pools[i])
	}
	return f.Pools
}

// Pool finds a pool by id. model.PoolAll is built from the entries: every
// entry as a tag item, in file order, except entries that play a pool.
func (s *Store) Pool(id string) (model.Pool, bool) {
	if id == model.PoolAll {
		return s.all(), true
	}
	p, err := s.Get(id)
	return p, err == nil
}

// ByName finds a pool by normalized name or id, for scans of tags that have
// no entry. A stored pool wins over the built-in "all".
func (s *Store) ByName(name string) (model.Pool, bool) {
	if name == "" {
		return model.Pool{}, false
	}
	for _, p := range s.pools() {
		if model.NormalizeName(p.Name) == name || p.ID == name {
			return p, true
		}
	}
	if name == model.NormalizeName(AllName) || name == model.PoolAll {
		return s.all(), true
	}
	return model.Pool{}, false
}

func (s *Store) all() model.Pool {
	p := model.Pool{ID: model.PoolAll, Name: AllName, Items: []model.PoolItem{}}
	for _, e := range s.entries.All() {
		if e.Pool == "" {
			p.Items = append(p.Items, model.PoolItem{Tag: e.ID})
		}
	}
	return p
}

// All returns the stored pools in file order. The slice is the caller's.
func (s *Store) All() []model.Pool {
	ps := s.pools()
	if ps == nil {
		ps = []model.Pool{}
	}
	return ps
}

func (s *Store) Get(id string) (model.Pool, error) {
	for _, p := range s.pools() {
		if p.ID == id {
			return p, nil
		}
	}
	return model.Pool{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Create validates p, fills ID from the name if empty, and stores it.
// An ID taken by another pool gets a numeric suffix when it came from the
// name, and is an ErrDuplicate when the caller chose it.
func (s *Store) Create(p model.Pool) (model.Pool, error) {
	normalize(&p)
	if err := s.validate(p); err != nil {
		return model.Pool{}, err
	}
	err := s.file.Update(func(f *File) error {
		if p.ID == "" {
			p.ID = uniqueID(slug(p.Name), f.Pools)
		} else if indexOf(f.Pools, p.ID) >= 0 {
			return fmt.Errorf("%w: id %q is taken", ErrDuplicate, p.ID)
		}
		f.Pools = append(f.Pools, p)
		return nil
	})
	if err != nil {
		return model.Pool{}, err
	}
	return p, nil
}

// Update replaces the pool with the given id. An empty p.ID keeps the id; a
// different p.ID renames the pool and must be free. Tag entries that play
// the old id are not changed.
func (s *Store) Update(id string, p model.Pool) (model.Pool, error) {
	normalize(&p)
	if p.ID == "" {
		p.ID = id
	}
	if err := s.validate(p); err != nil {
		return model.Pool{}, err
	}
	err := s.file.Update(func(f *File) error {
		i := indexOf(f.Pools, id)
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if p.ID != id && indexOf(f.Pools, p.ID) >= 0 {
			return fmt.Errorf("%w: id %q is taken", ErrDuplicate, p.ID)
		}
		f.Pools[i] = p
		return nil
	})
	if err != nil {
		return model.Pool{}, err
	}
	return p, nil
}

func (s *Store) Delete(id string) error {
	return s.file.Update(func(f *File) error {
		i := indexOf(f.Pools, id)
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		f.Pools = slices.Delete(f.Pools, i, i+1)
		return nil
	})
}

// validate checks the id, the name, the settings and every item. A tag item
// must name an existing entry that plays a URL: pools don't nest. An entry
// deleted or changed later only fails that item at play time.
func (s *Store) validate(p model.Pool) error {
	if p.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if p.ID == model.PoolAll {
		return fmt.Errorf("%w: id %q is reserved for the built-in pool", ErrInvalid, model.PoolAll)
	}
	if p.ID != "" && registry.Slug(p.ID) != p.ID {
		return fmt.Errorf("%w: id must be lowercase letters, digits and \"-\", not %q", ErrInvalid, p.ID)
	}
	if p.Repeat != nil && !p.Repeat.Valid() {
		return fmt.Errorf("%w: repeat must be null, off, one or all, not %q", ErrInvalid, *p.Repeat)
	}
	if p.Volume != nil && (*p.Volume < 0 || *p.Volume > 100) {
		return fmt.Errorf("%w: volume must be 0..100, not %d", ErrInvalid, *p.Volume)
	}
	if len(p.Items) == 0 {
		return fmt.Errorf("%w: at least one item is required", ErrInvalid)
	}
	entries := s.entries.All()
	for i, it := range p.Items {
		if err := validateItem(it, entries); err != nil {
			return fmt.Errorf("%w: item %d: %v", ErrInvalid, i+1, err)
		}
	}
	return nil
}

func validateItem(it model.PoolItem, entries []model.Entry) error {
	switch {
	case (it.Tag == "") == (it.Media == ""):
		return errors.New("set exactly one of tag and media")
	case it.Tag != "":
		e, ok := entryByID(entries, it.Tag)
		if !ok {
			return fmt.Errorf("no tag entry with id %q", it.Tag)
		}
		if e.Pool != "" {
			return fmt.Errorf("tag %q plays a pool; pools can't nest", it.Tag)
		}
	case IsURL(it.Media):
		if _, err := catalog.Parse(it.Media); err != nil {
			return fmt.Errorf("media %q: %v", it.Media, err)
		}
	}
	return nil
}

// uriPrefix matches the start of a provider URI: apple_music:// and
// spotify://, both also with a Music Assistant instance suffix
// (spotify--RiSye5Z5://), and spotify:<kind>: ("spotify: hits" is a name).
var uriPrefix = regexp.MustCompile(`^(?:(?:apple_music|spotify)(?:--[a-z0-9]+)?://|spotify:[a-z]+:)`)

// IsURL reports whether media text is meant as a URL: http(s):// or a
// provider URI. Such text must catalog.Parse; any other text is a name.
func IsURL(media string) bool {
	m := strings.ToLower(strings.TrimSpace(media))
	return strings.HasPrefix(m, "http://") || strings.HasPrefix(m, "https://") || uriPrefix.MatchString(m)
}

// applyDefaults fills values missing from hand-written files.
func applyDefaults(p *model.Pool) {
	if p.Items == nil {
		p.Items = []model.PoolItem{}
	}
}

// normalize trims input from the API and applies defaults. An empty player
// means "item decides", as an empty entry player does in the registry.
func normalize(p *model.Pool) {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	for i := range p.Items {
		p.Items[i].Tag = strings.TrimSpace(p.Items[i].Tag)
		p.Items[i].Media = strings.TrimSpace(p.Items[i].Media)
	}
	for _, v := range []**string{&p.Player, &p.Label.Title, &p.Label.Subtitle, &p.Label.Artwork} {
		trimPtr(v)
	}
	applyDefaults(p)
}

// trimPtr trims *v and turns an empty string into nil.
func trimPtr(v **string) {
	if *v == nil {
		return
	}
	if t := strings.TrimSpace(**v); t != "" {
		*v = &t
	} else {
		*v = nil
	}
}

// RefPrefix marks a pool in a list of entry ids, such as a print job's.
const RefPrefix = "pool:"

// AsEntry turns a pool into an entry that plays it, so the label and art
// code can treat it as one. Its id is RefPrefix + the pool id.
func AsEntry(p model.Pool) model.Entry {
	return model.Entry{ID: RefPrefix + p.ID, Name: p.Name, Pool: p.ID, Label: p.Label}
}

func entryByID(entries []model.Entry, id string) (model.Entry, bool) {
	i := slices.IndexFunc(entries, func(e model.Entry) bool { return e.ID == id })
	if i < 0 {
		return model.Entry{}, false
	}
	return entries[i], true
}

func indexOf(ps []model.Pool, id string) int {
	return slices.IndexFunc(ps, func(p model.Pool) bool { return p.ID == id })
}

// slug is registry.Slug, with "pool" for a name without letters or digits.
func slug(name string) string {
	if !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return "pool"
	}
	return registry.Slug(name)
}

// uniqueID adds a numeric suffix until the id is free. PoolAll counts as
// taken, so a pool named "All" gets "all-2".
func uniqueID(base string, ps []model.Pool) string {
	taken := func(id string) bool { return id == model.PoolAll || indexOf(ps, id) >= 0 }
	id := base
	for n := 2; taken(id); n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	return id
}

// Artwork wraps an ArtworkSource: a pool entry without label.artwork gets
// its pool's label.artwork, else the art of the pool's first item.
type Artwork struct {
	Pools  *Store
	Source model.ArtworkSource
}

var _ model.ArtworkSource = (*Artwork)(nil)

// Artwork passes e through, unless it is a pool entry without an artwork
// override. Then Source gets a copy of e with the pool's artwork override,
// or else with the art of the pool's first item that has some: a tag item's
// entry (its override, else its URL), or a media item that parses. A pool
// without such an item, or an unknown pool, passes e through.
func (a *Artwork) Artwork(ctx context.Context, e model.Entry, refresh bool) (image.Image, error) {
	if e.Pool != "" && e.Label.Artwork == nil {
		if p, ok := a.Pools.Pool(e.Pool); ok {
			if p.Label.Artwork != nil {
				e.Label.Artwork = p.Label.Artwork
			} else if first, ok := a.first(p); ok {
				e.URL, e.Label.Artwork = first.URL, first.Label.Artwork
			}
		}
	}
	return a.Source.Artwork(ctx, e, refresh)
}

// first returns the pool's first item with art, as an entry: a tag item's
// entry with a URL or an artwork override, or a media item that parses.
func (a *Artwork) first(p model.Pool) (model.Entry, bool) {
	entries := a.Pools.entries.All()
	for _, it := range p.Items {
		if it.Tag != "" {
			if e, ok := entryByID(entries, it.Tag); ok && (e.URL != "" || e.Label.Artwork != nil) {
				return e, true
			}
			continue
		}
		if _, err := catalog.Parse(it.Media); err == nil {
			return model.Entry{Name: it.Media, URL: it.Media}, true
		}
	}
	return model.Entry{}, false
}
