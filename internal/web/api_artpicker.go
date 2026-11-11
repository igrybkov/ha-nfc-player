package web

import (
	"context"
	"image"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/artwork"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/records"
)

// ArtPicker finds the images the art picker offers. *artwork.Cache
// satisfies it.
type ArtPicker interface {
	Candidates(ctx context.Context, s model.Subject, offset, limit int) (artwork.CandidatePage, error)
	SourceURL(ctx context.Context, s model.Subject) (string, error)
	Preview(ctx context.Context, u string) (image.Image, error)
}

// pickerTimeout bounds one candidates request: artist pages are slow.
const pickerTimeout = 25 * time.Second

// pickerPage is how many linked items one candidates request looks up.
const pickerPage = 12

// maxItemCandidates caps how many record items the picker looks up.
const maxItemCandidates = 100

// pickerItem is a candidate plus the URL the UI shows it with.
type pickerItem struct {
	artwork.Candidate
	Preview string `json:"preview"`
}

// pickerPageResponse is one page of the picker. Next is the offset to ask
// for next, or -1 at the end.
type pickerPageResponse struct {
	Items []pickerItem `json:"items"`
	Next  int          `json:"next"`
}

// previewPath is the picker thumbnail of an image URL.
func previewPath(u string) string {
	return "api/artwork/preview?url=" + url.QueryEscape(u)
}

// recordArtCandidates offers art for a record. A record of one media link
// gets that link's cover and linked items, paged. Any other record gets
// the art of each of its items on one page.
func (s *Server) recordArtCandidates(w http.ResponseWriter, r *http.Request) {
	rec, err := s.deps.Records.Get(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if offset, err = strconv.Atoi(v); err != nil || offset < 0 {
			s.writeErr(w, r, badRequest("offset must be a number from 0"))
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), pickerTimeout)
	defer cancel()
	if len(rec.Items) != 1 || !records.IsURL(rec.Items[0].Media) {
		if offset > 0 {
			writeJSON(w, http.StatusOK, pickerPageResponse{Items: []pickerItem{}, Next: -1})
			return
		}
		writeJSON(w, http.StatusOK, pickerPageResponse{Items: s.itemCandidates(ctx, rec), Next: -1})
		return
	}
	// The record name keys the art index, as the record's own art does.
	sub := model.Subject{ID: rec.ID, Name: rec.Name, URL: rec.Items[0].Media}
	page, err := s.deps.ArtPicker.Candidates(ctx, sub, offset, pickerPage)
	if err != nil {
		writeError(w, http.StatusBadGateway, "art lookup: "+err.Error())
		return
	}
	out := pickerPageResponse{Items: make([]pickerItem, 0, len(page.Items)), Next: page.Next}
	for _, c := range page.Items {
		out.Items = append(out.Items, pickerItem{Candidate: c, Preview: previewPath(c.URL)})
	}
	writeJSON(w, http.StatusOK, out)
}

// itemCandidates offers the art of each item of rec, in item order. A
// record item whose art is a file shows through that record's art route,
// so the preview proxy never reads files. Names, "others" and items
// without art are left out.
func (s *Server) itemCandidates(ctx context.Context, rec model.Record) []pickerItem {
	items := rec.Items
	if len(items) > maxItemCandidates {
		items = items[:maxItemCandidates]
	}
	found := make([]pickerItem, len(items))
	sem := make(chan struct{}, artParallel)
	var wg sync.WaitGroup
	for i, it := range items {
		var sub model.Subject
		switch {
		case it.Record != "":
			sr, err := s.deps.Records.Get(it.Record)
			if err != nil {
				continue
			}
			sub = s.deps.Records.Subject(sr)
		case records.IsURL(it.Media):
			sub = model.Subject{Name: it.Media, URL: it.Media}
		default:
			continue // a name to search for has no art of its own
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			actx, cancel := context.WithTimeout(ctx, artworkTimeout)
			defer cancel()
			u, err := s.deps.ArtPicker.SourceURL(actx, sub)
			if err != nil {
				s.log.Debug("web: no art for record item", "record", rec.ID, "item", sub.Name, "err", err)
				return
			}
			preview := previewPath(u)
			switch {
			case isHTTPURL(u):
			case sub.ID != "":
				preview = "api/records/" + url.PathEscape(sub.ID) + "/artwork"
			default:
				preview = "api/artwork/media?url=" + url.QueryEscape(sub.URL)
			}
			found[i] = pickerItem{Candidate: artwork.Candidate{URL: u, Label: sub.Name, Kind: "item"}, Preview: preview}
		}()
	}
	wg.Wait()
	out := []pickerItem{}
	seen := map[string]bool{}
	for _, f := range found {
		if f.URL != "" && !seen[f.URL] {
			seen[f.URL] = true
			out = append(out, f)
		}
	}
	return out
}

// artPreview serves a picker thumbnail. The picker only loads hosts the
// lookup chain may use; see artwork.Cache.Preview.
func (s *Server) artPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size := 200
	if v := q.Get("size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minThumb || n > maxThumb {
			s.writeErr(w, r, badRequest("size must be %d..%d", minThumb, maxThumb))
			return
		}
		size = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), artworkTimeout)
	defer cancel()
	img, err := s.deps.ArtPicker.Preview(ctx, q.Get("url"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no preview: "+err.Error())
		return
	}
	s.writeJPEG(w, r, scale(img, size), false)
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}
