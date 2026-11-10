package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

type settings struct{ s model.Settings }

func (s *settings) Settings() model.Settings { return s.s }

// entries is a minimal model.EntryLookup.
type entries []model.Entry

func (es entries) ByTagID(id string) (model.Entry, bool) {
	for _, e := range es {
		for _, t := range e.TagIDs {
			if t == id {
				return e, true
			}
		}
	}
	return model.Entry{}, false
}

func (es entries) ByName(name string) (model.Entry, bool) {
	for _, e := range es {
		if model.NormalizeName(e.Name) == name {
			return e, true
		}
	}
	return model.Entry{}, false
}

func (es entries) All() []model.Entry { return es }

func ptr[T any](v T) *T { return &v }

const (
	metallicaURL = "https://music.apple.com/us/playlist/metallica-essentials/pl.e55da0d4a18140e3b1120339e2608aa5"
	disturbedURL = "https://music.apple.com/us/album/divisive/1646032515"
	queenURL     = "https://music.apple.com/us/album/bohemian-rhapsody-the-original-soundtrack/6781027361"
)

var testEntries = entries{
	{ID: "metallica-essential", Name: "Metallica — Essential", URL: metallicaURL, Shuffle: true, Repeat: model.RepeatAll, TagIDs: []string{}},
	{ID: "disturbed-divisible", Name: "Disturbed — Divisible", URL: disturbedURL, Volume: ptr(50), TagIDs: []string{}},
	{ID: "queen", Name: "Queen — Bohemian Rhapsody", URL: queenURL, Repeat: model.RepeatOne, TagIDs: []string{"tag-queen"}},
	{ID: "on-ma", Name: "On MA", URL: disturbedURL, Player: ptr("music_assistant"), TagIDs: []string{}},
}

func fakeHA() *hatest.Fake {
	return &hatest.Fake{
		AreaList: []ha.Area{
			{AreaID: "family_room", Name: "Family Room"},
			{AreaID: "office", Name: "Office"},
			{AreaID: "garage", Name: "Garage"},
		},
		DeviceList: []ha.Device{
			{ID: "reader-fr", AreaID: "family_room"},
			{ID: "reader-office", AreaID: "office"},
			{ID: "reader-garage", AreaID: "garage"},
			{ID: "reader-nowhere"},
			{ID: "phone", AreaID: "office"},
			{ID: "arc", AreaID: "family_room", Model: "Arc"},
			{ID: "era", AreaID: "office", Model: "Era 100"},
			{ID: "port", AreaID: "garage", Model: "Port"},
		},
		EntityList: []ha.Entity{
			{EntityID: "event.reader_fr", DeviceID: "reader-fr", Platform: "esphome"},
			{EntityID: "event.reader_office", DeviceID: "reader-office", Platform: "esphome"},
			{EntityID: "event.reader_garage", DeviceID: "reader-garage", Platform: "esphome"},
			{EntityID: "event.reader_nowhere", DeviceID: "reader-nowhere", Platform: "esphome"},
			{EntityID: "device_tracker.phone", DeviceID: "phone", Platform: "mobile_app"},
			{EntityID: "media_player.arc", DeviceID: "arc", Platform: "sonos"},
			{EntityID: "media_player.office", DeviceID: "era", Platform: "sonos"},
			{EntityID: "media_player.garage", DeviceID: "port", Platform: "sonos"},
			{EntityID: "media_player.family_room_ma", Platform: "music_assistant", AreaID: "family_room", ConfigEntryID: "ma-entry"},
		},
	}
}

type harness struct {
	f      *hatest.Fake
	h      *Handler
	ring   *Ring
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, f *hatest.Fake) *harness {
	return startWith(t, f, f, nil)
}

