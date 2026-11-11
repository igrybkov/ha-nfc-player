package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"image"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

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
	maxSample   = labels.MaxSlots // a template outline preview fills one sheet
	maxLabels   = 2000
	artParallel = 4
)

type previewResponse struct {
	Cols   int                  `json:"cols"`
	Rows   int                  `json:"rows"`
	Labels int                  `json:"labels"`
	Pages  [][]labels.SlotState `json:"pages"`
	// MissingArt lists the picked entries that would print without art.
	// Empty for outline previews, which print no art.
	MissingArt []entryRef `json:"missing_art"`
}

type entryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// missingArtHeader carries, on the PDF, the number of picked entries that
// printed without art. Each of them printed once per copy.
const missingArtHeader = "X-Missing-Art"

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
	// Art comes from the same source and timeouts as the PDF, so the
	// warning matches what prints. Cached art makes repeat previews cheap.
	missing := []entryRef{}
	if !req.Outline && len(entries) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), printTimeout)
		defer cancel()
		missing = missingArt(entries, s.fetchArt(ctx, entries))
	}
	writeJSON(w, http.StatusOK, previewResponse{
		Cols:       job.Template.Grid.Cols,
		Rows:       job.Template.Grid.Rows,
		Labels:     len(job.Items) * job.Copies,
		Pages:      pages,
		MissingArt: missing,
	})
}

// missingArt lists the entries whose art is nil. art is parallel to entries.
func missingArt(entries []model.Entry, art []image.Image) []entryRef {
	out := []entryRef{}
	for i, e := range entries {
		if art[i] == nil {
			out = append(out, entryRef{ID: e.ID, Name: e.Name})
		}
	}
	return out
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
	if !req.Outline {
		w.Header().Set(missingArtHeader, strconv.Itoa(len(missingArt(entries, art))))
	}
	w.Header().Set("Content-Type", "application/pdf")
	ascii, utf := pdfNames(job, req.EntryIDs)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.pdf"; filename*=UTF-8''%s.pdf`, ascii, url.PathEscape(utf)))
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
	case (job.StartRow-1)*g.Cols+job.StartCol-1+job.Skip >= g.Cols*g.Rows:
		return job, nil, badRequest("start slot plus skip leaves no free slot on the first sheet")
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
func (*Server) items(entries []model.Entry, sample int, art []image.Image) []labels.Item {
	out := make([]labels.Item, 0, len(entries)+sample)
	for i, e := range entries {
		var img image.Image
		if i < len(art) {
			img = art[i]
		}
		out = append(out, labels.ItemFromEntry(e, img))
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

// pdfNameLen is the most characters of label titles in a PDF file name, and
// pdfTitleMin the least one title is cut to, so a long name doesn't crowd out
// the others.
const (
	pdfNameLen  = 35
	pdfTitleMin = 8
)

// pdfNames returns the PDF file name without ".pdf", in ASCII and in UTF-8:
// the label titles in print order, slugged, each cut to its share of
// pdfNameLen (see shares), joined and cut to pdfNameLen,
// then an 8-character hash of the entries and the print settings, so two
// different runs of the same labels get different names. The ASCII name drops
// letters outside a-z (a Cyrillic title vanishes); "labels" stands in when
// nothing is left.
func pdfNames(job labels.Job, entryIDs []string) (ascii, utf string) {
	var titles []string
	for _, it := range job.Items {
		titles = append(titles, it.Title)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%q|%q|%s|%s|%d|%d|%d|%d|%g|%g|%t",
		entryIDs, titles, job.Template.ID, job.Background.ID, job.Copies,
		job.StartRow, job.StartCol, job.Skip, job.Offset.XMM, job.Offset.YMM, job.Outline)
	hash := strings.ToLower(base32.StdEncoding.EncodeToString(h.Sum(nil)))[:8]

	name := func(keep func(rune) bool) string {
		var parts []string
		for _, t := range titles {
			if s := slugKeep(t, keep); s != "" {
				parts = append(parts, s)
			}
		}
		for i, n := range shares(parts) {
			parts[i] = cutRunes(parts[i], n)
		}
		joined := cutRunes(strings.Join(parts, "-"), pdfNameLen)
		if joined == "" {
			joined = "labels"
		}
		return joined + "-" + hash
	}
	ascii = name(func(r rune) bool { return r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) })
	utf = name(func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
	return ascii, utf
}

// shares splits the pdfNameLen budget, less the dashes between parts, over
// the parts. A part shorter than an even share keeps its length, and what it
// leaves goes to the longer parts, which share it evenly (the first ones get
// the remainder). No share is under pdfTitleMin; the final cut to
// pdfNameLen then drops what doesn't fit.
func shares(parts []string) []int {
	out := make([]int, len(parts))
	budget := pdfNameLen - max(len(parts)-1, 0)
	long := make([]int, 0, len(parts)) // parts not yet given their length
	for i := range parts {
		long = append(long, i)
	}
	for len(long) > 0 {
		share := max(pdfTitleMin, budget/len(long))
		var rest []int
		for _, i := range long {
			if n := utf8.RuneCountInString(parts[i]); n <= share {
				out[i] = n
				budget -= n
			} else {
				rest = append(rest, i)
			}
		}
		if len(rest) == len(long) { // all long: split what is left
			extra := max(budget-share*len(rest), 0)
			for k, i := range rest {
				out[i] = share
				if k < extra {
					out[i]++
				}
			}
			break
		}
		long = rest
	}
	return out
}

// cutRunes cuts s to at most n runes and drops dashes left at the end.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return strings.TrimRight(s, "-")
}

// slugKeep lower-cases s, keeps the runes keep accepts and turns every run of
// other runes into one "-".
func slugKeep(s string, keep func(rune) bool) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if !keep(r) {
			dash = true
			continue
		}
		if dash && b.Len() > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
		dash = false
	}
	return b.String()
}
