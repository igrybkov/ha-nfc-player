package scan

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/ha/hatest"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/player/musicassistant"
	"github.com/igrybkov/ha-nfc-player/internal/player/sonos"
	"github.com/igrybkov/ha-nfc-player/internal/records"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

// newHandler is a Handler that is not running, for the synchronous
// Play / Stop / PlayRecord paths.
func newHandler(t testing.TB, f *hatest.Fake) (*Handler, *Ring) {
	s := &settings{model.DefaultSettings()}
	ma := musicassistant.New(f, s)
	ring := NewRing(LogSize)
	h := New(Deps{HA: f, Records: newRecords(t), Settings: s, Rooms: rooms.New(f, s),
		Router: player.NewRouter(s, ma, sonos.New(f, nil), ma), Log: ring, Logger: slog.New(slog.DiscardHandler)})
	return h, ring
}

// services returns "domain.service entity" for each call.
func services(calls []hatest.ServiceCall) []string {
	var out []string
	for _, c := range calls {
		ent := ""
		if c.Target != nil {
			ent = " " + strings.Join(c.Target.EntityID, ",")
		}
		out = append(out, c.Domain+"."+c.Service+ent)
	}
	return out
}

func searchResponds(f *hatest.Fake) {
	f.Respond = func(c hatest.ServiceCall) (json.RawMessage, error) {
		if c.Service == "search" {
			return json.RawMessage(`{"albums":[{"uri":"apple_music://album/1544753335"}],"playlists":[]}`), nil
		}
		return nil, nil
	}
}

func TestPlayEventSequence(t *testing.T) {
	hs := start(t, fakeHA())
	hs.f.EmitData(PlayEventType, map[string]any{
		"area": "office", "media": metallicaURL, "volume": "35", "shuffle": "on", "repeat": "one",
		"player": "", "record": "", "tag": "", "pool": "", "toggle": "", // template blanks are unset
	})
	s := hs.waitScans(t, 1)[0]
	want := model.Scan{Time: s.Time, TagName: metallicaURL, AreaID: "office", Player: "sonos",
		Result: model.ScanPlayed, Source: model.SourceEvent, Media: metallicaURL}
	if s != want {
		t.Fatalf("scan:\n got %+v\nwant %+v", s, want)
	}
	target := &ha.Target{EntityID: []string{"media_player.office"}}
	wantCalls := []hatest.ServiceCall{
		{Domain: "media_player", Service: "media_stop", Target: target},
		{Domain: "media_player", Service: "volume_set", Data: map[string]any{"volume_level": 0.35}, Target: target},
		{Domain: "media_player", Service: "repeat_set", Data: map[string]any{"repeat": "one"}, Target: target},
		{Domain: "media_player", Service: "shuffle_set", Data: map[string]any{"shuffle": true}, Target: target},
		{Domain: "media_player", Service: "play_media", Data: map[string]any{"media_content_id": metallicaURL, "media_content_type": "music"}, Target: target},
		{Domain: "media_player", Service: "repeat_set", Data: map[string]any{"repeat": "one"}, Target: target},
		{Domain: "media_player", Service: "shuffle_set", Data: map[string]any{"shuffle": true}, Target: target},
	}
	if got := hs.f.ServiceCalls(); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("calls:\n got %+v\nwant %+v", got, wantCalls)
	}
}

func TestPlayEventStale(t *testing.T) {
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	f := fakeHA()
	hs := startWith(t, f, f, func(h *Handler) { h.now = func() time.Time { return now } })
	raw, _ := json.Marshal(map[string]any{"area": "office", "media": metallicaURL})
	f.Emit(ha.Event{EventType: PlayEventType, Data: raw, TimeFired: now.Add(-MaxScanAge - time.Second)})
	raw, _ = json.Marshal(map[string]any{"area": "office"})
	f.Emit(ha.Event{EventType: StopEventType, Data: raw, TimeFired: now.Add(-time.Hour)})
	for _, s := range hs.waitScans(t, 2) {
		if s.Result != model.ScanIgnored || s.Source != model.SourceEvent || !strings.Contains(s.Error, "old") {
			t.Errorf("scan = %+v", s)
		}
	}
	if calls := f.ServiceCalls(); len(calls) != 0 {
		t.Errorf("stale events made calls: %v", services(calls))
	}
}