// startWith lets a test wrap the HA client the rooms resolver uses and
// change the Handler before Run starts.
func startWith(t *testing.T, f *hatest.Fake, roomsHA ha.Client, setup func(*Handler)) *harness {
	t.Helper()
	s := &settings{model.DefaultSettings()}
	rs := rooms.New(roomsHA, s)
	ma := musicassistant.New(f, s)
	router := player.NewRouter(s, ma, sonos.New(f, nil), ma)
	ring := NewRing(LogSize)
	h := New(Deps{HA: f, Entries: testEntries, Settings: s, Rooms: rs, Router: router, Log: ring, Logger: slog.New(slog.DiscardHandler)})
	if setup != nil {
		setup(h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	hs := &harness{f: f, h: h, ring: ring, cancel: cancel, done: done}
	t.Cleanup(hs.stop)
	waitFor(t, "subscription", func() bool { return f.Subscribers(EventType) == 1 })
	return hs
}

func (hs *harness) stop() {
	hs.cancel()
	<-hs.done
}

func (hs *harness) scan(tagID, name, device string) {
	hs.scanAt(tagID, name, device, time.Time{})
}

// scanAt emits a scan with the given TimeFired (zero = not set).
func (hs *harness) scanAt(tagID, name, device string, fired time.Time) {
	data := map[string]any{"tag_id": tagID, "device_id": device}
	if name != "" {
		data["name"] = name
	}
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	hs.f.Emit(ha.Event{EventType: EventType, Data: raw, TimeFired: fired})
}

// waitScans waits until n scans are recorded and returns them oldest first.
func (hs *harness) waitScans(t *testing.T, n int) []model.Scan {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d scans", n), func() bool { return len(hs.ring.Recent()) >= n })
	recent := hs.ring.Recent()
	out := make([]model.Scan, len(recent))
	for i, s := range recent {
		out[len(recent)-1-i] = s
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// playMedia returns the play_media calls.
func playMedia(calls []hatest.ServiceCall) []hatest.ServiceCall {
	var out []hatest.ServiceCall
	for _, c := range calls {
		if c.Service == "play_media" {
			out = append(out, c)
		}
	}
	return out
}

func volumeOf(t *testing.T, calls []hatest.ServiceCall) float64 {
	t.Helper()
	for _, c := range calls {
		if c.Service == "volume_set" {
			return c.Data["volume_level"].(float64)
		}
	}
	t.Fatal("no volume_set call")
	return 0
}

func TestScanPlaysByName(t *testing.T) {
	hs := start(t, fakeHA())
	// Name is cut at "|", trimmed and matched without case.
	hs.scan("tag-1", "  metallica — ESSENTIAL | spare copy", "reader-fr")
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanPlayed || s.EntryID != "metallica-essential" || s.Player != "sonos" || s.AreaID != "family_room" {
		t.Fatalf("scan = %+v", s)
	}
	calls := hs.f.ServiceCalls()
	pm := playMedia(calls)
	if len(pm) != 1 || pm[0].Data["media_content_id"] != metallicaURL || pm[0].Target.EntityID[0] != "media_player.arc" {
		t.Fatalf("play_media = %+v", pm)
	}
	if v := volumeOf(t, calls); v != 0.30 {
		t.Errorf("volume = %v, want area volume 0.30", v)
	}
	if calls[3].Service != "shuffle_set" || calls[3].Data["shuffle"] != true {
		t.Errorf("shuffle call = %+v", calls[3])
	}
}

func TestScanTagIDBeatsName(t *testing.T) {
	hs := start(t, fakeHA())
	hs.scan("tag-queen", "Metallica — Essential", "reader-office")
	s := hs.waitScans(t, 1)[0]
	if s.EntryID != "queen" {
		t.Fatalf("EntryID = %q, want queen (tag ID match)", s.EntryID)
	}
	calls := hs.f.ServiceCalls()
	if pm := playMedia(calls); pm[0].Data["media_content_id"] != queenURL {
		t.Errorf("played %v", pm[0].Data["media_content_id"])
	}
	if calls[2].Data["repeat"] != "one" {
		t.Errorf("repeat = %v", calls[2].Data["repeat"])
	}
}

func TestScanVolumeOrder(t *testing.T) {
	tests := []struct {
		name, tag, device string
		want              float64
	}{
		{"entry volume", "Disturbed — Divisible", "reader-fr", 0.50},
		{"area volume", "Metallica — Essential", "reader-fr", 0.30},
		{"default volume", "Metallica — Essential", "reader-garage", 0.15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := start(t, fakeHA())
			hs.scan("", tt.tag, tt.device)
			if s := hs.waitScans(t, 1)[0]; s.Result != model.ScanPlayed {
				t.Fatalf("scan = %+v", s)
			}
			if v := volumeOf(t, hs.f.ServiceCalls()); v != tt.want {
				t.Errorf("volume = %v, want %v", v, tt.want)
			}
		})
	}
}

func TestScanFiltersReaders(t *testing.T) {
	hs := start(t, fakeHA())
	hs.scan("tag-1", "Metallica — Essential", "phone")
	hs.scan("tag-1", "Metallica — Essential", "")
	hs.scan("tag-1", "Metallica — Essential", "unknown-device")
	hs.scan("tag-1", "Metallica — Essential", "reader-nowhere")
	scans := hs.waitScans(t, 4)
	for i, want := range []model.ScanResult{model.ScanIgnored, model.ScanIgnored, model.ScanIgnored, model.ScanError} {
		if scans[i].Result != want {
			t.Errorf("scan %d (%s) = %s, want %s", i, scans[i].DeviceID, scans[i].Result, want)
		}
	}
	if calls := hs.f.ServiceCalls(); len(calls) != 0 {
		t.Errorf("service calls for filtered scans: %+v", calls)
	}
}

func TestScanUnknownRecorded(t *testing.T) {
	hs := start(t, fakeHA())
	hs.scan("tag-x", "Some random tag", "reader-fr")
	hs.scan("tag-y", "Two — Dashes — Here", "reader-fr")
	scans := hs.waitScans(t, 2)
	for _, s := range scans {
		if s.Result != model.ScanNoMatch || s.EntryID != "" || s.AreaID != "family_room" {
			t.Errorf("scan = %+v", s)
		}
	}
	if scans[0].TagID != "tag-x" || scans[0].TagName != "Some random tag" {
		t.Errorf("scan fields = %+v", scans[0])
	}
	if calls := hs.f.ServiceCalls(); len(calls) != 0 {
		t.Errorf("service calls for unknown scans: %+v", calls)
	}
}

func TestScanNameFromTagRegistry(t *testing.T) {
	f := fakeHA()
	f.TagList = []ha.Tag{{ID: "tag-1", Name: "Metallica — Essential"}}
	hs := start(t, f)
	hs.scan("tag-1", "", "reader-fr")
	if s := hs.waitScans(t, 1)[0]; s.EntryID != "metallica-essential" || s.TagName != "Metallica — Essential" {
		t.Fatalf("scan = %+v", s)
	}
}

func TestScanSearchFallback(t *testing.T) {
	f := fakeHA()
	f.Respond = func(c hatest.ServiceCall) (json.RawMessage, error) {
		if c.Service == "search" {
			if c.Data["artist"] != "Ray Charles" || c.Data["name"] != "The Ultimate Collection" {
				return nil, fmt.Errorf("bad search %v", c.Data)
			}
			return json.RawMessage(`{"albums":[{"uri":"apple_music://album/1544753335"}],"playlists":[]}`), nil
		}
		return nil, nil
	}
	hs := start(t, f)
	hs.scan("tag-r", "Ray Charles — The Ultimate Collection | b", "reader-fr")
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanPlayed || s.Player != "sonos" || s.EntryID != "" {
		t.Fatalf("scan = %+v", s)
	}
	pm := playMedia(hs.f.ServiceCalls())
	if len(pm) != 1 || pm[0].Data["media_content_id"] != "https://music.apple.com/us/album/album-1544753335/1544753335" {
		t.Fatalf("play_media = %+v", pm)
	}
}

func TestScanSearchNotFoundIsNoMatch(t *testing.T) {
	f := fakeHA()
	f.Respond = func(c hatest.ServiceCall) (json.RawMessage, error) {
		return json.RawMessage(`{"albums":[],"playlists":[]}`), nil
	}
	hs := start(t, f)
	hs.scan("tag-r", "Nobody — Nothing", "reader-fr")
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanNoMatch || s.Error == "" {
		t.Fatalf("scan = %+v", s)
	}
}

func TestScanPlayerOverride(t *testing.T) {
	hs := start(t, fakeHA())
	hs.scan("", "On MA", "reader-fr")
	s := hs.waitScans(t, 1)[0]
	if s.Player != "music_assistant" {
		t.Fatalf("scan = %+v", s)
	}
}

func TestScanErrorRecorded(t *testing.T) {
	f := fakeHA()
	f.Respond = func(c hatest.ServiceCall) (json.RawMessage, error) {
		return nil, &ha.Error{Code: "home_assistant_error", Message: "speaker offline"}
	}
	hs := start(t, f)
	hs.scan("", "Metallica — Essential", "reader-office") // office has no MA entity
	s := hs.waitScans(t, 1)[0]
	if s.Result != model.ScanError || s.EntryID != "metallica-essential" || s.Error == "" {
		t.Fatalf("scan = %+v", s)
	}
}

// Scans in one area run one at a time and in order. Another area is not
// held up by a slow play.
func TestScansSerializedPerArea(t *testing.T) {
	f := fakeHA()
	release := make(chan struct{})
	var mu sync.Mutex
	active := map[string]int{}
	maxActive := map[string]int{}
	var order []string
	f.Respond = func(c hatest.ServiceCall) (json.RawMessage, error) {
		ent := c.Target.EntityID[0]
		switch c.Service {
		case "media_stop":
			mu.Lock()
			active[ent]++
			maxActive[ent] = max(maxActive[ent], active[ent])
			mu.Unlock()
		case "play_media":
			mu.Lock()
			order = append(order, ent+" "+c.Data["media_content_id"].(string))
			mu.Unlock()
			if ent == "media_player.arc" {
				<-release
			}
			mu.Lock()
			active[ent]--
			mu.Unlock()
		}
		return nil, nil
	}
	hs := start(t, f)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock) // runs before the harness stops, even on failure
	hs.scan("", "Metallica — Essential", "reader-fr")
	hs.scan("", "Disturbed — Divisible", "reader-fr")
	hs.scan("", "Metallica — Essential", "reader-office")

	// The office scan finishes while the family room is still blocked.
	waitFor(t, "office scan", func() bool {
		for _, s := range hs.ring.Recent() {
			if s.AreaID == "office" && s.Result == model.ScanPlayed {
				return true
			}
		}
		return false
	})
	if n := len(hs.ring.Recent()); n != 1 {
		t.Fatalf("recorded %d scans while the family room is blocked, want 1", n)
	}
	unblock()
	hs.waitScans(t, 3)

	mu.Lock()
	defer mu.Unlock()
	if maxActive["media_player.arc"] != 1 {
		t.Errorf("max concurrent plays on arc = %d, want 1", maxActive["media_player.arc"])
	}
	var arc []string
	for _, o := range order {
		if o[:len("media_player.arc")] == "media_player.arc" {
			arc = append(arc, o)
		}
	}
	if len(arc) != 2 || arc[0] != "media_player.arc "+metallicaURL || arc[1] != "media_player.arc "+disturbedURL {
		t.Errorf("family room order = %v", arc)
	}
}

func TestPlayEntryAndAreas(t *testing.T) {
	f := fakeHA()
	s := &settings{model.DefaultSettings()}
	rs := rooms.New(f, s)
	ma := musicassistant.New(f, s)
	h := New(Deps{HA: f, Entries: testEntries, Settings: s, Rooms: rs,
		Router: player.NewRouter(s, ma, sonos.New(f, nil), ma), Logger: slog.New(slog.DiscardHandler)})
	ctx := context.Background()

	name, err := h.PlayEntry(ctx, testEntries[1], "office")
	if err != nil || name != "sonos" {
		t.Fatalf("PlayEntry = %q, %v", name, err)
	}
	if v := volumeOf(t, f.Calls); v != 0.50 {
		t.Errorf("volume = %v", v)
	}
	if _, err := h.PlayEntry(ctx, model.Entry{URL: "not a url"}, "office"); err == nil {
		t.Error("bad URL: want error")
	}
	if _, err := h.PlayEntry(ctx, testEntries[0], "attic"); err == nil {
		t.Error("unknown area: want error")
	}

	areas, err := h.Areas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := model.AreaInfo{ID: "family_room", Name: "Family Room", SonosEntity: "media_player.arc", MAEntity: "media_player.family_room_ma", Volume: 30}
	if len(areas) != 3 || areas[0] != want {
		t.Errorf("Areas = %+v", areas)
	}
}

func TestParseQuery(t *testing.T) {
	tests := []struct {
		in     string
		artist string
		album  string
		ok     bool
	}{
		{"Disturbed — Divisive", "Disturbed", "Divisive", true},
		{"  Ray Charles—The Ultimate Collection | 2 ", "Ray Charles", "The Ultimate Collection", true},
		{"Metallica - Essential", "", "", false}, // hyphen is not an em dash
		{"A — B — C", "", "", false},
		{"— Album", "", "", false},
		{"Artist —", "", "", false},
		{"Volodymyr Ivasiuk", "", "", false},
		{"Artist | — Album", "", "", false}, // dash after the cut
	}
	for _, tt := range tests {
		q, ok := ParseQuery(tt.in)
		if ok != tt.ok || (ok && (q.Artist != tt.artist || q.Album != tt.album)) {
			t.Errorf("ParseQuery(%q) = %+v, %v", tt.in, q, ok)
		}
	}
}

func TestRing(t *testing.T) {
	r := NewRing(3)
	if got := r.Recent(); len(got) != 0 {
		t.Fatalf("empty ring = %v", got)
	}
	for i := 1; i <= 5; i++ {
		r.Add(model.Scan{TagID: fmt.Sprint(i)})
		got := r.Recent()
		if len(got) != min(i, 3) || got[0].TagID != fmt.Sprint(i) {
			t.Fatalf("after %d adds: %v", i, got)
		}
	}
	got := r.Recent()
	if got[0].TagID != "5" || got[1].TagID != "4" || got[2].TagID != "3" {
		t.Errorf("Recent = %v, want 5 4 3", got)
	}
	// Recent returns a copy.
	got[0].TagID = "x"
	if r.Recent()[0].TagID != "5" {
		t.Error("Recent shares the buffer")
	}
}

func TestRingConcurrent(t *testing.T) {
	r := NewRing(LogSize)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				r.Add(model.Scan{})
				_ = r.Recent()
			}
		}()
	}
	wg.Wait()
	if n := len(r.Recent()); n != LogSize {
		t.Errorf("len = %d", n)
	}
}

