// Package scan handles tag_scanned events: event, entry, room, router. It
// also implements model.ScanLog and model.PlayService.
//
// Owner: track A (docs/PLAN.md section 10).
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// EventType is the HA event a scan arrives as.
const EventType = "tag_scanned"

const (
	// LogSize is the scan history length.
	LogSize = 50
	// playTimeout bounds one playback: an artist queue is 50+ service calls.
	playTimeout = 2 * time.Minute
	// Queue sizes. A full queue drops the scan and records an error.
	eventQueueSize = 64
	areaQueueSize  = 8
)

// Deps are the collaborators of a Handler.
type Deps struct {
	HA       ha.Client
	Entries  model.EntryLookup
	Settings model.SettingsSource
	Rooms    *rooms.Resolver
	Router   *player.Router
	Log      model.ScanLog
	Logger   *slog.Logger
}

// Handler handles scans and test plays.
//
// Events are read on the subscription callback and queued at once. One
// dispatcher resolves the reader's area in event order and hands the scan to
// a worker for that area. Each area has one worker, so scans in one room
// play in order and never overlap, while other rooms are not held up. Test
// plays take the same per-area lock.
type Handler struct {
	deps Deps
	log  *slog.Logger
	now  func() time.Time

	mu        sync.Mutex
	areaLocks map[string]*sync.Mutex
}

var _ model.PlayService = (*Handler)(nil)

// New returns a Handler.
func New(d Deps) *Handler {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		deps:      d,
		log:       log.With("component", "scan"),
		now:       time.Now,
		areaLocks: make(map[string]*sync.Mutex),
	}
}

// tagScanned is the data of a tag_scanned event.
type tagScanned struct {
	TagID    string `json:"tag_id"`
	Name     string `json:"name"`
	DeviceID string `json:"device_id"`
}

// Run subscribes to tag_scanned and handles events until ctx is done. It
// waits for running plays to stop before it returns ctx.Err().
func (h *Handler) Run(ctx context.Context) error {
	events := make(chan ha.Event, eventQueueSize)
	err := h.deps.HA.SubscribeEvents(ctx, EventType, func(ev ha.Event) {
		select {
		case events <- ev:
		default:
			h.record(h.scanFromEvent(ev), model.ScanError, "", errors.New("scan queue full, scan dropped"))
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", EventType, err)
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	workers := make(map[string]chan model.Scan)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-events:
			h.dispatch(ctx, ev, workers, &wg)
		}
	}
}

func (h *Handler) scanFromEvent(ev ha.Event) model.Scan {
	var d tagScanned
	_ = json.Unmarshal(ev.Data, &d) // bad data leaves fields empty; the scan is then ignored
	t := ev.TimeFired
	if t.IsZero() {
		t = h.now()
	}
	return model.Scan{Time: t, TagID: d.TagID, TagName: d.Name, DeviceID: d.DeviceID}
}

// dispatch filters the reader and queues the scan on its area's worker.
func (h *Handler) dispatch(ctx context.Context, ev ha.Event, workers map[string]chan model.Scan, wg *sync.WaitGroup) {
	s := h.scanFromEvent(ev)
	areaID, allowed, err := h.deps.Rooms.DeviceArea(ctx, s.DeviceID, h.deps.Settings.Settings().Readers.Integrations)
	switch {
	case err != nil:
		h.record(s, model.ScanError, "", fmt.Errorf("reader area: %w", err))
		return
	case !allowed:
		h.record(s, model.ScanIgnored, "", nil)
		return
	case areaID == "":
		h.record(s, model.ScanError, "", errors.New("reader has no area"))
		return
	}
	s.AreaID = areaID

	q, ok := workers[areaID]
	if !ok {
		q = make(chan model.Scan, areaQueueSize)
		workers[areaID] = q
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case s := <-q:
					h.handle(ctx, s)
				}
			}
		}()
	}
	select {
	case q <- s:
	default:
		h.record(s, model.ScanError, "", errors.New("area busy, scan dropped"))
	}
}

// handle matches the scan to an entry, or to the "Artist — Album" search
// fallback, and plays it.
func (h *Handler) handle(ctx context.Context, s model.Scan) {
	if s.TagName == "" && s.TagID != "" {
		s.TagName = h.tagName(ctx, s.TagID)
	}
	if e, ok := h.match(s); ok {
		s.EntryID = e.ID
		name, err := h.PlayEntry(ctx, e, s.AreaID)
		h.recordPlay(s, name, err)
		return
	}
	q, ok := ParseQuery(s.TagName)
	if !ok {
		h.record(s, model.ScanNoMatch, "", nil)
		return
	}
	name, err := h.playQuery(ctx, q, s.AreaID)
	if errors.Is(err, player.ErrNotFound) {
		h.record(s, model.ScanNoMatch, "", err)
		return
	}
	h.recordPlay(s, name, err)
}

// match finds the entry: tag ID first, then normalized name.
func (h *Handler) match(s model.Scan) (model.Entry, bool) {
	if s.TagID != "" {
		if e, ok := h.deps.Entries.ByTagID(s.TagID); ok {
			return e, true
		}
	}
	if key := model.NormalizeName(s.TagName); key != "" {
		return h.deps.Entries.ByName(key)
	}
	return model.Entry{}, false
}

