package scan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/pools"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// Play and stop commands come from nfc_player_play / nfc_player_stop events
// and from the web UI (docs/PLAN.md section 13). They share the per-area
// lock with tag scans: the lock key is the area id, or "entity:<id>" for a
// player entity without an area.

var (
	// ErrInvalid means a play or stop command is malformed: no target or
	// both, no media, or a value out of range.
	ErrInvalid = errors.New("invalid command")
	// ErrNestedPool means a pool item is a tag that plays a pool.
	ErrNestedPool = errors.New("nested pool")
	// ErrEmptyPool means a pool has no items: "all" with no URL entries, or
	// a hand-edited pools.yaml.
	ErrEmptyPool = errors.New("pool has no items")
)

// statePlaying is the media_player state that toggle and stop look for.
const statePlaying = "playing"

// Play runs a play command synchronously under the target's lock and
// records it. An empty cmd.Source means SourceUI.
func (h *Handler) Play(ctx context.Context, cmd model.PlayCommand) (model.PlayResult, error) {
	if cmd.Source == "" {
		cmd.Source = model.SourceUI
	}
	cmd = trimCommand(cmd)
	return h.runPlay(ctx, cmd, commandScan(h.now(), cmd))
}

// Stop stops playback on a target synchronously under its lock and records
// it, as SourceUI. It returns the name of the player it stopped.
func (h *Handler) Stop(ctx context.Context, t model.Target) (string, error) {
	t = trimTarget(t)
	return h.runStop(ctx, t, stopScan(h.now(), model.SourceUI, t))
}

func (h *Handler) runPlay(ctx context.Context, cmd model.PlayCommand, s model.Scan) (model.PlayResult, error) {
	res, err := h.execPlay(ctx, cmd, &s)
	s.EntryID, s.PoolID, s.Media = res.EntryID, res.PoolID, res.Media
	switch {
	case err != nil:
		h.record(s, errResult(err), res.Player, err)
	case res.Stopped:
		h.record(s, model.ScanStopped, res.Player, nil)
	default:
		h.record(s, model.ScanPlayed, res.Player, nil)
	}
	return res, err
}

// execPlay resolves the target, takes its lock, handles a toggle, then
// resolves the media and plays it. The pool pick happens after the toggle
// check, so a toggle that stops doesn't use up a pick.
func (h *Handler) execPlay(ctx context.Context, cmd model.PlayCommand, s *model.Scan) (model.PlayResult, error) {
	if err := checkCommand(cmd); err != nil {
		return model.PlayResult{}, err
	}
	area, key, err := h.target(ctx, cmd.Target)
	if err != nil {
		return model.PlayResult{}, err
	}
	s.AreaID = area.ID
	unlock, err := h.lock(ctx, key)
	if err != nil {
		return model.PlayResult{}, err
	}
	defer unlock()
	// The play timeout starts once the room is ours, as in inArea.
	ctx, cancel := context.WithTimeout(ctx, playTimeout)
	defer cancel()

	if cmd.Toggle {
		playing, err := h.playing(ctx, area)
		if err != nil {
			return model.PlayResult{}, err
		}
		if len(playing) > 0 {
			name, err := h.stopEntities(ctx, area, playing)
			return model.PlayResult{Player: name, Stopped: err == nil}, err
		}
	}

	j, err := h.commandJob(cmd)
	res := model.PlayResult{EntryID: j.entryID, PoolID: j.poolID, Media: j.text}
	if err != nil {
		return res, err
	}
	res.Player, err = h.playJob(ctx, area, j, cmd.Radio)
	return res, err
}

func (h *Handler) runStop(ctx context.Context, t model.Target, s model.Scan) (string, error) {
	name, err := h.execStop(ctx, t, &s)
	if err != nil {
		h.record(s, errResult(err), name, err)
		return name, err
	}
	h.record(s, model.ScanStopped, name, nil)
	return name, nil
}

// execStop stops every target entity that is playing. With nothing
// playing it still stops the Sonos entity, else the MA entity, so a stop
// never depends on a stale state.
func (h *Handler) execStop(ctx context.Context, t model.Target, s *model.Scan) (string, error) {
	if err := checkTarget(t); err != nil {
		return "", err
	}
	area, key, err := h.target(ctx, t)
	if err != nil {
		return "", err
	}
	s.AreaID = area.ID
	unlock, err := h.lock(ctx, key)
	if err != nil {
		return "", err
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, playTimeout)
	defer cancel()

	entities, err := h.playing(ctx, area)
	if err != nil {
		return "", err
	}
	if len(entities) == 0 {
		if all := areaEntities(area); len(all) > 0 {
			entities = all[:1]
		}
	}
	if len(entities) == 0 {
		return "", fmt.Errorf("area %q has no Sonos or Music Assistant player", area.ID)
	}
	return h.stopEntities(ctx, area, entities)
}

