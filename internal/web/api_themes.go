package web

import (
	"bytes"
	"net/http"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
)

func (s *Server) listThemes(w http.ResponseWriter, r *http.Request) {
	all, err := s.deps.Themes.List()
	if err != nil {
		s.log.Warn("web: load themes", "err", err)
	}
	writeJSON(w, http.StatusOK, all)
}

func (s *Server) createTheme(w http.ResponseWriter, r *http.Request) {
	var th labels.Theme
	if err := decodeJSON(w, r, &th); err != nil {
		s.writeErr(w, r, err)
		return
	}
	th, err := s.deps.Themes.Create(th)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, th)
}

func (s *Server) updateTheme(w http.ResponseWriter, r *http.Request) {
	var th labels.Theme
	if err := decodeJSON(w, r, &th); err != nil {
		s.writeErr(w, r, err)
		return
	}
	th, err := s.deps.Themes.Update(r.PathValue("id"), th)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, th)
}

func (s *Server) deleteTheme(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Themes.Delete(r.PathValue("id")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// themePreviewRequest is the body of api/themes/preview: one label of an
// unsaved theme on a saved template, or on a format's reference size.
type themePreviewRequest struct {
	Theme      labels.Theme  `json:"theme"`
	TemplateID string        `json:"template_id"` // wins over format
	Format     labels.Format `json:"format"`
	Title      string        `json:"title"`
	Subtitle   string        `json:"subtitle"`
	Cover      string        `json:"cover"`     // a labels.SampleCovers name; "" = no art
	PxPerIn    float64       `json:"px_per_in"` // 0 = default
}

// themePreview renders one label as a PNG, as it prints.
func (s *Server) themePreview(w http.ResponseWriter, r *http.Request) {
	var req themePreviewRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var tpl labels.Template
	if req.TemplateID != "" {
		t, err := s.deps.Templates.Get(req.TemplateID)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		tpl = t
	} else {
		tpl.Format = req.Format
		tpl.Label, tpl.Font = req.Format.Reference()
	}
	if req.Theme.ID == "" {
		req.Theme.ID = "preview" // new themes have no id until saved
	}
	item := labels.Item{Title: req.Title, Subtitle: req.Subtitle}
	if req.Cover != "" {
		img, err := labels.SampleCover(req.Cover)
		if err != nil {
			s.writeErr(w, r, badRequest("%v", err))
			return
		}
		item.Art = img
	}
	var buf bytes.Buffer
	err := s.lbl.RenderPreview(&buf, labels.Preview{
		Template: tpl,
		Theme:    req.Theme,
		Item:     item,
		InsetMM:  s.deps.Settings.Settings().Print.InsetMM,
		PxPerIn:  req.PxPerIn,
	})
	if err != nil {
		s.writeErr(w, r, badRequest("%v", err))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

type formatInfo struct {
	ID         labels.Format `json:"id"`
	DefaultArt labels.Art    `json:"default_art"`
	Round      bool          `json:"round"`
	Label      labels.Size   `json:"label"` // reference size, for previews with no template
}

// labelFormats lists the label formats and art placements the UI offers.
func (s *Server) labelFormats(w http.ResponseWriter, _ *http.Request) {
	formats := make([]formatInfo, 0, len(labels.FormatList))
	for _, f := range labels.FormatList {
		size, _ := f.Reference()
		formats = append(formats, formatInfo{ID: f, DefaultArt: f.DefaultArt(), Round: f.Round(), Label: size})
	}
	writeJSON(w, http.StatusOK, map[string]any{"formats": formats, "placements": labels.ArtPlacements, "covers": labels.SampleCovers})
}
