// Package scan handles tag_scanned events (event, record, room, router) and
// the nfc_player_play / nfc_player_stop events. It also implements
// model.ScanLog and model.PlayService.
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

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/records"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// HA events the handler subscribes to.
const (
	EventType     = "tag_scanned"     // a tag scan
	PlayEventType = "nfc_player_play" // a play command, see model.PlayCommand
	StopEventType = "nfc_player_stop" // a stop command, see model.Target
)

const (
	// LogSize is the scan history length.
	LogSize = 50
	// playTimeout bounds one playback: an artist queue is 50+ service calls.
	playTimeout = 2 * time.Minute
	// Queue sizes. A full queue drops the scan and records an error.
	eventQueueSize = 64
	areaQueueSize  = 8
	// areaTimeout bounds the reader area lookup, so a stalled HA can't hold
	// up the dispatcher and every room behind it.
	areaTimeout = 10 * time.Second
	// MaxScanAge is the oldest scan that still plays. An older one waited
	// too long (behind a slow play, or replayed after a reconnect); music
	// starting a minute after the tap would surprise more than help.
	MaxScanAge = 30 * time.Second
)

// Deps are the collaborators of a Handler.
type Deps struct {
	HA       ha.Client
	Records  *records.Store
	Settings model.SettingsSource
	Rooms    *rooms.Resolver
	Router   *player.Router
	Log      model.ScanLog
	Logger   *slog.Logger
}

// Handler handles scans, play and stop commands, and test plays.
//
// Events are read on the subscription callback and queued at once. One
// dispatcher resolves the reader's area (or the command's lock key) in event
// order and hands the event to a worker for that key. Each key has one
// worker, so events in one room play in order and never overlap, while other
// rooms are not held up. Test plays and web commands take the same lock.
type Handler struct {
	deps        Deps
	log         *slog.Logger
	now         func() time.Time // clock; tests replace it
	areaTimeout time.Duration

	mu        sync.Mutex
	areaLocks map[string]chan struct{} // one-slot semaphores, so a wait can give up on ctx
}

var _ model.PlayService = (*Handler)(nil)

// New returns a Handler.
func New(d Deps) *Handler {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		deps:        d,
		log:         log.With("component", "scan"),
		now:         time.Now,
		areaTimeout: areaTimeout,
		areaLocks:   make(map[string]chan struct{}),
	}
}

// queued is a scan or command waiting for its area worker.
type queued struct {
	scan  model.Scan
	fired time.Time          // the event's TimeFired; zero = unknown, so no age check
	cmd   *model.PlayCommand // set for a play event
	stop  *model.Target      // set for a stop event
}

// tagScanned is the data of a tag_scanned event.
type tagScanned struct {
	TagID    string `json:"tag_id"`
	Name     string `json:"name"`
	DeviceID string `json:"device_id"`
}

// Run subscribes to tag_scanned, nfc_player_play and nfc_player_stop and
// handles events until ctx is done. It waits for running plays to stop
// before it returns ctx.Err().
func (h *Handler) Run(ctx context.Context) error {
	// A failed subscribe returns at once; the cancel drops the subscriptions
	// made before it, so the caller's retry doesn't double them.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan ha.Event, eventQueueSize)
	for _, typ := range []string{EventType, PlayEventType, StopEventType} {
		err := h.deps.HA.SubscribeEvents(ctx, typ, func(ev ha.Event) {
			select {
			case events <- ev:
			default:
				h.record(h.scanFromEvent(ev), model.ScanError, "", errors.New("scan queue full, scan dropped"))
			}
		})
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", typ, err)
		}
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	workers := make(map[string]chan queued)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-events:
			h.dispatch(ctx, ev, workers, &wg)
		}
	}
}

