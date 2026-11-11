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
	"github.com/igrybkov/ha-nfc-player/internal/pools"
)

// ArtPicker finds the images the art picker offers. *artwork.Cache
// satisfies it.
type ArtPicker interface {
	Candidates(ctx context.Context, e model.Entry, offset, limit int) (artwork.CandidatePage, error)
	SourceURL(ctx context.Context, e model.Entry) (string, error)
	Preview(ctx context.Context, u string) (image.Image, error)
}

// pickerTimeout bounds one candidates request: artist pages are slow.
const pickerTimeout = 25 * time.Second

// pickerPage is how many linked items one candidates request looks up.
const pickerPage = 12

// maxPoolCandidates caps how many pool items the picker looks up.
const maxPoolCandidates = 100

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

func (s *Server) tagArtCandidates(w http.ResponseWriter, r *http.Request) {
	e, err := s.deps.Registry.Get(r.PathValue("id"))
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
	if e.Pool != "" {
		p, ok := s.deps.Pools.Pool(e.Pool)
		if !ok || offset > 0 {
			writeJSON(w, http.StatusOK, pickerPageResponse{Items: []pickerItem{}, Next: -1})
			return
		}
		writeJSON(w, http.StatusOK, pickerPageResponse{Items: s.poolCandidates(ctx, p), Next: -1})
		return
	}
	page, err := s.deps.ArtPicker.Candidates(ctx, e, offset, pickerPage)
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

func (s *Server) poolArtCandidates(w http.ResponseWriter, r *http.Request) {
	p, ok := s.deps.Pools.Pool(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no pool "+r.PathValue("id"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pickerTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, pickerPageResponse{Items: s.poolCandidates(ctx, p), Next: -1})
}

// poolCandidates offers the art of each pool item, in item order. A tag
// item with an artwork file shows through its tag's art route, so the
// preview proxy never reads files. Items without art are left out.
func (s *Server) poolCandidates(ctx context.Context, p model.Pool) []pickerItem {
	items := p.Items
	if len(items) > maxPoolCandidates {
		items = items[:maxPoolCandidates]
	}
	found := make([]pickerItem, len(items))
	sem := make(chan struct{}, artParallel)
	var wg sync.WaitGroup
	for i, it := range items {
		e := model.Entry{Name: it.Media, URL: it.Media}
		if it.Tag != "" {
			te, err := s.deps.Registry.Get(it.Tag)
			if err != nil {
				continue
			}
			e = te
		} else if !pools.IsURL(it.Media) {
			continue // a name to search for has no art of its own
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			actx, cancel := context.WithTimeout(ctx, artworkTimeout)
			defer cancel()
			u, err := s.deps.ArtPicker.SourceURL(actx, e)
			if err != nil {
				s.log.Debug("web: no art for pool item", "pool", p.ID, "item", e.Name, "err", err)
				return
			}
			preview := previewPath(u)
			if !isHTTPURL(u) {
				preview = "api/tags/" + url.PathEscape(e.ID) + "/artwork"
			}
			found[i] = pickerItem{Candidate: artwork.Candidate{URL: u, Label: e.Name, Kind: "item"}, Preview: preview}
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