// target resolves a command target to an Area and its lock key. The
// lookups get their own short timeout: the caller's ctx may have none.
func (h *Handler) target(ctx context.Context, t model.Target) (rooms.Area, string, error) {
	ctx, cancel := context.WithTimeout(ctx, h.areaTimeout)
	defer cancel()
	if t.Entity != "" {
		a, err := h.deps.Rooms.Entity(ctx, t.Entity)
		if err != nil {
			return rooms.Area{}, "", err
		}
		return a, entityKey(a.ID, t.Entity), nil
	}
	a, err := h.deps.Rooms.Area(ctx, t.AreaID)
	return a, t.AreaID, err
}

// lockKey is the key target returns. The event dispatcher uses it to pick
// the worker. An unknown area fails here, so a mistyped area in a template
// can't start a worker that lives until shutdown.
func (h *Handler) lockKey(ctx context.Context, t model.Target) (string, error) {
	if t.Entity == "" {
		if _, err := h.deps.Rooms.Area(ctx, t.AreaID); err != nil {
			return "", err
		}
		return t.AreaID, nil
	}
	a, err := h.deps.Rooms.Entity(ctx, t.Entity)
	if err != nil {
		return "", err
	}
	return entityKey(a.ID, t.Entity), nil
}

func entityKey(areaID, entity string) string {
	if areaID != "" {
		return areaID
	}
	return "entity:" + entity
}

