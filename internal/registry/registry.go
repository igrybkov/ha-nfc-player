// Package registry is the tag registry (tags.yaml): validation, slugs and CRUD.
// It implements model.EntryLookup.
//
// Owner: track C (docs/PLAN.md section 10).
package registry

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// TagsFile is the content of tags.yaml.
type TagsFile struct {
	Version int           `yaml:"version" json:"version"`
	Tags    []model.Entry `yaml:"tags" json:"tags"`
}

// DefaultTagsFile is the content of a new tags.yaml.
func DefaultTagsFile() TagsFile { return TagsFile{Version: 1, Tags: []model.Entry{}} }

var (
	ErrNotFound  = errors.New("entry not found")
	ErrDuplicate = errors.New("duplicate entry")
	ErrInvalid   = errors.New("invalid entry")
)

// Registry is the tag registry backed by a store file.
type Registry struct {
	file *store.File[TagsFile]
}

var _ model.EntryLookup = (*Registry)(nil)

// New returns a Registry on f. Open f with DefaultTagsFile as the default.
func New(f *store.File[TagsFile]) *Registry { return &Registry{file: f} }

// entries loads the file and applies defaults. A read error is logged and the
// last good content is used: a broken hand edit must not stop playback.
func (r *Registry) entries() []model.Entry {
	tf, err := r.file.Load()
	if err != nil {
		slog.Warn("registry: load tags", "path", r.file.Path(), "err", err)
	}
	for i := range tf.Tags {
		applyDefaults(&tf.Tags[i])
	}
	return tf.Tags
}

func (r *Registry) ByTagID(tagID string) (model.Entry, bool) {
	if tagID == "" {
		return model.Entry{}, false
	}
	for _, e := range r.entries() {
		if slices.Contains(e.TagIDs, tagID) {
			return e, true
		}
	}
	return model.Entry{}, false
}

// ByName finds an entry by its normalized name (model.NormalizeName).
func (r *Registry) ByName(name string) (model.Entry, bool) {
	if name == "" {
		return model.Entry{}, false
	}
	for _, e := range r.entries() {
		if model.NormalizeName(e.Name) == name {
			return e, true
		}
	}
	return model.Entry{}, false
}

// All returns every entry in file order. The slice is the caller's.
func (r *Registry) All() []model.Entry {
	es := r.entries()
	if es == nil {
		es = []model.Entry{}
	}
	return es
}

func (r *Registry) Get(id string) (model.Entry, error) {
	for _, e := range r.entries() {
		if e.ID == id {
			return e, nil
		}
	}
	return model.Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Create validates e, fills ID from the name if empty, and stores it.
// An ID taken by another entry gets a numeric suffix when it came from the
// name, and is an ErrDuplicate when the caller chose it.
func (r *Registry) Create(e model.Entry) (model.Entry, error) {
	normalize(&e)
	if err := Validate(e); err != nil {
		return model.Entry{}, err
	}
	err := r.file.Update(func(tf *TagsFile) error {
		if e.ID == "" {
			e.ID = uniqueID(Slug(e.Name), tf.Tags)
		} else if indexOf(tf.Tags, e.ID) >= 0 {
			return fmt.Errorf("%w: id %q is taken", ErrDuplicate, e.ID)
		}
		if err := checkConflicts(e, tf.Tags, ""); err != nil {
			return err
		}
		tf.Tags = append(tf.Tags, e)
		return nil
	})
	if err != nil {
		return model.Entry{}, err
	}
	return e, nil
}

// Update replaces the entry with the given id. The id does not change; e.ID
// must be empty or equal to id.
func (r *Registry) Update(id string, e model.Entry) (model.Entry, error) {
	if e.ID != "" && e.ID != id {
		return model.Entry{}, fmt.Errorf("%w: id cannot change (%q → %q)", ErrInvalid, id, e.ID)
	}
	e.ID = id
	normalize(&e)
	if err := Validate(e); err != nil {
		return model.Entry{}, err
	}
	err := r.file.Update(func(tf *TagsFile) error {
		i := indexOf(tf.Tags, id)
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if err := checkConflicts(e, tf.Tags, id); err != nil {
			return err
		}
		tf.Tags[i] = e
		return nil
	})
	if err != nil {
		return model.Entry{}, err
	}
	return e, nil
}

func (r *Registry) Delete(id string) error {
	return r.file.Update(func(tf *TagsFile) error {
		i := indexOf(tf.Tags, id)
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		tf.Tags = slices.Delete(tf.Tags, i, i+1)
		return nil
	})
}

// Validate checks required fields, value ranges and the source: a URL that
// catalog.Parse reads (so a bad link is caught on save rather than on scan),
// or a pool id.
func Validate(e model.Entry) error {
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if model.NormalizeName(e.Name) == "" {
		return fmt.Errorf("%w: name has no text before \"|\"", ErrInvalid)
	}
	if err := validateSource(e); err != nil {
		return err
	}
	if !e.Repeat.Valid() {
		return fmt.Errorf("%w: repeat must be off, one or all, not %q", ErrInvalid, e.Repeat)
	}
	if e.Volume != nil && (*e.Volume < 0 || *e.Volume > 100) {
		return fmt.Errorf("%w: volume must be 0..100, not %d", ErrInvalid, *e.Volume)
	}
	if e.Player != nil && strings.TrimSpace(*e.Player) == "" {
		return fmt.Errorf("%w: player must be null or a player name", ErrInvalid)
	}
	for _, id := range e.TagIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%w: empty tag id", ErrInvalid)
		}
	}
	return nil
}