// Toggle reads live state: playing → stop only; idle → play.
func TestToggle(t *testing.T) {
	f := fakeHA()
	f.StateList = []ha.State{
		{EntityID: "media_player.arc", State: "idle"},
		{EntityID: "media_player.family_room_ma", State: "playing"},
	}
	hs := start(t, f)
	hs.f.EmitData(PlayEventType, map[string]any{"area": "family_room", "pool": "kitchen", "toggle": true})
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanStopped || s.Player != "music_assistant" || s.TagName != "pool kitchen (toggle)" || s.RecordID != "" {
		t.Fatalf("scan = %+v", s)
	}
	want := []string{"media_player.media_stop media_player.family_room_ma"}
	if got := services(f.ServiceCalls()); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}

	// The same toggle with nothing playing plays.
	h, _ := newHandler(t, &hatest.Fake{AreaList: f.AreaList, DeviceList: f.DeviceList, EntityList: f.EntityList})
	res, err := h.Play(context.Background(), model.PlayCommand{Target: model.Target{AreaID: "family_room"}, Media: queenURL, Toggle: true})
	if err != nil || res.Stopped || res.Player != "sonos" {
		t.Errorf("toggle with nothing playing = %+v, %v", res, err)
	}
}

func TestStop(t *testing.T) {
	ctx := context.Background()
	t.Run("every playing entity", func(t *testing.T) {
		f := fakeHA()
		f.StateList = []ha.State{
			{EntityID: "media_player.arc", State: "playing"},
			{EntityID: "media_player.family_room_ma", State: "playing"},
		}
		h, ring := newHandler(t, f)
		name, err := h.Stop(ctx, model.Target{AreaID: "family_room"})
		if err != nil || name != "sonos" {
			t.Fatalf("Stop = %q, %v", name, err)
		}
		want := []string{"media_player.media_stop media_player.arc", "media_player.media_stop media_player.family_room_ma"}
		if got := services(f.Calls); !reflect.DeepEqual(got, want) {
			t.Errorf("calls = %v", got)
		}
		if s := ring.Recent()[0]; s.Result != model.ScanStopped || s.Source != model.SourceUI || s.TagName != "stop" || s.AreaID != "family_room" {
			t.Errorf("scan = %+v", s)
		}
	})
	t.Run("nothing playing stops Sonos", func(t *testing.T) {
		f := fakeHA()
		h, _ := newHandler(t, f)
		if name, err := h.Stop(ctx, model.Target{AreaID: "family_room"}); err != nil || name != "sonos" {
			t.Fatalf("Stop = %q, %v", name, err)
		}
		if got := services(f.Calls); !reflect.DeepEqual(got, []string{"media_player.media_stop media_player.arc"}) {
			t.Errorf("calls = %v", got)
		}
	})
	t.Run("event with a player", func(t *testing.T) {
		hs := start(t, fakeHA())
		hs.f.EmitData(StopEventType, map[string]any{"player": "media_player.portable_ma"})
		s := hs.waitScans(t, 1)[0]
		if s.Result != model.ScanStopped || s.Entity != "media_player.portable_ma" || s.Player != "music_assistant" || s.Source != model.SourceEvent {
			t.Fatalf("scan = %+v", s)
		}
	})
	t.Run("no player in area", func(t *testing.T) {
		f := fakeHA()
		f.AreaList = append(f.AreaList, ha.Area{AreaID: "attic", Name: "Attic"})
		h, _ := newHandler(t, f)
		if _, err := h.Stop(ctx, model.Target{AreaID: "attic"}); err == nil || len(f.Calls) != 0 {
			t.Errorf("err = %v, calls = %v", err, f.Calls)
		}
	})
}