// scanFromEvent is the ScanLog record of an event. For a command event it
// only has the time and source; dispatchCommand fills in the rest.
func (h *Handler) scanFromEvent(ev ha.Event) model.Scan {
	t := ev.TimeFired
	if t.IsZero() {
		t = h.now()
	}
	if ev.EventType != EventType {
		return model.Scan{Time: t, Source: model.SourceEvent, TagName: ev.EventType}
	}
	var d tagScanned
	_ = json.Unmarshal(ev.Data, &d) // bad data leaves fields empty; the scan is then ignored
	return model.Scan{Time: t, TagID: d.TagID, TagName: d.Name, DeviceID: d.DeviceID, Source: model.SourceTag}
}

// dispatch filters the reader and queues the scan on its area's worker.
// Command events go to dispatchCommand.
func (h *Handler) dispatch(ctx context.Context, ev ha.Event, workers map[string]chan queued, wg *sync.WaitGroup) {
	if ev.EventType != EventType {
		h.dispatchCommand(ctx, ev, workers, wg)
		return
	}
	s := h.scanFromEvent(ev)
	actx, cancel := context.WithTimeout(ctx, h.areaTimeout)
	areaID, allowed, err := h.deps.Rooms.DeviceArea(actx, s.DeviceID, h.deps.Settings.Settings().Readers.Integrations)
	cancel()
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
	h.enqueue(ctx, areaID, queued{scan: s, fired: ev.TimeFired}, workers, wg)
}

// dispatchCommand decodes a play or stop event and queues it on the worker
// of its lock key. There is no reader filter: anything in HA may send one.
func (h *Handler) dispatchCommand(ctx context.Context, ev ha.Event, workers map[string]chan queued, wg *sync.WaitGroup) {
	s := h.scanFromEvent(ev)
	item := queued{fired: ev.TimeFired}
	t, err := h.decodeCommand(ev, &item)
	switch {
	case item.cmd != nil:
		s = commandScan(s.Time, *item.cmd)
	case item.stop != nil:
		s = stopScan(s.Time, model.SourceEvent, *item.stop)
	}
	if err == nil {
		err = checkTarget(t)
	}
	if err != nil {
		h.record(s, model.ScanError, "", err)
		return
	}
	actx, cancel := context.WithTimeout(ctx, h.areaTimeout)
	key, err := h.lockKey(actx, t)
	cancel()
	if err != nil {
		h.record(s, model.ScanError, "", err)
		return
	}
	item.scan = s
	h.enqueue(ctx, key, item, workers, wg)
}

// decodeCommand reads the event data into item.cmd or item.stop and
// returns the target.
func (h *Handler) decodeCommand(ev ha.Event, item *queued) (model.Target, error) {
	d, err := decodeEventData(ev.Data)
	if err != nil {
		return model.Target{}, err
	}
	if ev.EventType == StopEventType {
		t, err := d.target()
		item.stop = &t
		return t, err
	}
	cmd, err := d.playCommand()
	item.cmd = &cmd
	return cmd.Target, err
}

// enqueue hands an item to the worker for key, starting it if needed.
func (h *Handler) enqueue(ctx context.Context, key string, item queued, workers map[string]chan queued, wg *sync.WaitGroup) {
	q, ok := workers[key]
	if !ok {
		q = make(chan queued, areaQueueSize)
		workers[key] = q
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item := <-q:
					h.handle(ctx, item)
				}
			}
		}()
	}
	select {
	case q <- item:
	default:
		h.record(item.scan, model.ScanError, "", errors.New("area busy, scan dropped"))
	}
}

