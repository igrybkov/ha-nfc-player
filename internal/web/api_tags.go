package web

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"strconv"

	"golang.org/x/image/draw"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

func (s *Server) listTags(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Registry.All())
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	var e model.Entry
	if err := decodeJSON(w, r, &e); err != nil {
		s.writeErr(w, r, err)
		return
	}
	e, err := s.deps.Registry.Create(e)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) {
	var e model.Entry
	if err := decodeJSON(w, r, &e); err != nil {
		s.writeErr(w, r, err)
		return
	}
	e, err := s.deps.Registry.Update(r.PathValue("id"), e)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Registry.Delete(r.PathValue("id")); err != nil {
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

func (s *Server) playTag(w http.ResponseWriter, r *http.Request) {
	var req playRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if req.AreaID == "" {
		s.writeErr(w, r, badRequest("area_id is required"))
		return
	}
	e, err := s.deps.Registry.Get(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), playTimeout)
	defer cancel()
	player, err := s.deps.Play.PlayEntry(ctx, e, req.AreaID)
	if err != nil {
		s.log.Warn("web: test play failed", "entry", e.ID, "area", req.AreaID, "err", err)
		writeError(w, http.StatusBadGateway, "play failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, playResponse{Player: player})
}

// Thumbnail size limits for ?size=.
const (
	minThumb = 16
	maxThumb = 1200
)

// tagArtwork serves the entry art as JPEG. ?size=N scales it to N×N for list
// thumbnails; ?refresh=1 skips the art cache. The UI adds a version query
// built from the entry URL and art override, so a long max-age is safe.
func (s *Server) tagArtwork(w http.ResponseWriter, r *http.Request) {
	e, err := s.deps.Registry.Get(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
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
	img, err := s.deps.Artwork.Artwork(ctx, e, refresh)
	if err != nil {
		s.log.Debug("web: no artwork", "entry", e.ID, "err", err)
		writeError(w, http.StatusNotFound, fmt.Sprintf("no artwork: %v", err))
		return
	}
	if size > 0 {
		img = scale(img, size)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	if refresh {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=604800")
	}
	if err := jpeg.Encode(w, img, &jpeg.Options{Quality: 85}); err != nil {
		s.log.Debug("web: write artwork", "entry", e.ID, "err", err)
	}
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
