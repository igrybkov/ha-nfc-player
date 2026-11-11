// Package records is the record store (records.yaml): validation, ids,
// lookups by name and alias, art subjects, and the pick of what to play.
package records

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// FileName is the records file in config_dir.
const FileName = "records.yaml"

// File is the content of records.yaml.
type File struct {
	Version int            `yaml:"version" json:"version"`
	Records []model.Record `yaml:"records" json:"records"`
}

// DefaultFile is the content of a new records.yaml.
func DefaultFile() File { return File{Records: []model.Record{}} }

var (
	ErrNotFound  = errors.New("record not found")
	ErrDuplicate = errors.New("duplicate record")
	ErrInvalid   = errors.New("invalid record")
	// ErrNothingToPlay means no item of the record leads to media: it is
	// empty, or holds only records that are empty, missing or loop back.
	ErrNothingToPlay = errors.New("nothing to play")
)

// Store is the record store backed by a store file.
type Store struct {
	file *store.File[File]
	intn func(int) int // random source; tests replace it

	mu   sync.Mutex
	last map[string][]int // per scanned record: item index at each level of the last pick
}

// New returns a Store on f. Open f with DefaultFile as the default.
func New(f *store.File[File]) *Store {
	return &Store{file: f, intn: rand.IntN, last: map[string][]int{}}
}

// records loads the file and applies defaults. A read error is logged and
// the last good content is used: a broken hand edit must not stop playback.
func (s *Store) records() []model.Record {
	f, err := s.file.Load()
	if err != nil {
		slog.Warn("records: load", "path", s.file.Path(), "err", err)
	}
	for i := range f.Records {
		applyDefaults(&f.Records[i])
	}
	return f.Records
}

// All returns every record in file order. The slice is the caller's.
func (s *Store) All() []model.Record {
	rs := s.records()
	if rs == nil {
		rs = []model.Record{}
	}
	return rs
}

// Get finds a record by id.
func (s *Store) Get(id string) (model.Record, error) {
	if r, ok := byID(s.records(), id); ok {
		return r, nil
	}
	return model.Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// ByName finds the record a tag name plays: by normalized name, then by id.
// name is normalized by the caller (model.NormalizeName).
func (s *Store) ByName(name string) (model.Record, bool) {
	if name == "" {
		return model.Record{}, false
	}
	rs := s.records()
	for _, r := range rs {
		if model.NormalizeName(r.Name) == name {
			return r, true
		}
	}
	return byID(rs, name)
}

// Resolve finds the record a play command names. ns is "" for a record
// reference, or "tag" or "pool" for the v1 names: their alias wins, then
// any record id, alias or name.
func (s *Store) Resolve(ns, ref string) (model.Record, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return model.Record{}, false
	}
	rs := s.records()
	if ns != "" {
		for _, r := range rs {
			if slices.Contains(r.Aliases, ns+":"+ref) {
				return r, true
			}
		}
	}
	if r, ok := byID(rs, ref); ok {
		return r, true
	}
	for _, r := range rs {
		for _, a := range r.Aliases {
			// "tag:x" and "pool:x" from v1, plain "x" from a rename.
			if _, id, ok := strings.Cut(a, ":"); a == ref || ok && id == ref {
				return r, true
			}
		}
	}
	return s.ByName(model.NormalizeName(ref))
}

// Create validates r, fills the id from the name if empty, and stores it.
// An id taken by another record gets a numeric suffix when it came from the
// name, and is an ErrDuplicate when the caller chose it.
func (s *Store) Create(r model.Record) (model.Record, error) {
	normalize(&r)
	r.Aliases = nil
	err := s.file.Update(func(f *File) error {
		if r.ID == "" {
			r.ID = uniqueID(Slug(r.Name), f.Records)
		} else if _, ok := byID(f.Records, r.ID); ok {
			return fmt.Errorf("%w: id %q is taken", ErrDuplicate, r.ID)
		}
		if err := validate(r, f.Records, ""); err != nil {
			return err
		}
		f.Records = append(f.Records, r)
		return nil
	})
	if err != nil {
		return model.Record{}, err
	}
	applyDefaults(&r)
	return r, nil
}

