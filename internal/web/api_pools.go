package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/pools"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
	"github.com/igrybkov/ha-nfc-player/internal/scan"
)

type poolList struct {
	Pools []model.Pool `json:"pools"`
}

func (s *Server) listPools(w http.ResponseWriter, _ *http.Request) {
	ps := s.deps.Pools.All()
	if ps == nil {
		ps = []model.Pool{}
	}
	writeJSON(w, http.StatusOK, poolList{Pools: ps})
}

func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	var p model.Pool
	if err := decodeJSON(w, r, &p); err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.deps.Pools.Create(p)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) updatePool(w http.ResponseWriter, r *http.Request) {
	var p model.Pool
	if err := decodeJSON(w, r, &p); err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := s.deps.Pools.Update(r.PathValue("id"), p)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deletePool(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Pools.Delete(r.PathValue("id")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkTarget mirrors the Target contract: exactly one of area and player.
func checkTarget(t model.Target) error {
	area, entity := strings.TrimSpace(t.AreaID) != "", strings.TrimSpace(t.Entity) != ""
	switch {
	case !area && !entity:
		return badRequest("area or player is required")
	case area && entity:
		return badRequest("set area or player, not both")
	}
	return nil
}

// play runs a play command from the play box. Scan records it in the scan
// log with Source ui.
func (s *Server) play(w http.ResponseWriter, r *http.Request) {
	var cmd model.PlayCommand
	if err := decodeJSON(w, r, &cmd); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := checkTarget(cmd.Target); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if strings.TrimSpace(cmd.Media+cmd.Tag+cmd.Pool) == "" {
		s.writeErr(w, r, badRequest("media, tag or pool is required"))
		return
	}
	cmd.Source = model.SourceUI
	ctx, cancel := context.WithTimeout(r.Context(), playTimeout)
	defer cancel()
	res, err := s.deps.Play.Play(ctx, cmd)
	if err != nil {
		s.log.Warn("web: play failed", "area", cmd.AreaID, "player", cmd.Entity,
			"media", cmd.Media, "tag", cmd.Tag, "pool", cmd.Pool, "err", err)
		writePlayErr(w, "play", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	var t model.Target
	if err := decodeJSON(w, r, &t); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if err := checkTarget(t); err != nil {
		s.writeErr(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), playTimeout)
	defer cancel()
	name, err := s.deps.Play.Stop(ctx, t)
	if err != nil {
		s.log.Warn("web: stop failed", "area", t.AreaID, "player", t.Entity, "err", err)
		writePlayErr(w, "stop", err)
		return
	}
	writeJSON(w, http.StatusOK, playResponse{Player: name})
}

// writePlayErr maps a play or stop error. Lookups that miss (pool, tag,
// area, search) and invalid input are the client's. A nested or empty pool,
// or an unknown player name, is a 409: the request is fine but the stored
// pool or tag cannot be played until it is edited. The rest is the player or HA failing, a 502 as in test play.
func writePlayErr(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, pools.ErrNotFound), errors.Is(err, registry.ErrNotFound),
		errors.Is(err, rooms.ErrUnknownArea), errors.Is(err, rooms.ErrUnknownEntity),
		errors.Is(err, player.ErrNotFound):
		writeError(w, http.StatusNotFound, what+" failed: "+err.Error())
	case errors.Is(err, pools.ErrInvalid), errors.Is(err, registry.ErrInvalid),
		errors.Is(err, rooms.ErrUnsupportedEntity), errors.Is(err, scan.ErrInvalid):
		writeError(w, http.StatusBadRequest, what+" failed: "+err.Error())
	case errors.Is(err, scan.ErrNestedPool), errors.Is(err, scan.ErrEmptyPool),
		errors.Is(err, player.ErrUnknownPlayer):
		writeError(w, http.StatusConflict, what+" failed: "+err.Error())
	default:
		writeError(w, http.StatusBadGateway, what+" failed: "+err.Error())
	}
}