// validateSource checks that exactly one of url and pool is set. A pool must
// look like a pool id; whether the pool exists is checked at play time, since
// pools and tags are edited separately.
func validateSource(e model.Entry) error {
	switch {
	case e.URL == "" && e.Pool == "":
		return fmt.Errorf("%w: url or pool is required", ErrInvalid)
	case e.URL != "" && e.Pool != "":
		return fmt.Errorf("%w: set url or pool, not both", ErrInvalid)
	case e.Pool != "":
		if e.Pool != model.PoolAll && Slug(e.Pool) != e.Pool {
			return fmt.Errorf("%w: pool must be a pool id (lowercase letters, digits, \"-\") or %q, not %q", ErrInvalid, model.PoolAll, e.Pool)
		}
		return nil
	}
	if _, err := catalog.Parse(e.URL); err != nil {
		if errors.Is(err, catalog.ErrUnsupported) {
			return fmt.Errorf("%w: url must be a music.apple.com link or an apple_music://<kind>/<id> URI, not %q", ErrInvalid, e.URL)
		}
		return fmt.Errorf("%w: url: %v", ErrInvalid, err)
	}
	return nil
}

// Slug makes a stable id from a name ("Metallica — Essential" → "metallica-essential").
// Letters and digits of any script are kept, lower-cased; everything else
// becomes a single "-".
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	if b.Len() == 0 {
		return "entry"
	}
	return b.String()
}

// applyDefaults fills values missing from hand-written files.
func applyDefaults(e *model.Entry) {
	if e.Repeat == "" {
		e.Repeat = model.RepeatAll
	}
	if e.TagIDs == nil {
		e.TagIDs = []string{}
	}
}

// normalize trims input from the API and applies defaults.
func normalize(e *model.Entry) {
	e.ID = strings.TrimSpace(e.ID)
	e.Name = strings.TrimSpace(e.Name)
	e.URL = strings.TrimSpace(e.URL)
	e.Pool = strings.TrimSpace(e.Pool)
	ids := make([]string, 0, len(e.TagIDs))
	for _, id := range e.TagIDs {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	e.TagIDs = ids
	trimPtr := func(p **string) {
		if *p == nil {
			return
		}
		if v := strings.TrimSpace(**p); v != "" {
			*p = &v
		} else {
			*p = nil
		}
	}
	trimPtr(&e.Player)
	trimPtr(&e.Label.Title)
	trimPtr(&e.Label.Subtitle)
	trimPtr(&e.Label.Artwork)
	applyDefaults(e)
}

// checkConflicts rejects a name or tag id already used by another entry, so
// a scan always resolves to one entry. self is the id being updated.
func checkConflicts(e model.Entry, all []model.Entry, self string) error {
	key := model.NormalizeName(e.Name)
	for _, o := range all {
		if o.ID == self {
			continue
		}
		if model.NormalizeName(o.Name) == key {
			return fmt.Errorf("%w: name %q is used by %q", ErrDuplicate, e.Name, o.ID)
		}
		for _, id := range e.TagIDs {
			if slices.Contains(o.TagIDs, id) {
				return fmt.Errorf("%w: tag id %q is linked to %q", ErrDuplicate, id, o.ID)
			}
		}
	}
	return nil
}

func indexOf(es []model.Entry, id string) int {
	return slices.IndexFunc(es, func(e model.Entry) bool { return e.ID == id })
}

func uniqueID(base string, es []model.Entry) string {
	id := base
	for n := 2; indexOf(es, id) >= 0; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	return id
}