func TestScanAge(t *testing.T) {
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		fired time.Time
		want  model.ScanResult
	}{
		{"stale", now.Add(-MaxScanAge - time.Second), model.ScanIgnored},
		{"fresh", now.Add(-5 * time.Second), model.ScanPlayed},
		{"at the limit", now.Add(-MaxScanAge), model.ScanPlayed},
		{"clock skew", now.Add(time.Minute), model.ScanPlayed},
		{"no TimeFired", time.Time{}, model.ScanPlayed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := fakeHA()
			hs := startWith(t, f, f, func(h *Handler) { h.now = func() time.Time { return now } })
			hs.scanAt("", "Metallica — Essential", "reader-fr", tc.fired)
			s := hs.waitScans(t, 1)[0]
			if s.Result != tc.want {
				t.Fatalf("result = %s, want %s (%+v)", s.Result, tc.want, s)
			}
			played := len(playMedia(hs.f.ServiceCalls())) > 0
			if played != (tc.want == model.ScanPlayed) {
				t.Errorf("played = %v for result %s", played, s.Result)
			}
			if tc.want == model.ScanIgnored && !strings.Contains(s.Error, "old") {
				t.Errorf("ignored scan has no reason: %q", s.Error)
			}
		})
	}
}

// stallingHA never answers the device registry until ctx is done.
type stallingHA struct{ *hatest.Fake }

func (stallingHA) Devices(ctx context.Context) ([]ha.Device, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestScanAreaLookupTimeout(t *testing.T) {
	f := fakeHA()
	hs := startWith(t, f, stallingHA{f}, func(h *Handler) { h.areaTimeout = 20 * time.Millisecond })
	hs.scan("", "Metallica — Essential", "reader-fr")
	hs.scan("", "Metallica — Essential", "reader-office")
	scans := hs.waitScans(t, 2) // the second one is not stuck behind the first
	for _, s := range scans {
		if s.Result != model.ScanError || !strings.Contains(s.Error, "deadline") {
			t.Errorf("scan = %+v, want an area lookup timeout", s)
		}
	}
}