func TestPlayTargets(t *testing.T) {
	ctx := context.Background()
	f := fakeHA()
	h, ring := newHandler(t, f)

	// A player entity plays on that entity only, at its area's volume.
	res, err := h.Play(ctx, model.PlayCommand{Target: model.Target{Entity: "media_player.family_room_ma"}, Media: disturbedURL})
	if err != nil || res.Player != "music_assistant" || res.Media != disturbedURL {
		t.Fatalf("Play = %+v, %v", res, err)
	}
	for _, c := range f.Calls {
		if c.Target.EntityID[0] != "media_player.family_room_ma" {
			t.Errorf("call on %v", c.Target.EntityID)
		}
	}
	if v := volumeOf(t, f.Calls); v != 0.30 {
		t.Errorf("volume = %v, want family room 0.30", v)
	}
	if s := ring.Recent()[0]; s.AreaID != "family_room" || s.Entity != "media_player.family_room_ma" || s.Source != model.SourceUI {
		t.Errorf("scan = %+v", s)
	}

	errs := []struct {
		name string
		cmd  model.PlayCommand
		want error
	}{
		{"wrong platform", model.PlayCommand{Target: model.Target{Entity: "media_player.tv"}, Media: disturbedURL}, rooms.ErrUnsupportedEntity},
		{"unknown entity", model.PlayCommand{Target: model.Target{Entity: "media_player.nope"}, Media: disturbedURL}, rooms.ErrUnknownEntity},
		{"unknown area", model.PlayCommand{Target: model.Target{AreaID: "attic"}, Media: disturbedURL}, rooms.ErrUnknownArea},
		{"no target", model.PlayCommand{Media: disturbedURL}, ErrInvalid},
		{"both targets", model.PlayCommand{Target: model.Target{AreaID: "office", Entity: "media_player.office"}, Media: disturbedURL}, ErrInvalid},
		{"no media", model.PlayCommand{Target: model.Target{AreaID: "office"}, Media: "  "}, ErrInvalid},
		{"volume", model.PlayCommand{Target: model.Target{AreaID: "office"}, Media: disturbedURL, Volume: ptr(101)}, ErrInvalid},
		{"repeat", model.PlayCommand{Target: model.Target{AreaID: "office"}, Media: disturbedURL, Repeat: ptr(model.Repeat("x"))}, ErrInvalid},
		{"bad URL", model.PlayCommand{Target: model.Target{AreaID: "office"}, Media: "https://open.spotify.com/album/1"}, ErrInvalid},
		{"unknown record", model.PlayCommand{Target: model.Target{AreaID: "office"}, Record: "nope"}, records.ErrNotFound},
		{"unknown tag", model.PlayCommand{Target: model.Target{AreaID: "office"}, Tag: "nope"}, records.ErrNotFound},
		{"unknown pool", model.PlayCommand{Target: model.Target{AreaID: "office"}, Pool: "nope"}, records.ErrNotFound},
		{"deleted record item", model.PlayCommand{Target: model.Target{AreaID: "office"}, Record: "missing"}, records.ErrNothingToPlay},
		{"empty record", model.PlayCommand{Target: model.Target{AreaID: "office"}, Pool: "empty"}, records.ErrNothingToPlay},
		{"bad URL in a record", model.PlayCommand{Target: model.Target{AreaID: "office"}, Record: "bad-url"}, ErrInvalid},
	}
	for _, tt := range errs {
		f.Reset()
		if _, err := h.Play(ctx, tt.cmd); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
		if len(f.ServiceCalls()) != 0 {
			t.Errorf("%s: service calls %v", tt.name, services(f.ServiceCalls()))
		}
		if s := ring.Recent()[0]; s.Result != model.ScanError && s.Result != model.ScanNoMatch {
			t.Errorf("%s: scan = %+v", tt.name, s)
		}
	}
}

// A player entity without an area locks on "entity:<id>" and uses the
// default volume.
func TestPlayEventEntityWithoutArea(t *testing.T) {
	hs := start(t, fakeHA())
	hs.f.EmitData(PlayEventType, map[string]any{"player": "media_player.portable_ma", "media": queenURL, "radio": 1})
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanPlayed || s.Player != "music_assistant" || s.AreaID != "" {
		t.Fatalf("scan = %+v", s)
	}
	calls := hs.f.ServiceCalls()
	if v := volumeOf(t, calls); v != 0.15 {
		t.Errorf("volume = %v, want default 0.15", v)
	}
	if calls[1].Data["radio_mode"] != true {
		t.Errorf("play_media = %v, want radio_mode", calls[1].Data)
	}
}

