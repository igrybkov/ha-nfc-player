package records

import (
	"fmt"
	"slices"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// Pick is what to play for a record.
type Pick struct {
	Record model.Record // the record asked for: its settings apply
	Media  string       // the media item picked
	Kind   string       // MediaKind(Media)
	Path   []string     // record ids from Record down to the one holding Media
}

// option is one choice at a level of a pick: a media item, or a record to
// go down into.
type option struct {
	media  string
	record string
}

// Pick chooses media for record id. At each level it takes one item at
// random, all items equal, then goes down into it when it is a record: so
// a record of five artists gives each artist a fifth, whatever each one
// holds. Items that lead to
// no media (empty, missing or looping records) are skipped. The pick
// differs from the last one for id when another is possible.
func (s *Store) Pick(id string) (Pick, error) {
	rs := s.records()
	root, ok := byID(rs, id)
	if !ok {
		return Pick{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := picker{store: s, all: rs, last: s.last[id], dead: map[string]bool{}}
	media, path, idx, ok := p.pick(root, []string{root.ID}, nil)
	if !ok {
		return Pick{}, fmt.Errorf("record %q: %w", id, ErrNothingToPlay)
	}
	s.last[id] = idx
	return Pick{Record: root, Media: media, Kind: MediaKind(media), Path: path}, nil
}

type picker struct {
	store *Store
	all   []model.Record
	last  []int           // option index at each level of the last pick
	dead  map[string]bool // records found to lead to no media in this pick
}

// pick chooses within r. path holds the record ids from the root to r;
// idx the option index chosen at each level above. It backtracks over
// options that lead nowhere.
func (p *picker) pick(r model.Record, path []string, idx []int) (string, []string, []int, bool) {
	opts := p.options(r, path)
	alive := make([]int, 0, len(opts))
	for i, o := range opts {
		if o.record == "" || !p.dead[o.record] {
			alive = append(alive, i)
		}
	}
	// Steer away from the last pick: while this pick follows it, skip the
	// option it took at this level, if there is another.
	skipped := -1
	if len(idx) < len(p.last) && slices.Equal(idx, p.last[:len(idx)]) && len(alive) > 1 {
		if j := slices.Index(alive, p.last[len(idx)]); j >= 0 {
			skipped = alive[j]
			alive = slices.Delete(alive, j, j+1)
		}
	}
	for {
		if len(alive) == 0 {
			if skipped < 0 {
				break
			}
			// Every other option led nowhere: the last one is better than none.
			alive, skipped = []int{skipped}, -1
		}
		k := p.store.intn(len(alive))
		i := alive[k]
		o := opts[i]
		next := append(slices.Clone(idx), i)
		if o.record == "" {
			return o.media, path, next, true
		}
		sub, ok := byID(p.all, o.record)
		if ok {
			if media, subPath, subIdx, ok := p.pick(sub, append(slices.Clone(path), sub.ID), next); ok {
				return media, subPath, subIdx, true
			}
		}
		p.dead[o.record] = true
		alive = slices.Delete(alive, k, k+1)
	}
	return "", nil, nil, false
}

// options lists r's choices in item order. A record already on the path
// is left out, so loops end.
func (p *picker) options(r model.Record, path []string) []option {
	var out []option
	for _, it := range r.Items {
		switch {
		case it.Media != "":
			out = append(out, option{media: it.Media})
		case it.Record != "":
			if !slices.Contains(path, it.Record) {
				out = append(out, option{record: it.Record})
			}
		}
	}
	return out
}

// MediaKind classifies media text for shuffle: a link by its kind, an
// "Artist — Album" name as an album, any other name as other.
func MediaKind(media string) string {
	if m, err := catalog.Parse(media); err == nil {
		switch m.Kind {
		case catalog.KindAlbum:
			return model.MediaAlbum
		case catalog.KindArtist:
			return model.MediaArtist
		case catalog.KindPlaylist:
			return model.MediaPlaylist
		}
		return model.MediaOther
	}
	name, _, _ := strings.Cut(media, "|")
	if strings.Count(name, "—") == 1 {
		a, b, _ := strings.Cut(name, "—")
		if strings.TrimSpace(a) != "" && strings.TrimSpace(b) != "" {
			return model.MediaAlbum
		}
	}
	return model.MediaOther
}

// Subject is what r's label and art are made for. Its art is r's artwork
// override, else the art of r's first item that has some: a media link,
// or a record (its override, else its first item), in item order. A
// record without either gets a subject with no URL: its name is looked up.
func (s *Store) Subject(r model.Record) model.Subject {
	sub := model.Subject{ID: r.ID, Name: r.Name, Label: r.Label}
	if r.Label.Artwork != nil {
		return sub
	}
	art, url := s.firstArt(s.records(), r, map[string]bool{r.ID: true})
	sub.Label.Artwork, sub.URL = art, url
	return sub
}

func (s *Store) firstArt(all []model.Record, r model.Record, seen map[string]bool) (*string, string) {
	for _, it := range r.Items {
		switch {
		case it.Media != "" && IsURL(it.Media):
			return nil, it.Media
		case it.Record != "" && !seen[it.Record]:
			sub, ok := byID(all, it.Record)
			if !ok {
				continue
			}
			if sub.Label.Artwork != nil {
				return sub.Label.Artwork, ""
			}
			seen[sub.ID] = true
			if art, url := s.firstArt(all, sub, seen); art != nil || url != "" {
				return art, url
			}
		}
	}
	return nil, ""
}