// areaEntities is the Sonos entity, then the MA entity, whichever are set.
func areaEntities(a rooms.Area) []string {
	var out []string
	for _, e := range []string{a.SonosEntity, a.MAEntity} {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// playing returns the area's entities in state "playing", from live state.
func (h *Handler) playing(ctx context.Context, a rooms.Area) ([]string, error) {
	entities := areaEntities(a)
	if len(entities) == 0 {
		return nil, nil
	}
	states, err := h.deps.HA.States(ctx)
	if err != nil {
		return nil, fmt.Errorf("states: %w", err)
	}
	var out []string
	for _, e := range entities {
		i := slices.IndexFunc(states, func(s ha.State) bool { return s.EntityID == e })
		if i >= 0 && states[i].State == statePlaying {
			out = append(out, e)
		}
	}
	return out, nil
}

// stopEntities calls media_stop on each entity and returns the player name
// of the first one.
func (h *Handler) stopEntities(ctx context.Context, a rooms.Area, entities []string) (string, error) {
	name := playerName(a, entities[0])
	for _, e := range entities {
		if _, err := h.deps.HA.CallService(ctx, "media_player", "media_stop", nil, &ha.Target{EntityID: []string{e}}, false); err != nil {
			return name, fmt.Errorf("stop %s: %w", e, err)
		}
	}
	return name, nil
}

// playerName is the player that owns an area entity.
func playerName(a rooms.Area, entity string) string {
	if entity == a.SonosEntity {
		return "sonos"
	}
	return "music_assistant"
}

// errResult is the scan result of a failed command or play: a lookup that
// found nothing is no_match, anything else an error.
func errResult(err error) model.ScanResult {
	if errors.Is(err, player.ErrNotFound) || errors.Is(err, registry.ErrNotFound) || errors.Is(err, pools.ErrNotFound) {
		return model.ScanNoMatch
	}
	return model.ScanError
}

func trimTarget(t model.Target) model.Target {
	return model.Target{AreaID: strings.TrimSpace(t.AreaID), Entity: strings.TrimSpace(t.Entity)}
}

func trimCommand(c model.PlayCommand) model.PlayCommand {
	c.Target = trimTarget(c.Target)
	c.Media, c.Tag, c.Pool = strings.TrimSpace(c.Media), strings.TrimSpace(c.Tag), strings.TrimSpace(c.Pool)
	return c
}

func checkTarget(t model.Target) error {
	switch {
	case t.AreaID == "" && t.Entity == "":
		return fmt.Errorf("%w: area or player is required", ErrInvalid)
	case t.AreaID != "" && t.Entity != "":
		return fmt.Errorf("%w: set area or player, not both", ErrInvalid)
	}
	return nil
}

func checkCommand(c model.PlayCommand) error {
	if err := checkTarget(c.Target); err != nil {
		return err
	}
	if c.Media == "" && c.Tag == "" && c.Pool == "" {
		return fmt.Errorf("%w: media, tag or pool is required", ErrInvalid)
	}
	if c.Volume != nil && (*c.Volume < 0 || *c.Volume > 100) {
		return fmt.Errorf("%w: volume must be 0..100, not %d", ErrInvalid, *c.Volume)
	}
	if c.Repeat != nil && !c.Repeat.Valid() {
		return fmt.Errorf("%w: repeat must be off, one or all, not %q", ErrInvalid, *c.Repeat)
	}
	return nil
}

// commandScan is the ScanLog record of a play command. TagName is a short
// description of the source for the UI.
func commandScan(t time.Time, c model.PlayCommand) model.Scan {
	desc := c.Media
	switch {
	case c.Media != "":
	case c.Tag != "":
		desc = "tag " + c.Tag
	case c.Pool != "":
		desc = "pool " + c.Pool
	}
	if c.Toggle {
		desc += " (toggle)"
	}
	return model.Scan{Time: t, Source: c.Source, TagName: desc, AreaID: c.AreaID, Entity: c.Entity}
}

func stopScan(t time.Time, source string, tg model.Target) model.Scan {
	return model.Scan{Time: t, Source: source, TagName: "stop", AreaID: tg.AreaID, Entity: tg.Entity}
}

// job is a resolved play source: what to play, and the settings layers,
// highest precedence first.
type job struct {
	media   catalog.Media
	query   *player.Query
	layers  []layer
	entryID string // entry played, if any
	poolID  string // pool picked from, if any
	text    string // media text played when not an entry
}

// layer is one level of playback settings. Nil = defer to the next layer.
type layer struct {
	shuffle *bool
	repeat  *model.Repeat
	volume  *int
	player  *string
}

func commandLayer(c model.PlayCommand) layer {
	return layer{shuffle: c.Shuffle, repeat: c.Repeat, volume: c.Volume}
}

func poolLayer(p model.Pool) layer {
	return layer{shuffle: p.Shuffle, repeat: p.Repeat, volume: p.Volume, player: p.Player}
}

// entryLayer is a URL entry's settings. Shuffle is a plain bool, so it
// always decides; an empty repeat is "all".
func entryLayer(e model.Entry) layer {
	shuffle, repeat := e.Shuffle, e.Repeat
	if repeat == "" {
		repeat = model.RepeatAll
	}
	return layer{shuffle: &shuffle, repeat: &repeat, volume: e.Volume, player: e.Player}
}

// poolEntryLayer is a pool entry's settings: only Volume and Player apply.
func poolEntryLayer(e model.Entry) layer {
	return layer{volume: e.Volume, player: e.Player}
}

// commandJob resolves the first non-empty of Media, Tag and Pool.
func (h *Handler) commandJob(c model.PlayCommand) (job, error) {
	var (
		j   job
		err error
	)
	switch {
	case c.Media != "":
		j, err = mediaJob(c.Media)
	case c.Tag != "":
		var e model.Entry
		if e, err = h.entry(c.Tag); err == nil {
			j, err = h.entryJob(e)
		}
	default:
		j, err = h.poolJob(c.Pool, nil)
	}
	j.layers = append([]layer{commandLayer(c)}, j.layers...)
	return j, err
}

// entry finds a tag entry by id, else by name.
func (h *Handler) entry(tag string) (model.Entry, error) {
	if e, ok := h.entryByID(tag); ok {
		return e, nil
	}
	if e, ok := h.deps.Entries.ByName(model.NormalizeName(tag)); ok {
		return e, nil
	}
	return model.Entry{}, fmt.Errorf("tag %q: %w", tag, registry.ErrNotFound)
}

func (h *Handler) entryByID(id string) (model.Entry, bool) {
	for _, e := range h.deps.Entries.All() {
		if e.ID == id {
			return e, true
		}
	}
	return model.Entry{}, false
}

// entryJob plays a URL entry, or picks from a pool entry's pool.
func (h *Handler) entryJob(e model.Entry) (job, error) {
	if e.Pool != "" {
		return h.poolJob(e.Pool, &e)
	}
	m, err := catalog.Parse(e.URL)
	if err != nil {
		return job{entryID: e.ID}, fmt.Errorf("tag %q: %w", e.ID, err)
	}
	return job{media: m, layers: []layer{entryLayer(e)}, entryID: e.ID}, nil
}

// poolJob picks one item of a pool. from is the pool entry that named the
// pool, if any: its Volume and Player beat the pool's.
func (h *Handler) poolJob(poolID string, from *model.Entry) (job, error) {
	j := job{poolID: poolID}
	if h.deps.Pools == nil {
		return j, fmt.Errorf("%w: %s", pools.ErrNotFound, poolID)
	}
	p, ok := h.deps.Pools.Pool(poolID)
	if !ok {
		return j, fmt.Errorf("%w: %s", pools.ErrNotFound, poolID)
	}
	if len(p.Items) == 0 {
		return j, fmt.Errorf("pool %q: %w", poolID, ErrEmptyPool)
	}
	if from != nil {
		j.layers = append(j.layers, poolEntryLayer(*from))
	}
	j.layers = append(j.layers, poolLayer(p))

	it := h.pick(p)
	if it.Tag == "" {
		mj, err := mediaJob(it.Media)
		mj.poolID, mj.layers = poolID, j.layers
		return mj, err
	}
	e, ok := h.entryByID(it.Tag)
	switch {
	case !ok:
		return j, fmt.Errorf("pool %q: tag %q: %w", poolID, it.Tag, registry.ErrNotFound)
	case e.Pool != "":
		return j, fmt.Errorf("pool %q: tag %q plays pool %q: %w", poolID, it.Tag, e.Pool, ErrNestedPool)
	}
	m, err := catalog.Parse(e.URL)
	if err != nil {
		return j, fmt.Errorf("pool %q: tag %q: %w", poolID, it.Tag, err)
	}
	j.media, j.entryID = m, e.ID
	j.layers = append(j.layers, entryLayer(e))
	return j, nil
}

// mediaJob resolves media text: a URL, else "Artist — Album", else a name
// to search. Text that looks like a URL must parse.
func mediaJob(text string) (job, error) {
	j := job{text: text}
	m, err := catalog.Parse(text)
	switch {
	case err == nil:
		j.media = m
	case pools.IsURL(text):
		return j, fmt.Errorf("%w: media %q: %v", ErrInvalid, text, err)
	default:
		q, ok := ParseQuery(text)
		if !ok {
			q = player.Query{Album: text}
		}
		j.query = &q
	}
	return j, nil
}

// pick returns a uniformly random item of p, never the item picked last
// time for p when there is another to pick.
func (h *Handler) pick(p model.Pool) model.PoolItem {
	h.pickMu.Lock()
	defer h.pickMu.Unlock()
	items := p.Items
	if last, ok := h.lastPick[p.ID]; ok {
		others := slices.DeleteFunc(slices.Clone(items), func(it model.PoolItem) bool { return it == last })
		if len(others) > 0 {
			items = others
		}
	}
	it := items[h.intn(len(items))]
	h.lastPick[p.ID] = it
	return it
}

// playJob merges the job's layers and plays it in a resolved area. The
// caller holds the area lock.
func (h *Handler) playJob(ctx context.Context, a rooms.Area, j job, radio bool) (string, error) {
	var st layer
	for _, l := range j.layers {
		st.shuffle = firstSet(st.shuffle, l.shuffle)
		st.repeat = firstSet(st.repeat, l.repeat)
		st.volume = firstSet(st.volume, l.volume)
		st.player = firstSet(st.player, l.player)
	}
	req := player.Request{Media: j.media, Query: j.query, Area: a, Volume: a.Volume, Repeat: string(model.RepeatAll), Radio: radio}
	if st.shuffle != nil {
		req.Shuffle = *st.shuffle
	}
	if st.repeat != nil {
		req.Repeat = string(*st.repeat)
	}
	if st.volume != nil {
		req.Volume = *st.volume
	}
	req.Volume = min(max(req.Volume, 0), 100)
	var override string
	if st.player != nil {
		override = *st.player
	}
	return h.deps.Router.Play(ctx, req, override)
}

// firstSet returns a if set, else b.
func firstSet[T any](a, b *T) *T {
	if a != nil {
		return a
	}
	return b
}