// Radio in an area with both players skips Sonos.
func TestPlayRadioSkipsSonos(t *testing.T) {
	f := fakeHA()
	h, _ := newHandler(t, f)
	res, err := h.Play(context.Background(), model.PlayCommand{Target: model.Target{AreaID: "family_room"}, Media: queenURL, Radio: true})
	if err != nil || res.Player != "music_assistant" {
		t.Fatalf("Play = %+v, %v", res, err)
	}
	// Radio in a room with only Sonos has no player.
	if _, err := h.Play(context.Background(), model.PlayCommand{Target: model.Target{AreaID: "office"}, Media: queenURL, Radio: true}); !errors.Is(err, player.ErrNoPlayer) {
		t.Errorf("radio on Sonos only: err = %v", err)
	}
}

func TestPlayMediaText(t *testing.T) {
	tests := []struct {
		media  string
		search map[string]any // search data; nil = no search
	}{
		{"apple_music://album/1646032515", nil},
		{"Ray Charles — The Ultimate Collection", map[string]any{"artist": "Ray Charles", "name": "The Ultimate Collection"}},
		{"Volodymyr Ivasiuk", map[string]any{"name": "Volodymyr Ivasiuk"}},
	}
	for _, tt := range tests {
		f := fakeHA()
		searchResponds(f)
		h, _ := newHandler(t, f)
		res, err := h.Play(context.Background(), model.PlayCommand{Target: model.Target{AreaID: "family_room"}, Media: tt.media})
		if err != nil || res.Player != "sonos" || res.Media != tt.media {
			t.Fatalf("%s: Play = %+v, %v", tt.media, res, err)
		}
		if tt.search == nil {
			if f.Calls[0].Service == "search" {
				t.Errorf("%s: searched a URL", tt.media)
			}
			continue
		}
		got := f.Calls[0].Data
		_, hasArtist := got["artist"]
		_, wantArtist := tt.search["artist"]
		if f.Calls[0].Service != "search" || got["name"] != tt.search["name"] || hasArtist != wantArtist || got["artist"] != tt.search["artist"] {
			t.Errorf("%s: search data = %v, want %v", tt.media, got, tt.search)
		}
	}
}

