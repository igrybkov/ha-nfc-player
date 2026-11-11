package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/pools"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
)

// maxBodyBytes caps JSON request bodies. The largest real body is a settings
// document or a print job with a few hundred entry ids.
const maxBodyBytes = 1 << 20

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// decodeJSON reads one JSON value from a size-limited body. Unknown fields
// are rejected, so a typo in a client fails loudly.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeJSONFrom(http.MaxBytesReader(w, r.Body, maxBodyBytes), v)
}

func decodeJSONFrom(rd io.Reader, v any) error {
	dec := json.NewDecoder(rd)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &requestError{err}
	}
	if dec.More() {
		return &requestError{errors.New("trailing data after JSON value")}
	}
	return nil
}

// requestError marks a malformed request body.
type requestError struct{ err error }

func (e *requestError) Error() string { return "bad request body: " + e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }

// badRequest is a client error found by a handler.
func badRequest(format string, args ...any) error {
	return &requestError{fmt.Errorf(format, args...)}
}

// writeErr maps an error from a store or the request to a status code.
func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	var reqErr *requestError
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &maxErr):
		status = http.StatusRequestEntityTooLarge
	case errors.As(err, &reqErr),
		errors.Is(err, registry.ErrInvalid),
		errors.Is(err, pools.ErrInvalid),
		errors.Is(err, config.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, registry.ErrNotFound), errors.Is(err, pools.ErrNotFound), errors.Is(err, config.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, registry.ErrDuplicate), errors.Is(err, pools.ErrDuplicate), errors.Is(err, config.ErrDuplicate):
		status = http.StatusConflict
	case errors.Is(err, config.ErrReadOnly):
		status = http.StatusForbidden
	}
	if status >= 500 {
		s.log.Error("web: request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	} else {
		s.log.Debug("web: request rejected", "method", r.Method, "path", r.URL.Path, "status", status, "err", err)
	}
	writeError(w, status, err.Error())
}