// Update replaces the record with the given id. r.ID may differ to rename
// it: references from other records follow, and the old id becomes an
// alias so play events that name it keep working. Aliases are not
// editable; the stored ones are kept.
func (s *Store) Update(id string, r model.Record) (model.Record, error) {
	normalize(&r)
	if r.ID == "" {
		r.ID = id
	}
	err := s.file.Update(func(f *File) error {
		i := slices.IndexFunc(f.Records, func(x model.Record) bool { return x.ID == id })
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		r.Aliases = f.Records[i].Aliases
		if r.ID != id {
			if _, ok := byID(f.Records, r.ID); ok {
				return fmt.Errorf("%w: id %q is taken", ErrDuplicate, r.ID)
			}
			if !slices.Contains(r.Aliases, id) {
				r.Aliases = append(slices.Clone(r.Aliases), id)
			}
		}
		if err := validate(r, f.Records, id); err != nil {
			return err
		}
		f.Records[i] = r
		if r.ID != id {
			for j := range f.Records {
				for k, it := range f.Records[j].Items {
					if it.Record == id {
						f.Records[j].Items[k].Record = r.ID
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return model.Record{}, err
	}
	applyDefaults(&r)
	return r, nil
}

// Delete removes a record. Items of other records that point to it stay,
// and are skipped at play time until removed.
func (s *Store) Delete(id string) error {
	return s.file.Update(func(f *File) error {
		i := slices.IndexFunc(f.Records, func(x model.Record) bool { return x.ID == id })
		if i < 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		f.Records = slices.Delete(f.Records, i, i+1)
		return nil
	})
}

// UsedBy lists the records that hold an item pointing to id.
func (s *Store) UsedBy(id string) []model.Record {
	var out []model.Record
	for _, r := range s.records() {
		if slices.ContainsFunc(r.Items, func(it model.RecordItem) bool { return it.Record == id }) {
			out = append(out, r)
		}
	}
	return out
}

// validate checks r against the other records; skip is r's id before an
// update. Item records must exist; there is no limit on depth, and loops
// are skipped at play time.
func validate(r model.Record, all []model.Record, skip string) error {
	switch {
	case strings.TrimSpace(r.Name) == "":
		return fmt.Errorf("%w: name is required", ErrInvalid)
	case !idPattern.MatchString(r.ID):
		return fmt.Errorf("%w: id must be lowercase letters, digits and \"-\", not %q", ErrInvalid, r.ID)
	case r.Repeat != nil && !r.Repeat.Valid():
		return fmt.Errorf("%w: repeat must be null, off, one or all, not %q", ErrInvalid, *r.Repeat)
	case r.VolumeOffset != nil && (*r.VolumeOffset < -100 || *r.VolumeOffset > 100):
		return fmt.Errorf("%w: volume offset must be -100..100, not %d", ErrInvalid, *r.VolumeOffset)
	}
	key := model.NormalizeName(r.Name)
	for _, o := range all {
		if o.ID == skip {
			continue
		}
		if model.NormalizeName(o.Name) == key {
			return fmt.Errorf("%w: record %q already has the name %q", ErrDuplicate, o.ID, o.Name)
		}
	}
	for i, it := range r.Items {
		if err := validateItem(it, all, skip, r.ID); err != nil {
			return fmt.Errorf("%w: item %d: %v", ErrInvalid, i+1, err)
		}
	}
	return nil
}

func validateItem(it model.RecordItem, all []model.Record, skip, self string) error {
	set := 0
	for _, b := range []bool{it.Media != "", it.Record != ""} {
		if b {
			set++
		}
	}
	switch {
	case set != 1:
		return errors.New("set exactly one of media and record")
	case utf8.RuneCountInString(it.Name) > maxItemName:
		return fmt.Errorf("name is longer than %d characters", maxItemName)
	case it.Record == self || it.Record == skip && skip != "":
		return errors.New("a record can't hold itself")
	case it.Record != "":
		if _, ok := byID(all, it.Record); !ok {
			return fmt.Errorf("no record with id %q", it.Record)
		}
	case IsURL(it.Media):
		if _, err := catalog.Parse(it.Media); err != nil {
			return fmt.Errorf("media %q: %v", it.Media, err)
		}
	}
	return nil
}

// maxItemName caps an item's display name.
const maxItemName = 200

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

var idPattern = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{Nd}]+(?:-[\p{Ll}\p{Lo}\p{Nd}]+)*$`)

// Slug turns a name into an id: lower case letters and digits joined by "-".
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
		return "record"
	}
	return b.String()
}

func uniqueID(base string, rs []model.Record) string {
	id := base
	for n := 2; ; n++ {
		if _, ok := byID(rs, id); !ok {
			return id
		}
		id = base + "-" + strconv.Itoa(n)
	}
}

func byID(rs []model.Record, id string) (model.Record, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return model.Record{}, false
}

// normalize trims input from the API. An empty player means "settings
// chain", as null does.
func normalize(r *model.Record) {
	r.ID = strings.TrimSpace(r.ID)
	r.Name = strings.TrimSpace(r.Name)
	for i := range r.Items {
		r.Items[i].Media = strings.TrimSpace(r.Items[i].Media)
		r.Items[i].Record = strings.TrimSpace(r.Items[i].Record)
		r.Items[i].Name = strings.TrimSpace(r.Items[i].Name)
	}
	for _, v := range []**string{&r.Player, &r.Label.Title, &r.Label.Subtitle, &r.Label.Artwork} {
		if *v == nil {
			continue
		}
		if t := strings.TrimSpace(**v); t != "" {
			*v = &t
		} else {
			*v = nil
		}
	}
	applyDefaults(r)
}

// applyDefaults fills values missing from hand-written files.
func applyDefaults(r *model.Record) {
	r.Items = dedupe(r.Items)
	if r.Aliases == nil {
		r.Aliases = []string{}
	}
}

// dedupe keeps the first of items that play the same thing, in order.
// It runs on every read and save, so a file with duplicates plays without
// them and loses them on its next save.
func dedupe(items []model.RecordItem) []model.RecordItem {
	out := make([]model.RecordItem, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		k := ItemKey(it)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, it)
	}
	return out
}

// ItemKey says what an item plays, so two items with the same key are the
// same: a record by id; a link by service, kind and catalog id, so share
// parameters (?si=) and slugs don't count; other text without regard to
// case and spacing.
func ItemKey(it model.RecordItem) string {
	if it.Record != "" {
		return "record:" + it.Record
	}
	if m, err := catalog.Parse(it.Media); err == nil {
		svc := m.Service
		if svc == "" {
			svc = catalog.ServiceAppleMusic
		}
		return fmt.Sprintf("media:%s:%s:%s", svc, m.Kind, m.ID)
	}
	return "text:" + strings.ToLower(strings.Join(strings.Fields(it.Media), " "))
}