// tagName looks the name up in the tag registry, for events without one.
func (h *Handler) tagName(ctx context.Context, tagID string) string {
	tags, err := h.deps.HA.Tags(ctx)
	if err != nil {
		h.log.Warn("tag list failed", "err", err)
		return ""
	}
	for _, t := range tags {
		if t.ID == tagID {
			return t.Name
		}
	}
	return ""
}

// ParseQuery reads an unknown tag name of the form "Artist — Album": exactly
// one em dash, both sides non-empty after the "|" cut and trimming.
func ParseQuery(name string) (player.Query, bool) {
	name, _, _ = strings.Cut(name, "|")
	if strings.Count(name, "—") != 1 {
		return player.Query{}, false
	}
	artist, album, _ := strings.Cut(name, "—")
	q := player.Query{Artist: strings.TrimSpace(artist), Album: strings.TrimSpace(album)}
	return q, q.Artist != "" && q.Album != ""
}

// PlayEntry plays an entry in an area. It waits for any other playback in
// that area to finish first.
func (h *Handler) PlayEntry(ctx context.Context, entry model.Entry, areaID string) (string, error) {
	media, err := catalog.Parse(entry.URL)
	if err != nil {
		return "", err
	}
	repeat := entry.Repeat
	if repeat == "" {
		repeat = model.RepeatAll
	}
	var override string
	if entry.Player != nil {
		override = *entry.Player
	}
	return h.play(ctx, areaID, override, func(a rooms.Area) player.Request {
		return player.Request{Media: media, Area: a, Volume: rooms.Volume(entry, a), Shuffle: entry.Shuffle, Repeat: string(repeat)}
	})
}

// playQuery plays the search fallback with the old defaults: area volume,
// no shuffle, repeat all.
func (h *Handler) playQuery(ctx context.Context, q player.Query, areaID string) (string, error) {
	return h.play(ctx, areaID, "", func(a rooms.Area) player.Request {
		return player.Request{Query: &q, Area: a, Volume: a.Volume, Repeat: string(model.RepeatAll)}
	})
}

func (h *Handler) play(ctx context.Context, areaID, override string, build func(rooms.Area) player.Request) (string, error) {
	lock := h.areaLock(areaID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(ctx, playTimeout)
	defer cancel()
	area, err := h.deps.Rooms.Area(ctx, areaID)
	if err != nil {
		return "", err
	}
	return h.deps.Router.Play(ctx, build(area), override)
}

func (h *Handler) areaLock(areaID string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.areaLocks[areaID]
	if !ok {
		l = &sync.Mutex{}
		h.areaLocks[areaID] = l
	}
	return l
}

func (h *Handler) recordPlay(s model.Scan, playerName string, err error) {
	if err != nil {
		h.record(s, model.ScanError, playerName, err)
		return
	}
	h.record(s, model.ScanPlayed, playerName, nil)
}

func (h *Handler) record(s model.Scan, result model.ScanResult, playerName string, err error) {
	s.Result, s.Player = result, playerName
	attrs := []any{"tag_id", s.TagID, "tag_name", s.TagName, "device_id", s.DeviceID, "area_id", s.AreaID, "entry_id", s.EntryID, "result", result}
	if playerName != "" {
		attrs = append(attrs, "player", playerName)
	}
	if err != nil {
		s.Error = err.Error()
		attrs = append(attrs, "err", err)
	}
	switch result {
	case model.ScanError:
		h.log.Error("scan", attrs...)
	case model.ScanIgnored:
		h.log.Debug("scan", attrs...)
	default:
		h.log.Info("scan", attrs...)
	}
	if h.deps.Log != nil {
		h.deps.Log.Add(s)
	}
}

// Areas returns all HA areas with their resolved players and volume.
func (h *Handler) Areas(ctx context.Context) ([]model.AreaInfo, error) {
	areas, err := h.deps.Rooms.Areas(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.AreaInfo, len(areas))
	for i, a := range areas {
		out[i] = model.AreaInfo{ID: a.ID, Name: a.Name, SonosEntity: a.SonosEntity, MAEntity: a.MAEntity, Volume: a.Volume}
	}
	return out, nil
}

// Ring is an in-memory model.ScanLog that keeps the last N scans.
type Ring struct {
	size int

	mu   sync.Mutex
	buf  []model.Scan
	next int // write position once buf is full
}

var _ model.ScanLog = (*Ring)(nil)

// NewRing returns a Ring of the given size (50 in production).
func NewRing(size int) *Ring {
	if size <= 0 {
		size = LogSize
	}
	return &Ring{size: size, buf: make([]model.Scan, 0, size)}
}

func (r *Ring) Add(s model.Scan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) < r.size {
		r.buf = append(r.buf, s)
		return
	}
	r.buf[r.next] = s
	r.next = (r.next + 1) % r.size
}

// Recent returns the scans, newest first.
func (r *Ring) Recent() []model.Scan {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.Scan, 0, len(r.buf))
	// Oldest is at next (when full) or 0; walk backwards from the newest.
	for i := range len(r.buf) {
		out = append(out, r.buf[(r.next-1-i+2*len(r.buf))%len(r.buf)])
	}
	return out
}