// handle drops an event older than MaxScanAge. It runs a command, or
// matches a scan to a record, or to the "Artist — Album" search fallback,
// and plays it.
func (h *Handler) handle(ctx context.Context, item queued) {
	s := item.scan
	if !item.fired.IsZero() {
		// A negative age is clock skew between HA and us; play it.
		if age := h.now().Sub(item.fired); age > MaxScanAge {
			h.record(s, model.ScanIgnored, "", fmt.Errorf("event is %s old, over the %s limit; not played", age.Round(time.Second), MaxScanAge))
			return
		}
	}
	switch {
	case item.cmd != nil:
		_, _ = h.runPlay(ctx, *item.cmd, s)
		return
	case item.stop != nil:
		_, _ = h.runStop(ctx, *item.stop, s)
		return
	}
	if s.TagName == "" && s.TagID != "" {
		s.TagName = h.tagName(ctx, s.TagID)
	}
	if r, ok := h.match(s); ok {
		s.RecordID = r.ID
		name, j, err := h.playRecord(ctx, r.ID, s.AreaID)
		s.Media = j.text
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

// match finds the record a scanned tag plays, by its normalized name.
// Records don't know tag ids: renaming a tag in HA moves it to another
// record.
func (h *Handler) match(s model.Scan) (model.Record, bool) {
	return h.deps.Records.ByName(model.NormalizeName(s.TagName))
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

// PlayRecord plays a record in an area, as a tag scan would: one item
// picked, with the record's settings. It waits for any other playback in
// that area to finish first. It is not recorded in the ScanLog.
func (h *Handler) PlayRecord(ctx context.Context, id, areaID string) (string, error) {
	name, _, err := h.playRecord(ctx, id, areaID)
	return name, err
}

// playRecord is PlayRecord that also returns the resolved job, for the scan log.
func (h *Handler) playRecord(ctx context.Context, id, areaID string) (string, job, error) {
	j, err := h.recordJob(id)
	if err != nil {
		return "", j, err
	}
	name, err := h.inArea(ctx, areaID, func(ctx context.Context, a rooms.Area) (string, error) {
		return h.playJob(ctx, a, j, false)
	})
	return name, j, err
}

// playQuery plays the search fallback with the old defaults: area volume,
// no shuffle, repeat all.
func (h *Handler) playQuery(ctx context.Context, q player.Query, areaID string) (string, error) {
	return h.inArea(ctx, areaID, func(ctx context.Context, a rooms.Area) (string, error) {
		return h.playJob(ctx, a, job{query: &q}, false)
	})
}

// inArea takes the area lock, resolves the area and runs fn under the play
// timeout. fn must not take the lock again.
func (h *Handler) inArea(ctx context.Context, areaID string, fn func(context.Context, rooms.Area) (string, error)) (string, error) {
	unlock, err := h.lock(ctx, areaID)
	if err != nil {
		return "", err
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(ctx, playTimeout)
	defer cancel()
	area, err := h.deps.Rooms.Area(ctx, areaID)
	if err != nil {
		return "", err
	}
	return fn(ctx, area)
}

// lock takes the lock for key (an area id or "entity:<id>") and returns its
// unlock. It gives up when ctx is done, so a web request doesn't wait out a
// long play in the same room. The locks don't nest: never call it while
// holding one.
func (h *Handler) lock(ctx context.Context, key string) (unlock func(), err error) {
	h.mu.Lock()
	l, ok := h.areaLocks[key]
	if !ok {
		l = make(chan struct{}, 1)
		h.areaLocks[key] = l
	}
	h.mu.Unlock()
	select {
	case l <- struct{}{}:
		return func() { <-l }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for another play in %q: %w", key, ctx.Err())
	}
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
	attrs := []any{"source", s.Source, "tag_id", s.TagID, "tag_name", s.TagName, "device_id", s.DeviceID, "area_id", s.AreaID, "record_id", s.RecordID, "result", result}
	for _, kv := range [][2]string{{"entity", s.Entity}, {"media", s.Media}, {"player", playerName}} {
		if kv[1] != "" {
			attrs = append(attrs, kv[0], kv[1])
		}
	}
	if err != nil {
		s.Error = err.Error()
		attrs = append(attrs, "err", err)
	}
	switch {
	case result == model.ScanError:
		h.log.Error("scan", attrs...)
	case result == model.ScanIgnored && err == nil: // reader filter: routine noise
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
