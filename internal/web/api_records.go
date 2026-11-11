package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/image/draw"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/records"
)

func (s *Server) listRecords(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Records.All())
}

func (s *Server) createRecord(w http.ResponseWriter, r *http.Request) {
	var rec model.Record
	if err := decodeJSON(w, r, &rec); err != nil {
		s.writeErr(w, r, err)
		return
	}
	rec, err := s.deps.Records.Create(rec)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// updateRecord replaces a record. A body id that differs from the path
// renames it; see records.Store.Update.
func (s *Server) updateRecord(w http.ResponseWriter, r *http.Request) {
	var rec model.Record
	if err := decodeJSON(w, r, &rec); err != nil {
		s.writeErr(w, r, err)
		return
	}
	rec, err := s.deps.Records.Update(r.PathValue("id"), rec)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Records.Delete(r.PathValue("id")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type playRequest struct {
	AreaID string `json:"area_id"`
}

type playResponse struct {
	Player string `json:"player"`
}

// playRecord is the editor's test play: the record plays as a tag scan
// would play it.
func (s *Server) playRecord(w http.ResponseWriter, r *http.Request) {
	var req playRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.AreaID == "" {
		s.writeErr(w, r, badRequest("area_id is required"))
		return
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), playTimeout)
	defer cancel()
	player, err := s.deps.Play.PlayRecord(ctx, id, req.AreaID)
	if err != nil {
		s.log.Warn("web: test play failed", "record", id, "area", req.AreaID, "err", err)
		writePlayErr(w, "play", err)
		return
	}
	writeJSON(w, http.StatusOK, playResponse{Player: player})
}

// Thumbnail size limits for ?size=.
const (
	minThumb = 16
	maxThumb = 1200
)

// recordArtwork serves the record's label art as JPEG. ?size=N scales it
// to N×N for list thumbnails; ?refresh=1 skips the art cache.
//
// The art behind one URL changes when "Refresh art" finds a new image, so
// the response carries an ETag (a hash of the JPEG) with no-cache: browsers
// revalidate and get a 304 while the art is the same. Computing the ETag
// costs a decode from the disk cache plus an encode, which is fine for the
// few dozen thumbnails a screen shows.
func (s *Server) recordArtwork(w http.ResponseWriter, r *http.Request) {
	rec, err := s.deps.Records.Get(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.serveArtwork(w, r, s.deps.Records.Subject(rec))
}

// mediaArtwork serves the art of a bare media link, for the item rows of
// the record editor. Only music links: names have no art of their own.
func (s *Server) mediaArtwork(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if !records.IsURL(u) {
		s.writeErr(w, r, badRequest("url must be a music link"))
		return
	}
	if _, err := catalog.Parse(u); err != nil {
		s.writeErr(w, r, badRequest("url: %v", err))
		return
	}
	s.serveArtwork(w, r, model.Subject{Name: u, URL: u})
}

// serveArtwork writes the subject's art as a JPEG: size (px) scales it
// down, and refresh=1 looks it up again.
func (s *Server) serveArtwork(w http.ResponseWriter, r *http.Request, sub model.Subject) {
	var err error
	q := r.URL.Query()
	refresh := q.Get("refresh") == "1"
	size := 0
	if v := q.Get("size"); v != "" {
		size, err = strconv.Atoi(v)
		if err != nil || size < minThumb || size > maxThumb {
			s.writeErr(w, r, badRequest("size must be %d..%d", minThumb, maxThumb))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), artworkTimeout)
	defer cancel()
	img, err := s.deps.Artwork.Artwork(ctx, sub, refresh)
	if err != nil {
		s.log.Debug("web: no artwork", "record", sub.ID, "url", sub.URL, "err", err)
		writeError(w, http.StatusNotFound, fmt.Sprintf("no artwork: %v", err))
		return
	}
	if size > 0 {
		img = scale(img, size)
	}
	s.writeJPEG(w, r, img, refresh)
}

// writeJPEG serves img with an ETag, so browsers revalidate cheaply.
// noStore is for a refresh, whose result must not be reused.
func (s *Server) writeJPEG(w http.ResponseWriter, r *http.Request, img image.Image, noStore bool) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		s.writeErr(w, r, fmt.Errorf("encode artwork: %w", err))
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	if noStore {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	// ServeContent answers If-None-Match with a 304.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(buf.Bytes()))
}

// scale fits img into a size×size square (art is already square).
func scale(img image.Image, size int) image.Image {
	b := img.Bounds()
	if b.Dx() <= size && b.Dy() <= size {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}