// Settings precedence: command, then the record asked for (whatever item
// the pick went down to), then defaults. The record's shuffle depends on
// the kind of media picked; a command's shuffle applies to every kind.
func TestPlaySettingsPrecedence(t *testing.T) {
	type want struct {
		volume  float64
		shuffle bool
		repeat  string
	}
	tests := []struct {
		name string
		cmd  model.PlayCommand
		want want
	}{
		{"album in order by default", model.PlayCommand{Record: "albums"}, want{0.40, false, "all"}},
		// metallica-essential says shuffle on, repeat all: nest wins.
		{"scanned record over inner record", model.PlayCommand{Record: "nest"}, want{0.55, false, "off"}},
		{"command over record", model.PlayCommand{Record: "nest", Volume: ptr(10), Shuffle: ptr(true), Repeat: ptr(model.RepeatOne)}, want{0.10, true, "one"}},
		{"tag alias", model.PlayCommand{Tag: "nest-tag"}, want{0.55, false, "off"}},
		{"tag by name", model.PlayCommand{Tag: "Disturbed — Divisible | x"}, want{0.50, false, "all"}},
		{"record by name", model.PlayCommand{Record: "metallica — essential"}, want{0.15, true, "all"}},
		{"defaults", model.PlayCommand{Media: queenURL}, want{0.15, false, "all"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fakeHA()
			h, _ := newHandler(t, f)
			tt.cmd.AreaID = "office"
			if _, err := h.Play(context.Background(), tt.cmd); err != nil {
				t.Fatal(err)
			}
			got := want{volume: volumeOf(t, f.Calls)}
			for _, c := range f.Calls {
				switch c.Service {
				case "shuffle_set":
					got.shuffle = c.Data["shuffle"].(bool)
				case "repeat_set":
					got.repeat = c.Data["repeat"].(string)
				}
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A record of several items: by command, by v1 pool alias and by scan.
// Each play picks one item and reports it as Media.
func TestPlayRecordPicks(t *testing.T) {
	ctx := context.Background()
	items := []string{metallicaURL, queenURL, "Volodymyr Ivasiuk"}
	f := fakeHA()
	searchResponds(f)
	h, ring := newHandler(t, f)

	for _, cmd := range []model.PlayCommand{{Record: "kitchen"}, {Pool: "kitchen"}, {Tag: "kitchen-tag"}} {
		cmd.AreaID = "office"
		res, err := h.Play(ctx, cmd)
		if err != nil || res.Player != "sonos" || res.RecordID != "kitchen" || !slices.Contains(items, res.Media) {
			t.Fatalf("Play(%+v) = %+v, %v", cmd, res, err)
		}
		if s := ring.Recent()[0]; s.RecordID != "kitchen" || s.Media != res.Media || s.Result != model.ScanPlayed {
			t.Errorf("scan = %+v", s)
		}
	}
	if s := ring.Recent()[0]; s.TagName != "tag kitchen-tag" {
		t.Errorf("scan = %+v", s)
	}

	hs := start(t, fakeHA())
	searchResponds(hs.f)
	hs.scan("tag-kitchen", "Kitchen", "reader-office")
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanPlayed || s.RecordID != "kitchen" || !slices.Contains(items, s.Media) || s.Source != model.SourceTag {
		t.Errorf("scan = %+v", s)
	}
}

// A record that leads to no media fails before any call.
func TestPlayRecordNothingToPlay(t *testing.T) {
	f := fakeHA()
	h, ring := newHandler(t, f)
	for _, id := range []string{"empty", "missing"} {
		if _, err := h.PlayRecord(context.Background(), id, "office"); !errors.Is(err, records.ErrNothingToPlay) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	hs := start(t, fakeHA())
	hs.scan("", "Empty", "reader-office")
	if s := hs.waitScans(t, 1)[0]; s.Result != model.ScanError || s.RecordID != "empty" || !strings.Contains(s.Error, "nothing to play") {
		t.Errorf("scan = %+v", s)
	}
	if len(f.ServiceCalls())+len(hs.f.ServiceCalls()) != 0 || len(ring.Recent()) != 0 {
		t.Errorf("calls %v, scans %v", services(f.ServiceCalls()), ring.Recent())
	}
}

func TestDecodeEventData(t *testing.T) {
	decode := func(t *testing.T, data string) (model.PlayCommand, error) {
		t.Helper()
		d, err := decodeEventData(json.RawMessage(data))
		if err != nil {
			return model.PlayCommand{}, err
		}
		return d.playCommand()
	}
	t.Run("lenient", func(t *testing.T) {
		cmd, err := decode(t, `{"area":" office ","volume":"30","shuffle":"true","repeat":"ALL","radio":"yes","toggle":1,"record":" kitchen ","tag":"","pool":null,"media":"x"}`)
		if err != nil {
			t.Fatal(err)
		}
		want := model.PlayCommand{Target: model.Target{AreaID: "office"}, Media: "x", Record: "kitchen", Volume: ptr(30), Shuffle: ptr(true),
			Repeat: ptr(model.RepeatAll), Radio: true, Toggle: true, Source: model.SourceEvent}
		if !reflect.DeepEqual(cmd, want) {
			t.Errorf("cmd:\n got %+v\nwant %+v", cmd, want)
		}
	})
	t.Run("numbers and blanks", func(t *testing.T) {
		cmd, err := decode(t, `{"volume":30.0,"shuffle":"","repeat":"","radio":false,"toggle":"off","tag":12345}`)
		if err != nil {
			t.Fatal(err)
		}
		if *cmd.Volume != 30 || cmd.Shuffle != nil || cmd.Repeat != nil || cmd.Radio || cmd.Toggle || cmd.Tag != "12345" {
			t.Errorf("cmd = %+v", cmd)
		}
	})
	t.Run("empty", func(t *testing.T) {
		for _, data := range []string{``, `null`, `{}`} {
			if cmd, err := decode(t, data); err != nil || cmd.Volume != nil || cmd.Media != "" {
				t.Errorf("%q: %+v, %v", data, cmd, err)
			}
		}
	})
	for _, data := range []string{
		`{"volume":"loud"}`, `{"volume":30.5}`, `{"volume":true}`, `{"shuffle":"maybe"}`,
		`{"shuffle":2}`, `{"radio":[]}`, `{"media":{"a":1}}`, `[1]`, `{"area":true}`,
	} {
		if _, err := decode(t, data); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", data, err)
		}
	}
}

// Bad event data is recorded as an error, never a panic or a call.
func TestPlayEventBadData(t *testing.T) {
	hs := start(t, fakeHA())
	hs.f.EmitData(PlayEventType, map[string]any{"area": "office", "media": queenURL, "volume": "loud"})
	hs.f.EmitData(PlayEventType, map[string]any{"media": queenURL})
	hs.f.Emit(ha.Event{EventType: StopEventType, Data: json.RawMessage(`"x"`)})
	hs.f.EmitData(PlayEventType, map[string]any{"player": "media_player.tv", "media": queenURL})
	for _, s := range hs.waitScans(t, 4) {
		if s.Result != model.ScanError || s.Source != model.SourceEvent || s.Error == "" {
			t.Errorf("scan = %+v", s)
		}
	}
	if calls := hs.f.ServiceCalls(); len(calls) != 0 {
		t.Errorf("calls: %v", services(calls))
	}
}

// Play events share the area worker with tag scans: a play event waits for
// the scan before it.
func TestPlayEventSharesAreaWorker(t *testing.T) {
	f := fakeHA()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	f.Respond = func(c hatest.ServiceCall) (json.RawMessage, error) {
		if c.Service == "play_media" && c.Data["media_content_id"] == metallicaURL {
			<-release
		}
		return nil, nil
	}
	hs := start(t, f)
	t.Cleanup(unblock)
	hs.scan("", "Metallica — Essential", "reader-fr")
	waitFor(t, "blocked play", func() bool { return len(playMedia(f.ServiceCalls())) == 1 })
	hs.f.EmitData(PlayEventType, map[string]any{"player": "media_player.arc", "media": queenURL})
	time.Sleep(20 * time.Millisecond)
	if n := len(playMedia(f.ServiceCalls())); n != 1 {
		t.Fatalf("event played during the scan: %d play_media calls", n)
	}
	unblock()
	scans := hs.waitScans(t, 2)
	if scans[0].Source != model.SourceTag || scans[1].Source != model.SourceEvent || scans[1].Result != model.ScanPlayed {
		t.Errorf("scans = %+v", scans)
	}
}

// failingHA refuses one event subscription.
type failingHA struct {
	*hatest.Fake
	refuse string
}

func (f failingHA) SubscribeEvents(ctx context.Context, typ string, fn func(ha.Event)) error {
	if typ == f.refuse {
		return errors.New("refused")
	}
	return f.Fake.SubscribeEvents(ctx, typ, fn)
}

// A failed subscribe drops the ones made before it, so a retry doesn't
// double them.
func TestRunSubscribeFailure(t *testing.T) {
	f := fakeHA()
	h, _ := newHandler(t, f)
	h.deps.HA = failingHA{f, StopEventType}
	if err := h.Run(context.Background()); err == nil {
		t.Fatal("Run: want error")
	}
	if n := f.Subscribers(EventType) + f.Subscribers(PlayEventType); n != 0 {
		t.Errorf("%d subscriptions left after a failed Run", n)
	}
}

// A play that can't get the room gives up when its ctx ends, without
// calling any service, so a web request doesn't wait out a long play.
func TestPlayLockWaitHonorsContext(t *testing.T) {
	f := fakeHA()
	h, _ := newHandler(t, f)
	unlock, err := h.lock(context.Background(), "office")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = h.Play(ctx, model.PlayCommand{Target: model.Target{AreaID: "office"}, Media: queenURL})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if calls := f.ServiceCalls(); len(calls) != 0 {
		t.Errorf("calls: %v", services(calls))
	}
}

// An unknown area fails before a worker is started for it.
func TestPlayEventUnknownArea(t *testing.T) {
	hs := start(t, fakeHA())
	hs.f.EmitData(PlayEventType, map[string]any{"area": "offfice", "media": queenURL})
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanError || !strings.Contains(s.Error, "unknown area") {
		t.Errorf("scan = %+v", s)
	}
	if calls := hs.f.ServiceCalls(); len(calls) != 0 {
		t.Errorf("calls: %v", services(calls))
	}
}
