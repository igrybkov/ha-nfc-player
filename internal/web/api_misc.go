package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// haTag is an HA tag with the registry entry it resolves to, if any.
type haTag struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	LastScanned *time.Time `json:"last_scanned"`
	EntryID     string     `json:"entry_id"` // empty = not linked
}

func (s *Server) haTags(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), haTimeout)
	defer cancel()
	tags, err := s.deps.HATags.Tags(ctx)
	if err != nil {
		s.log.Warn("web: list HA tags", "err", err)
		writeError(w, http.StatusBadGateway, "Home Assistant: "+err.Error())
		return
	}
	out := make([]haTag, 0, len(tags))
	for _, t := range tags {
		out = append(out, haTag{ID: t.ID, Name: t.Name, LastScanned: t.LastScanned, EntryID: s.resolve(t.ID, t.Name)})
	}
	writeJSON(w, http.StatusOK, out)
}

// maxTagName caps a new HA tag name. HA sets no limit; this only stops junk.
const maxTagName = 200

// renameHATag renames an HA tag, so a new tag can get its name from the
// Unknown scans screen. It returns the tag as haTags lists it.
func (s *Server) renameHATag(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeErr(w, r, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > maxTagName {
		writeError(w, http.StatusBadRequest, "name must be 1 to 200 characters")
		return
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), haTimeout)
	defer cancel()
	if err := s.deps.HATags.RenameTag(ctx, id, name); err != nil {
		var haErr *ha.Error
		if errors.As(err, &haErr) && haErr.Code == "not_found" {
			writeError(w, http.StatusNotFound, "Home Assistant has no tag "+id)
			return
		}
		s.log.Warn("web: rename HA tag", "tag_id", id, "err", err)
		writeError(w, http.StatusBadGateway, "Home Assistant: "+err.Error())
		return
	}
	s.log.Info("web: renamed HA tag", "tag_id", id, "name", name)
	writeJSON(w, http.StatusOK, haTag{ID: id, Name: name, EntryID: s.resolve(id, name)})
}

// resolve mirrors the scan matching order: tag id, then normalized name.
func (s *Server) resolve(tagID, name string) string {
	if e, ok := s.deps.Registry.ByTagID(tagID); ok {
		return e.ID
	}
	if e, ok := s.deps.Registry.ByName(model.NormalizeName(name)); ok {
		return e.ID
	}
	return ""
}

func (s *Server) haAreas(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), haTimeout)
	defer cancel()
	areas, err := s.deps.Play.Areas(ctx)
	if err != nil {
		s.log.Warn("web: list areas", "err", err)
		writeError(w, http.StatusBadGateway, "Home Assistant: "+err.Error())
		return
	}
	if areas == nil {
		areas = []model.AreaInfo{}
	}
	writeJSON(w, http.StatusOK, areas)
}

// scanView is a scan plus the entry it resolves to now. An unknown scan
// stops being unknown once an entry for it exists. Only tag scans resolve:
// for events and UI commands TagName is a description, not an HA tag name.
// HAName is the tag's name in HA now, which differs from TagName after a
// rename; it is empty when HA is unreachable.
type scanView struct {
	model.Scan
	HAName       string `json:"ha_name,omitempty"`
	MatchesEntry string `json:"matches_entry"`
	MatchesPool  string `json:"matches_pool"` // pool id the name plays, when no entry matches
}

func (s *Server) scans(w http.ResponseWriter, r *http.Request) {
	recent := s.deps.Scans.Recent()
	names := s.haTagNames(r.Context())
	out := make([]scanView, 0, len(recent))
	for _, sc := range recent {
		v := scanView{Scan: sc}
		if sc.Source == "" || sc.Source == model.SourceTag {
			name := sc.TagName
			if n, ok := names[sc.TagID]; ok {
				v.HAName, name = n, n
			}
			v.MatchesEntry = s.resolve(sc.TagID, name)
			if v.MatchesEntry == "" && s.deps.Pools != nil {
				if p, ok := s.deps.Pools.ByName(model.NormalizeName(name)); ok {
					v.MatchesPool = p.ID
				}
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// haTagNames maps HA tag ids to their names now. It is nil when HA does not
// answer; the scans list then shows the names recorded at scan time.
func (s *Server) haTagNames(ctx context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, haNamesTimeout)
	defer cancel()
	tags, err := s.deps.HATags.Tags(ctx)
	if err != nil {
		s.log.Warn("web: list HA tags for scans", "err", err)
		return nil
	}
	names := make(map[string]string, len(tags))
	for _, t := range tags {
		names[t.ID] = t.Name
	}
	return names
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Settings.Settings())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var v model.Settings
	if err := decodeJSON(w, r, &v); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := s.deps.Settings.Save(v); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Settings.Settings())
}

// saveOffset stores the print offset for one template.
func (s *Server) saveOffset(w http.ResponseWriter, r *http.Request) {
	var o model.Offset
	if err := decodeJSON(w, r, &o); err != nil {
		s.writeErr(w, r, err)
		return
	}
	id := r.PathValue("template")
	if _, err := s.deps.Templates.Get(id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	err := s.deps.Settings.Update(func(v *model.Settings) error {
		v.Print.Offsets[id] = o
		return nil
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// setDefaultBackground makes a background the one prints start with.
func (s *Server) setDefaultBackground(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Backgrounds.Get(id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	err := s.deps.Settings.Update(func(v *model.Settings) error {
		v.Print.DefaultBackground = id
		return nil
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"default_background": id})
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	all, err := s.deps.Templates.List()
	if err != nil {
		s.log.Warn("web: load templates", "err", err)
	}
	writeJSON(w, http.StatusOK, all)
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var t labels.Template
	if err := decodeJSON(w, r, &t); err != nil {
		s.writeErr(w, r, err)
		return
	}
	t, err := s.deps.Templates.Create(t)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	var t labels.Template
	if err := decodeJSON(w, r, &t); err != nil {
		s.writeErr(w, r, err)
		return
	}
	t, err := s.deps.Templates.Update(r.PathValue("id"), t)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Templates.Delete(r.PathValue("id")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listBackgrounds(w http.ResponseWriter, r *http.Request) {
	all, err := s.deps.Backgrounds.List()
	if err != nil {
		s.log.Warn("web: load backgrounds", "err", err)
	}
	writeJSON(w, http.StatusOK, all)
}

func (s *Server) createBackground(w http.ResponseWriter, r *http.Request) {
	var b labels.Background
	if err := decodeJSON(w, r, &b); err != nil {
		s.writeErr(w, r, err)
		return
	}
	b, err := s.deps.Backgrounds.Create(b)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) updateBackground(w http.ResponseWriter, r *http.Request) {
	var b labels.Background
	if err := decodeJSON(w, r, &b); err != nil {
		s.writeErr(w, r, err)
		return
	}
	b, err := s.deps.Backgrounds.Update(r.PathValue("id"), b)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) deleteBackground(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Backgrounds.Delete(r.PathValue("id")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type layoutInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// listLayouts lists registered layouts; labels.Layouts sorts them by id.
func (s *Server) listLayouts(w http.ResponseWriter, _ *http.Request) {
	ls := s.lbl.Layouts()
	out := make([]layoutInfo, 0, len(ls))
	for _, l := range ls {
		out = append(out, layoutInfo{ID: l.ID(), Name: l.Name()})
	}
	writeJSON(w, http.StatusOK, out)
}
