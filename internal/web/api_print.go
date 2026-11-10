package web

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// printRequest is the body of api/print/preview and api/print/pdf. The server
// fills the template, background, offsets and bleed values from storage, so
// the UI sends ids only.
type printRequest struct {
	EntryIDs []string `json:"entry_ids"`
	// Sample adds placeholder labels without art, for template previews.
	Sample       int              `json:"sample"`
	TemplateID   string           `json:"template_id"`
	Template     *labels.Template `json:"template"` // unsaved template; wins over template_id
	BackgroundID string           `json:"background_id"`
	Copies       int              `json:"copies"`    // 0 = 1
	StartRow     int              `json:"start_row"` // 1-based; 0 = 1
	StartCol     int              `json:"start_col"` // 1-based; 0 = 1
	Skip         int              `json:"skip"`
	Offset       *model.Offset    `json:"offset"` // nil = saved offset for the template
	Outline      bool             `json:"outline"`
}

// Limits that keep one request from building a huge PDF.
const (
	maxCopies   = 100
	maxSkip     = 1000
	maxSample   = 500
	maxLabels   = 2000
	artParallel = 4
)

type previewResponse struct {
	Cols   int                  `json:"cols"`
	Rows   int                  `json:"rows"`
	Labels int                  `json:"labels"`
	Pages  [][]labels.SlotState `json:"pages"`
}

func (s *Server) printPreview(w http.ResponseWriter, r *http.Request) {
	var req printRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	job, entries, err := s.buildJob(req)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	job.Items = s.items(entries, req.Sample, nil)
	pages := s.lbl.SheetMap(job.Template, job)
	if pages == nil {
		pages = [][]labels.SlotState{}
	}
	writeJSON(w, http.StatusOK, previewResponse{
		Cols:   job.Template.Grid.Cols,
		Rows:   job.Template.Grid.Rows,
		Labels: len(job.Items) * job.Copies,
		Pages:  pages,
	})
}

// printPDF accepts JSON, or a form post with the JSON in the "job" field so
// the UI can open the PDF with a plain <form target="_blank">. Blob URLs and
// window.open are unreliable in the HA companion app webview.
func (s *Server) printPDF(w http.ResponseWriter, r *http.Request) {
	req, err := s.readPrintRequest(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	job, entries, err := s.buildJob(req)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), printTimeout)
	defer cancel()
	var art []image.Image
	if !req.Outline {
		art = s.fetchArt(ctx, entries)
	}
	job.Items = s.items(entries, req.Sample, art)

	var buf bytes.Buffer
	if err := s.lbl.Render(&buf, job); err != nil {
		s.writeErr(w, r, fmt.Errorf("render PDF: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="labels-%s.pdf"`, safeFilename(job.Template.ID)))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) readPrintRequest(w http.ResponseWriter, r *http.Request) (printRequest, error) {
	var req printRequest
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/x-www-form-urlencoded" {
		return req, decodeJSON(w, r, &req)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		return req, &requestError{err}
	}
	job := r.PostForm.Get("job")
	if job == "" {
		return req, badRequest("form field job is required")
	}
	return req, decodeJSONFrom(strings.NewReader(job), &req)
}

// buildJob resolves ids against storage and checks the numbers. Items are
// filled by the caller, since only the PDF needs art.
func (s *Server) buildJob(req printRequest) (labels.Job, []model.Entry, error) {
	var job labels.Job

	switch {
	case req.Template != nil:
		job.Template = *req.Template
		if job.Template.ID == "" {
			// New templates have no id until saved; validation needs one.
			job.Template.ID = "preview"
		}
		if err := s.lbl.ValidateTemplate(job.Template); err != nil {
			return job, nil, badRequest("template: %v", err)
		}
	case req.TemplateID != "":
		t, err := s.deps.Templates.Get(req.TemplateID)
		if err != nil {
			return job, nil, err
		}
		job.Template = t
	default:
		return job, nil, badRequest("template_id or template is required")
	}

	bgID := req.BackgroundID
	if bgID == "" {
		bgID = "white"
	}
	bg, err := s.deps.Backgrounds.Get(bgID)
	if err != nil {
		return job, nil, err
	}
	job.Background = bg

	entries := make([]model.Entry, 0, len(req.EntryIDs))
	for _, id := range req.EntryIDs {
		e, err := s.deps.Registry.Get(id)
		if err != nil {
			return job, nil, err
		}
		entries = append(entries, e)
	}

	job.Copies = max(req.Copies, 1)
	job.StartRow = max(req.StartRow, 1)
	job.StartCol = max(req.StartCol, 1)
	job.Skip = req.Skip
	g := job.Template.Grid
	switch {
	case job.Copies > maxCopies:
		return job, nil, badRequest("copies must be 1..%d", maxCopies)
	case req.Sample < 0 || req.Sample > maxSample:
		return job, nil, badRequest("sample must be 0..%d", maxSample)
	case job.Skip < 0 || job.Skip > maxSkip:
		return job, nil, badRequest("skip must be 0..%d", maxSkip)
	case job.StartRow > g.Rows:
		return job, nil, badRequest("start_row must be 1..%d", g.Rows)
	case job.StartCol > g.Cols:
		return job, nil, badRequest("start_col must be 1..%d", g.Cols)
	case (len(entries)+req.Sample)*job.Copies > maxLabels:
		return job, nil, badRequest("at most %d labels per job", maxLabels)
	case len(entries)+req.Sample == 0:
		return job, nil, badRequest("pick at least one entry")
	}

	settings := s.deps.Settings.Settings()
	if req.Offset != nil {
		job.Offset = *req.Offset
	} else {
		job.Offset = settings.Print.Offsets[job.Template.ID]
	}
	job.InsetMM = settings.Print.InsetMM
	job.BleedMM = settings.Print.BleedMM
	job.EdgeBleedMM = settings.Print.EdgeBleedMM
	job.Outline = req.Outline
	return job, entries, nil
}

// items builds the labels: entries first, then sample placeholders. art is
// parallel to entries and may be nil.
func (s *Server) items(entries []model.Entry, sample int, art []image.Image) []labels.Item {
	out := make([]labels.Item, 0, len(entries)+sample)
	for i, e := range entries {
		title, subtitle := s.lbl.TitleSubtitle(e.Name)
		if e.Label.Title != nil {
			title = *e.Label.Title
		}
		if e.Label.Subtitle != nil {
			subtitle = *e.Label.Subtitle
		}
		it := labels.Item{Title: title, Subtitle: subtitle}
		if i < len(art) {
			it.Art = art[i]
		}
		out = append(out, it)
	}
	for range sample {
		out = append(out, labels.Item{Title: "Artist", Subtitle: "Album title"})
	}
	return out
}

// fetchArt loads art for each entry with limited parallelism. A failure
// leaves that label without art: one missing cover must not block a sheet.
func (s *Server) fetchArt(ctx context.Context, entries []model.Entry) []image.Image {
	art := make([]image.Image, len(entries))
	sem := make(chan struct{}, artParallel)
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			actx, cancel := context.WithTimeout(ctx, artworkTimeout)
			defer cancel()
			img, err := s.deps.Artwork.Artwork(actx, e, false)
			if err != nil {
				s.log.Warn("web: artwork for print", "entry", e.ID, "err", err)
				return
			}
			art[i] = img
		}()
	}
	wg.Wait()
	return art
}

// safeFilename keeps a template id usable in a Content-Disposition header.
func safeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}
