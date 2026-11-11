package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/artwork"
	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/records"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
	"github.com/igrybkov/ha-nfc-player/internal/scan"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// --- fakes for other tracks' components ---

type fakePlay struct {
	mu     sync.Mutex
	played []string // record id @ area
	cmds   []model.PlayCommand
	stops  []model.Target
	err    error
	panic  bool
	areas  []model.AreaInfo
}

func (f *fakePlay) PlayRecord(_ context.Context, id, areaID string) (string, error) {
	if f.panic {
		panic("boom")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.played = append(f.played, id+"@"+areaID)
	return "sonos", f.err
}

func (f *fakePlay) Play(_ context.Context, cmd model.PlayCommand) (model.PlayResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if f.err != nil {
		return model.PlayResult{}, f.err
	}
	return model.PlayResult{Player: "music_assistant", RecordID: cmd.Record, Media: cmd.Media}, nil
}

func (f *fakePlay) Stop(_ context.Context, t model.Target) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, t)
	return "sonos", f.err
}

func (f *fakePlay) Areas(context.Context) ([]model.AreaInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.areas, nil
}

type fakeScans struct{ scans []model.Scan }

func (f *fakeScans) Add(s model.Scan)     { f.scans = append([]model.Scan{s}, f.scans...) }
func (f *fakeScans) Recent() []model.Scan { return f.scans }

// fakeArt keys subjects by id, or by URL for a bare media link.
type fakeArt struct {
	mu       sync.Mutex
	fail     map[string]bool // subject key → error
	calls    []string        // subject keys
	subs     []model.Subject
	refreshs int
	shade    uint8 // pixel value; 0 = 200
}

func artKey(s model.Subject) string {
	if s.ID != "" {
		return s.ID
	}
	return s.URL
}

func (f *fakeArt) Artwork(_ context.Context, s model.Subject, refresh bool) (image.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, artKey(s))
	f.subs = append(f.subs, s)
	if refresh {
		f.refreshs++
	}
	if f.fail[artKey(s)] {
		return nil, errors.New("no art")
	}
	shade := f.shade
	if shade == 0 {
		shade = 200
	}
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	return img, nil
}

func (f *fakeArt) lastSubject(t *testing.T) model.Subject {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.subs) == 0 {
		t.Fatal("no artwork asked for")
	}
	return f.subs[len(f.subs)-1]
}

type fakeTags struct {
	tags []ha.Tag
	err  error
}

func (f *fakeTags) Tags(context.Context) ([]ha.Tag, error) { return f.tags, f.err }

func (f *fakeTags) RenameTag(_ context.Context, id, name string) error {
	if f.err != nil {
		return f.err
	}
	for i := range f.tags {
		if f.tags[i].ID == id {
			f.tags[i].Name = name
			return nil
		}
	}
	return &ha.Error{Code: "not_found", Message: "Unable to find tag_id"}
}

func (f *fakeTags) DeleteTag(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	for i := range f.tags {
		if f.tags[i].ID == id {
			f.tags = append(f.tags[:i], f.tags[i+1:]...)
			return nil
		}
	}
	return &ha.Error{Code: "not_found", Message: "Unable to find tag_id"}
}

type fakeLayout struct{}

func (fakeLayout) ID() string   { return "web-test" }
func (fakeLayout) Name() string { return "Web test" }
func (fakeLayout) Draw(labels.Canvas, labels.Rect, labels.Item, labels.Background, labels.Template) error {
	return nil
}

// The real Template.Validate checks the layout registry; register a test
// layout once so user templates in these tests validate.
var registerLayout sync.Once

type fakeLabels struct {
	mu   sync.Mutex
	jobs []labels.Job
}

func (f *fakeLabels) Render(w io.Writer, job labels.Job) error {
	f.mu.Lock()
	f.jobs = append(f.jobs, job)
	f.mu.Unlock()
	_, err := io.WriteString(w, "%PDF-1.4 fake")
	return err
}

func (f *fakeLabels) SheetMap(t labels.Template, job labels.Job) [][]labels.SlotState {
	f.mu.Lock()
	f.jobs = append(f.jobs, job)
	f.mu.Unlock()
	per := t.Grid.Cols * t.Grid.Rows
	start := (job.StartRow-1)*t.Grid.Cols + job.StartCol - 1 + job.Skip
	n := len(job.Items) * job.Copies
	var pages [][]labels.SlotState
	for i := 0; i < start+n || len(pages) == 0; i += per {
		page := make([]labels.SlotState, per)
		for j := range page {
			switch k := i + j; {
			case k < start:
				page[j] = labels.SlotUsed
			case k < start+n:
				page[j] = labels.SlotPrinted
			default:
				page[j] = labels.SlotFree
			}
		}
		pages = append(pages, page)
	}
	return pages
}

func (f *fakeLabels) Layouts() []labels.Layout { return []labels.Layout{fakeLayout{}} }

func (f *fakeLabels) ValidateTemplate(t labels.Template) error {
	if t.ID == "" {
		return errors.New("id is required")
	}
	if t.Grid.Cols <= 0 || t.Grid.Rows <= 0 {
		return errors.New("grid must be positive")
	}
	return nil
}

func (f *fakeLabels) lastJob(t *testing.T) labels.Job {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.jobs) == 0 {
		t.Fatal("no job recorded")
	}
	return f.jobs[len(f.jobs)-1]
}

// --- test env ---

type testEnv struct {
	h        http.Handler
	recs     *records.Store
	picker   *fakePicker
	settings *config.SettingsStore
	play     *fakePlay
	scans    *fakeScans
	art      *fakeArt
	tags     *fakeTags
	lbl      *fakeLabels
}

func newEnv(t *testing.T, supervisor bool) *testEnv {
	t.Helper()
	registerLayout.Do(func() { labels.RegisterLayout(fakeLayout{}) })
	dir := t.TempDir()
	tpls, err := config.NewTemplateStore(store.Open(filepath.Join(dir, config.TemplatesFileName), config.DefaultTemplatesFile))
	if err != nil {
		t.Fatal(err)
	}
	bgs, err := config.NewBackgroundStore(store.Open(filepath.Join(dir, config.BackgroundsFileName), config.DefaultBackgroundsFile))
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{
		recs:     records.New(store.Open(filepath.Join(dir, records.FileName), records.DefaultFile)),
		settings: config.NewSettingsStore(store.Open(filepath.Join(dir, config.SettingsFileName), model.DefaultSettings)),
		play:     &fakePlay{areas: []model.AreaInfo{{ID: "office", Name: "Office", SonosEntity: "media_player.office", Volume: 15}}},
		scans:    &fakeScans{},
		art:      &fakeArt{fail: map[string]bool{}},
		tags:     &fakeTags{},
		lbl:      &fakeLabels{},
		picker:   &fakePicker{},
	}
	e.h = New(Deps{
		Records: e.recs, Settings: e.settings, Templates: tpls, Backgrounds: bgs,
		Play: e.play, Scans: e.scans, Artwork: e.art, HATags: e.tags, Labels: e.lbl, ArtPicker: e.picker,
		Supervisor: supervisor,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
	return e
}

type reqOpt func(*http.Request)

func remote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }
func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (e *testEnv) do(t *testing.T, method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, path, rd)
	if rd != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d; body %s", w.Code, code, w.Body.String())
	}
}

func wantJSONError(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	wantStatus(t, w, code)
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type %q", w.Header().Get("Content-Type"))
	}
	if b := decode[errorBody](t, w); b.Error == "" {
		t.Fatal("error message is empty")
	}
}

// media is the record items for media texts.
func media(m ...string) []model.RecordItem {
	var out []model.RecordItem
	for _, x := range m {
		out = append(out, model.RecordItem{Media: x})
	}
	return out
}

func (e *testEnv) mustCreate(t *testing.T, r model.Record) model.Record {
	t.Helper()
	got, err := e.recs.Create(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// --- tests ---

func TestHealthz(t *testing.T) {
	e := newEnv(t, false)
	wantStatus(t, e.do(t, "GET", "/healthz", nil), http.StatusOK)
}

func TestRecordsCRUD(t *testing.T) {
	e := newEnv(t, false)

	w := e.do(t, "POST", "/api/records", map[string]any{
		"name": "Metallica — Essential", "items": []any{map[string]any{"media": "https://music.apple.com/us/playlist/x/pl.1"}},
		"shuffle": map[string]any{"albums": true, "artists": nil, "playlists": false, "other": nil},
		"label":   map[string]any{"title": " Metallica ", "subtitle": "", "artwork": nil},
	})
	wantStatus(t, w, http.StatusCreated)
	created := decode[model.Record](t, w)
	if created.ID != "metallica-essential" || created.Aliases == nil || len(created.Items) != 1 || !created.Shuffle.For(model.MediaAlbum) || created.Shuffle.For(model.MediaPlaylist) {
		t.Fatalf("created %+v", created)
	}
	if created.Label.Title == nil || *created.Label.Title != "Metallica" || created.Label.Subtitle != nil {
		t.Fatalf("label %+v", created.Label)
	}

	wantJSONError(t, e.do(t, "POST", "/api/records", map[string]any{"name": "metallica — essential | x"}), http.StatusConflict)
	wantJSONError(t, e.do(t, "POST", "/api/records", map[string]any{"name": " "}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/records", map[string]any{"name": "X", "items": []any{map[string]any{"media": "https://example.com/x"}}}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/records", map[string]any{"name": "X", "items": []any{map[string]any{"record": "nope"}}}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/records", map[string]any{"name": "X", "volum": 3}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/records", "{not json"), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/records", map[string]any{"id": created.ID, "name": "Other"}), http.StatusConflict)

	mix := e.mustCreate(t, model.Record{Name: "Mix", Items: []model.RecordItem{{Record: created.ID}}})

	w = e.do(t, "GET", "/api/records", nil)
	wantStatus(t, w, http.StatusOK)
	if list := decode[[]model.Record](t, w); len(list) != 2 {
		t.Fatalf("list %+v", list)
	}

	vol := -10
	created.VolumeOffset = &vol
	w = e.do(t, "PUT", "/api/records/metallica-essential", created)
	wantStatus(t, w, http.StatusOK)
	if got := decode[model.Record](t, w); got.VolumeOffset == nil || *got.VolumeOffset != -10 {
		t.Fatalf("updated %+v", got)
	}
	created.ID = ""
	wantJSONError(t, e.do(t, "PUT", "/api/records/missing", created), http.StatusNotFound)
	created.ID = "Bad Id"
	wantJSONError(t, e.do(t, "PUT", "/api/records/metallica-essential", created), http.StatusBadRequest)

	// Rename: the new id goes in the body; the old one stays an alias and
	// records that hold it follow.
	created.ID = "metallica"
	w = e.do(t, "PUT", "/api/records/metallica-essential", created)
	wantStatus(t, w, http.StatusOK)
	if got := decode[model.Record](t, w); got.ID != "metallica" || len(got.Aliases) != 1 || got.Aliases[0] != "metallica-essential" {
		t.Fatalf("renamed %+v", got)
	}
	if got, _ := e.recs.Get(mix.ID); got.Items[0].Record != "metallica" {
		t.Fatalf("reference not renamed: %+v", got.Items)
	}

	wantStatus(t, e.do(t, "DELETE", "/api/records/metallica", nil), http.StatusNoContent)
	wantJSONError(t, e.do(t, "DELETE", "/api/records/metallica", nil), http.StatusNotFound)
}

func TestPlayCommand(t *testing.T) {
	e := newEnv(t, false)

	w := e.do(t, "POST", "/api/play", map[string]any{"area": "office", "record": "kitchen", "volume": 20})
	wantStatus(t, w, http.StatusOK)
	if got := decode[model.PlayResult](t, w); got.Player != "music_assistant" || got.RecordID != "kitchen" {
		t.Fatalf("result %+v", got)
	}
	cmd := e.play.cmds[0]
	if cmd.Source != model.SourceUI || cmd.AreaID != "office" || cmd.Volume == nil || *cmd.Volume != 20 {
		t.Fatalf("cmd %+v", cmd)
	}
	// The v1 keys still pass through to scan, which resolves them.
	for _, body := range []map[string]any{{"area": "office", "tag": "x"}, {"area": "office", "pool": "all"}} {
		wantStatus(t, e.do(t, "POST", "/api/play", body), http.StatusOK)
	}

	for _, body := range []any{
		map[string]any{"record": "kitchen"},                                               // no target
		map[string]any{"area": "office", "player": "media_player.x", "record": "kitchen"}, // two targets
		map[string]any{"area": "office", "media": " "},                                    // no media
		map[string]any{"area": "office", "media": "x", "source": "event"},                 // source is not client input
		"{",
	} {
		wantJSONError(t, e.do(t, "POST", "/api/play", body), http.StatusBadRequest)
	}
	if len(e.play.cmds) != 3 {
		t.Fatalf("bad requests reached the player: %+v", e.play.cmds)
	}

	for err, code := range map[error]int{
		fmt.Errorf("%w: kitchen", records.ErrNotFound):                 http.StatusNotFound,
		fmt.Errorf("%w: kitchen", rooms.ErrUnknownArea):                http.StatusNotFound,
		fmt.Errorf("%w: light.x", rooms.ErrUnsupportedEntity):          http.StatusBadRequest,
		fmt.Errorf("%w: volume 120", scan.ErrInvalid):                  http.StatusBadRequest,
		fmt.Errorf("record \"kitchen\": %w", records.ErrNothingToPlay): http.StatusConflict,
		fmt.Errorf("%w: media_player.x", rooms.ErrUnknownEntity):       http.StatusNotFound,
		errors.New("speaker offline"):                                  http.StatusBadGateway,
	} {
		e.play.err = err
		wantJSONError(t, e.do(t, "POST", "/api/play", map[string]any{"area": "kitchen", "record": "kitchen"}), code)
	}
}

func TestStop(t *testing.T) {
	e := newEnv(t, false)

	w := e.do(t, "POST", "/api/stop", map[string]any{"player": "media_player.office"})
	wantStatus(t, w, http.StatusOK)
	if got := decode[playResponse](t, w); got.Player != "sonos" {
		t.Fatalf("result %+v", got)
	}
	if len(e.play.stops) != 1 || e.play.stops[0].Entity != "media_player.office" {
		t.Fatalf("stops %+v", e.play.stops)
	}
	wantJSONError(t, e.do(t, "POST", "/api/stop", map[string]any{}), http.StatusBadRequest)
	e.play.err = errors.New("HA down")
	wantJSONError(t, e.do(t, "POST", "/api/stop", map[string]any{"area": "office"}), http.StatusBadGateway)
	e.play.err = fmt.Errorf("%w: no target", scan.ErrInvalid)
	wantJSONError(t, e.do(t, "POST", "/api/stop", map[string]any{"area": "office"}), http.StatusBadRequest)
}

func TestEmptyListIsArray(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "GET", "/api/records", nil)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("body %q, want []", w.Body.String())
	}
}

func TestBodyTooLarge(t *testing.T) {
	e := newEnv(t, false)
	big := `{"name":"` + strings.Repeat("a", maxBodyBytes+10) + `"}`
	wantJSONError(t, e.do(t, "POST", "/api/records", big), http.StatusRequestEntityTooLarge)
}

func TestPlay(t *testing.T) {
	e := newEnv(t, false)
	r := e.mustCreate(t, model.Record{Name: "A — B", Items: media("apple_music://album/1")})

	wantJSONError(t, e.do(t, "POST", "/api/records/"+r.ID+"/play", map[string]string{}), http.StatusBadRequest)

	w := e.do(t, "POST", "/api/records/"+r.ID+"/play", map[string]string{"area_id": "office"})
	wantStatus(t, w, http.StatusOK)
	if got := decode[playResponse](t, w); got.Player != "sonos" {
		t.Fatalf("player %q", got.Player)
	}
	if len(e.play.played) != 1 || e.play.played[0] != r.ID+"@office" {
		t.Fatalf("played %v", e.play.played)
	}

	for err, code := range map[error]int{
		fmt.Errorf("%w: nope", records.ErrNotFound):        http.StatusNotFound,
		fmt.Errorf("record: %w", records.ErrNothingToPlay): http.StatusConflict,
		fmt.Errorf("%w: office", rooms.ErrUnknownArea):     http.StatusNotFound,
		errors.New("speaker offline"):                      http.StatusBadGateway,
	} {
		e.play.err = err
		wantJSONError(t, e.do(t, "POST", "/api/records/"+r.ID+"/play", map[string]string{"area_id": "office"}), code)
	}
}

func TestPanicBecomes500(t *testing.T) {
	e := newEnv(t, false)
	r := e.mustCreate(t, model.Record{Name: "A", Items: media("apple_music://album/1")})
	e.play.panic = true
	wantJSONError(t, e.do(t, "POST", "/api/records/"+r.ID+"/play", map[string]string{"area_id": "office"}), http.StatusInternalServerError)
}

func TestArtwork(t *testing.T) {
	e := newEnv(t, false)
	r := e.mustCreate(t, model.Record{Name: "A", Items: media("Some name", "apple_music://album/1")})

	w := e.do(t, "GET", "/api/records/"+r.ID+"/artwork", nil)
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type %q", ct)
	}
	// The art comes from the record's first item with a link.
	if sub := e.art.lastSubject(t); sub.ID != r.ID || sub.Name != "A" || sub.URL != "apple_music://album/1" {
		t.Fatalf("subject %+v", sub)
	}
	// The art behind a URL can change (Refresh art), so browsers revalidate.
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") || strings.Contains(cc, "max-age") {
		t.Fatalf("cache-control %q", cc)
	}
	etag := w.Header().Get("ETag")
	if len(etag) < 10 || etag[0] != '"' {
		t.Fatalf("etag %q", etag)
	}
	img, err := jpeg.Decode(w.Body)
	if err != nil || img.Bounds().Dx() != 100 {
		t.Fatalf("decode: %v %v", err, img)
	}
	w = e.do(t, "GET", "/api/records/"+r.ID+"/artwork", nil, header("If-None-Match", etag))
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("revalidation: status %d, %d bytes; want 304", w.Code, w.Body.Len())
	}
	// New art behind the same URL: a new ETag, and the old one no longer matches.
	e.art.mu.Lock()
	e.art.shade = 90
	e.art.mu.Unlock()
	w = e.do(t, "GET", "/api/records/"+r.ID+"/artwork", nil, header("If-None-Match", etag))
	wantStatus(t, w, http.StatusOK)
	if w.Header().Get("ETag") == etag {
		t.Fatal("ETag did not change with the art")
	}

	w = e.do(t, "GET", "/api/records/"+r.ID+"/artwork?size=32&refresh=1", nil)
	wantStatus(t, w, http.StatusOK)
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("refresh cache-control %q", cc)
	}
	img, err = jpeg.Decode(w.Body)
	if err != nil || img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Fatalf("thumb: %v %v", err, img.Bounds())
	}
	if e.art.refreshs != 1 {
		t.Fatalf("refresh not passed: %d", e.art.refreshs)
	}

	wantJSONError(t, e.do(t, "GET", "/api/records/"+r.ID+"/artwork?size=5", nil), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "GET", "/api/records/nope/artwork", nil), http.StatusNotFound)
	e.art.fail[r.ID] = true
	wantJSONError(t, e.do(t, "GET", "/api/records/"+r.ID+"/artwork", nil), http.StatusNotFound)
}

func TestMediaArtwork(t *testing.T) {
	e := newEnv(t, false)
	const link = "https://music.apple.com/us/album/teaser/1"

	w := e.do(t, "GET", "/api/artwork/media?size=64&url="+url.QueryEscape(link), nil)
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type %q", ct)
	}
	if img, err := jpeg.Decode(w.Body); err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("thumb: %v", err)
	}
	if sub := e.art.lastSubject(t); sub.ID != "" || sub.Name != link || sub.URL != link {
		t.Fatalf("subject %+v", sub)
	}

	for _, u := range []string{"", "Cat Stevens — Teaser", "https://example.com/x"} {
		wantJSONError(t, e.do(t, "GET", "/api/artwork/media?url="+url.QueryEscape(u), nil), http.StatusBadRequest)
	}
	wantJSONError(t, e.do(t, "GET", "/api/artwork/media?size=1&url="+url.QueryEscape(link), nil), http.StatusBadRequest)
	e.art.fail[link] = true
	wantJSONError(t, e.do(t, "GET", "/api/artwork/media?url="+url.QueryEscape(link), nil), http.StatusNotFound)
}

func TestHATags(t *testing.T) {
	e := newEnv(t, false)
	r := e.mustCreate(t, model.Record{Name: "Metallica — Essential", Items: media("apple_music://album/1")})
	now := time.Now().UTC().Truncate(time.Second)
	e.tags.tags = []ha.Tag{
		{ID: "t1", Name: "Metallica — Essential | kitchen", LastScanned: &now},
		{ID: "metallica-essential", Name: "Unknown"}, // a tag id is no match
	}
	w := e.do(t, "GET", "/api/ha/tags", nil)
	wantStatus(t, w, http.StatusOK)
	got := decode[[]haTag](t, w)
	if len(got) != 2 || got[0].RecordID != r.ID || got[1].RecordID != "" {
		t.Fatalf("got %+v", got)
	}
	if got[0].LastScanned == nil || !got[0].LastScanned.Equal(now) {
		t.Fatalf("last_scanned %v", got[0].LastScanned)
	}
	if !strings.Contains(w.Body.String(), `"record_id":""`) {
		t.Fatalf("record_id missing for no match: %s", w.Body.String())
	}

	e.tags.err = errors.New("disconnected")
	wantJSONError(t, e.do(t, "GET", "/api/ha/tags", nil), http.StatusBadGateway)
}

func TestRenameHATag(t *testing.T) {
	e := newEnv(t, false)
	r := e.mustCreate(t, model.Record{Name: "Ghost — Impera", Items: media("apple_music://album/1")})
	e.tags.tags = []ha.Tag{{ID: "t1", Name: "Tag 1a2b"}}

	w := e.do(t, "PUT", "/api/ha/tags/t1", map[string]any{"name": "  Ghost — Impera "})
	wantStatus(t, w, http.StatusOK)
	if got := decode[haTag](t, w); got.Name != "Ghost — Impera" || got.RecordID != r.ID {
		t.Fatalf("got %+v", got)
	}
	if e.tags.tags[0].Name != "Ghost — Impera" {
		t.Fatalf("HA tag %+v", e.tags.tags[0])
	}

	wantJSONError(t, e.do(t, "PUT", "/api/ha/tags/t1", map[string]any{"name": " "}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "PUT", "/api/ha/tags/t1", map[string]any{"name": strings.Repeat("x", 201)}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "PUT", "/api/ha/tags/nope", map[string]any{"name": "x"}), http.StatusNotFound)
	e.tags.err = errors.New("disconnected")
	wantJSONError(t, e.do(t, "PUT", "/api/ha/tags/t1", map[string]any{"name": "x"}), http.StatusBadGateway)
}

func TestDeleteHATag(t *testing.T) {
	e := newEnv(t, false)
	e.tags.tags = []ha.Tag{{ID: "t1", Name: "Old"}, {ID: "t2", Name: "Keep"}}

	wantStatus(t, e.do(t, "DELETE", "/api/ha/tags/t1", nil), http.StatusNoContent)
	if len(e.tags.tags) != 1 || e.tags.tags[0].ID != "t2" {
		t.Fatalf("HA tags %+v", e.tags.tags)
	}
	wantJSONError(t, e.do(t, "DELETE", "/api/ha/tags/t1", nil), http.StatusNotFound)
	e.tags.err = errors.New("disconnected")
	wantJSONError(t, e.do(t, "DELETE", "/api/ha/tags/t2", nil), http.StatusBadGateway)
}

func TestHAAreas(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "GET", "/api/ha/areas", nil)
	wantStatus(t, w, http.StatusOK)
	if got := decode[[]model.AreaInfo](t, w); len(got) != 1 || got[0].SonosEntity != "media_player.office" {
		t.Fatalf("got %+v", got)
	}
	e.play.err = errors.New("down")
	wantJSONError(t, e.do(t, "GET", "/api/ha/areas", nil), http.StatusBadGateway)
}

func TestScans(t *testing.T) {
	e := newEnv(t, false)
	e.scans.Add(model.Scan{TagID: "x", TagName: "New Album", Result: model.ScanNoMatch})
	e.scans.Add(model.Scan{TagID: "y", TagName: "Old | shelf", Result: model.ScanNoMatch})
	r := e.mustCreate(t, model.Record{Name: "Old", Items: media("apple_music://album/1")})

	w := e.do(t, "GET", "/api/scans", nil)
	wantStatus(t, w, http.StatusOK)
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["tag_name"] != "Old | shelf" || got[0]["matches_record"] != r.ID || got[1]["matches_record"] != "" {
		t.Fatalf("got %+v", got)
	}
	if got[0]["result"] != "no_match" {
		t.Fatalf("scan fields not flattened: %+v", got[0])
	}
	if _, ok := got[0]["matches_entry"]; ok {
		t.Fatalf("v1 field left: %+v", got[0])
	}

	e.scans.Add(model.Scan{Source: model.SourceEvent, TagName: "record kitchen", RecordID: "kitchen", Media: "x", Result: model.ScanStopped})
	got = decode[[]map[string]any](t, e.do(t, "GET", "/api/scans", nil))
	if got[0]["source"] != "event" || got[0]["record_id"] != "kitchen" || got[0]["result"] != "stopped" {
		t.Fatalf("event scan %+v", got[0])
	}

	// A tag renamed in HA shows and resolves by its new name.
	e.tags.tags = []ha.Tag{{ID: "x", Name: "Old"}}
	got = decode[[]map[string]any](t, e.do(t, "GET", "/api/scans", nil))
	if last := got[len(got)-1]; last["tag_name"] != "New Album" || last["ha_name"] != "Old" || last["matches_record"] != r.ID {
		t.Fatalf("renamed scan %+v", last)
	}
	e.tags.tags = nil

	// An event's description is not a tag name, even when it equals one.
	e.scans.Add(model.Scan{Source: model.SourceUI, TagName: "Old", Media: "Old", Result: model.ScanNoMatch})
	got = decode[[]map[string]any](t, e.do(t, "GET", "/api/scans", nil))
	if got[0]["matches_record"] != "" {
		t.Fatalf("ui scan resolved to %v", got[0]["matches_record"])
	}
}

func TestSettings(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "GET", "/api/settings", nil)
	wantStatus(t, w, http.StatusOK)
	s := decode[model.Settings](t, w)
	if s.Rooms.DefaultVolume != 15 {
		t.Fatalf("got %+v", s)
	}

	s.Players.Default = []string{"music_assistant", "sonos"}
	s.Print.BleedMM = 2
	w = e.do(t, "PUT", "/api/settings", s)
	wantStatus(t, w, http.StatusOK)
	if got := e.settings.Settings(); got.Players.Default[0] != "music_assistant" || got.Print.BleedMM != 2 {
		t.Fatalf("not saved: %+v", got)
	}

	s.Rooms.DefaultVolume = 500
	wantJSONError(t, e.do(t, "PUT", "/api/settings", s), http.StatusBadRequest)
}

func TestSaveOffset(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "PUT", "/api/print/offsets/avery-5434", model.Offset{XMM: 0.5, YMM: -1})
	wantStatus(t, w, http.StatusOK)
	got := e.settings.Settings().Print.Offsets
	if got["avery-5434"] != (model.Offset{XMM: 0.5, YMM: -1}) || got["avery-5260"].YMM != -0.75 {
		t.Fatalf("offsets %+v", got)
	}
	wantJSONError(t, e.do(t, "PUT", "/api/print/offsets/nope", model.Offset{}), http.StatusNotFound)
	wantJSONError(t, e.do(t, "PUT", "/api/print/offsets/avery-5434", model.Offset{YMM: 99}), http.StatusBadRequest)
}

func userTemplate() labels.Template {
	return labels.Template{
		Name:   "My sheet",
		Page:   labels.Size{WidthIn: 8.5, HeightIn: 11},
		Label:  labels.Size{WidthIn: 2, HeightIn: 1},
		Grid:   labels.Grid{Cols: 3, Rows: 8},
		Margin: labels.Margin{LeftIn: 0.5, TopIn: 0.5},
		Layout: "web-test",
		Font:   labels.FontSizes{TitlePt: 10, SubtitlePt: 8},
	}
}

func TestTemplates(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "GET", "/api/templates", nil)
	wantStatus(t, w, http.StatusOK)
	list := decode[[]labels.Template](t, w)
	if len(list) < 2 || !list[0].Builtin {
		t.Fatalf("list %+v", list)
	}

	w = e.do(t, "POST", "/api/templates", userTemplate())
	wantStatus(t, w, http.StatusCreated)
	created := decode[labels.Template](t, w)
	if created.ID != "my-sheet" || created.Builtin {
		t.Fatalf("created %+v", created)
	}

	bad := created
	bad.Grid.Cols = 0
	wantJSONError(t, e.do(t, "PUT", "/api/templates/my-sheet", bad), http.StatusBadRequest)
	created.Grid.Rows = 7
	wantStatus(t, e.do(t, "PUT", "/api/templates/my-sheet", created), http.StatusOK)

	wantJSONError(t, e.do(t, "PUT", "/api/templates/avery-5260", list[0]), http.StatusForbidden)
	wantJSONError(t, e.do(t, "DELETE", "/api/templates/avery-5260", nil), http.StatusForbidden)
	wantStatus(t, e.do(t, "DELETE", "/api/templates/my-sheet", nil), http.StatusNoContent)
	wantJSONError(t, e.do(t, "DELETE", "/api/templates/my-sheet", nil), http.StatusNotFound)
}

func TestBackgrounds(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "GET", "/api/backgrounds", nil)
	wantStatus(t, w, http.StatusOK)
	if list := decode[[]labels.Background](t, w); len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
	w = e.do(t, "POST", "/api/backgrounds", map[string]any{"name": "Navy", "background": "#000080", "text": "#FFFFFF", "fade": nil, "fade_text": "#FFEE00"})
	wantStatus(t, w, http.StatusCreated)
	b := decode[labels.Background](t, w)
	if b.ID != "navy" || b.Fade != nil || b.FadeText == nil || *b.FadeText != "#FFEE00" {
		t.Fatalf("created %+v", b)
	}
	wantJSONError(t, e.do(t, "POST", "/api/backgrounds", labels.Background{Name: "Bad", Background: "blue", Text: "#FFFFFF"}), http.StatusBadRequest)
	b.Text = "#EEEEEE"
	wantStatus(t, e.do(t, "PUT", "/api/backgrounds/navy", b), http.StatusOK)
	wantJSONError(t, e.do(t, "DELETE", "/api/backgrounds/black", nil), http.StatusForbidden)
	wantStatus(t, e.do(t, "DELETE", "/api/backgrounds/navy", nil), http.StatusNoContent)
}

func TestLayouts(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "GET", "/api/layouts", nil)
	wantStatus(t, w, http.StatusOK)
	if got := decode[[]layoutInfo](t, w); len(got) != 1 || got[0].ID != "web-test" || got[0].Name != "Web test" {
		t.Fatalf("got %+v", got)
	}
}

func TestPrintPreview(t *testing.T) {
	e := newEnv(t, false)
	a := e.mustCreate(t, model.Record{Name: "A — B", Items: media("apple_music://album/1")})
	b := e.mustCreate(t, model.Record{Name: "C", Items: media("apple_music://album/1")})

	w := e.do(t, "POST", "/api/print/preview", printRequest{
		RecordIDs: []string{a.ID, b.ID}, TemplateID: "avery-5434", Copies: 2, StartRow: 2, StartCol: 2,
	})
	wantStatus(t, w, http.StatusOK)
	got := decode[previewResponse](t, w)
	if got.Cols != 2 || got.Rows != 5 || got.Labels != 4 || len(got.Pages) != 1 || len(got.Pages[0]) != 10 {
		t.Fatalf("got %+v", got)
	}
	if got.Pages[0][2] != labels.SlotUsed || got.Pages[0][3] != labels.SlotPrinted || got.Pages[0][7] != labels.SlotFree {
		t.Fatalf("slots %v", got.Pages[0])
	}
	job := e.lbl.lastJob(t)
	if job.Background.ID != "white" || job.InsetMM != 1.5 || job.BleedMM != 1.5 || job.EdgeBleedMM != 0.4 || job.OuterBleedMM != model.DefaultOuterBleedMM {
		t.Fatalf("job %+v", job)
	}
	if len(got.MissingArt) != 0 {
		t.Fatalf("missing art %+v, want none", got.MissingArt)
	}

	// Records without art are listed, so the UI can warn before printing.
	e.art.fail[b.ID] = true
	w = e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{a.ID, b.ID}, TemplateID: "avery-5434"})
	wantStatus(t, w, http.StatusOK)
	if got := decode[previewResponse](t, w); len(got.MissingArt) != 1 || got.MissingArt[0].ID != b.ID || got.MissingArt[0].Name != "C" {
		t.Fatalf("missing art %+v", got.MissingArt)
	}
	// An outline preview prints no art, so it looks none up.
	calls := len(e.art.calls)
	w = e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{b.ID}, TemplateID: "avery-5434", Outline: true})
	if got := decode[previewResponse](t, w); got.MissingArt == nil || len(got.MissingArt) != 0 || len(e.art.calls) != calls {
		t.Fatalf("outline preview: missing %v, art calls %d → %d", got.MissingArt, calls, len(e.art.calls))
	}
	// The template editor's outline preview fills a whole sheet of the
	// largest allowed grid.
	wantStatus(t, e.do(t, "POST", "/api/print/preview", printRequest{Sample: maxSample, TemplateID: "avery-5434", Outline: true}), http.StatusOK)

	// Saved offset is used when the request has none.
	e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{a.ID}, TemplateID: "avery-5260"})
	if job := e.lbl.lastJob(t); job.Offset.YMM != -0.75 {
		t.Fatalf("offset %+v", job.Offset)
	}
	e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{a.ID}, TemplateID: "avery-5260", Offset: &model.Offset{XMM: 1}})
	if job := e.lbl.lastJob(t); job.Offset != (model.Offset{XMM: 1}) {
		t.Fatalf("offset %+v", job.Offset)
	}

	bad := []printRequest{
		{TemplateID: "avery-5434"},
		{RecordIDs: []string{a.ID}},
		{RecordIDs: []string{a.ID}, TemplateID: "avery-5434", StartRow: 6},
		{RecordIDs: []string{a.ID}, TemplateID: "avery-5434", StartCol: 3},
		{RecordIDs: []string{a.ID}, TemplateID: "avery-5434", Copies: 101},
		{RecordIDs: []string{a.ID}, TemplateID: "avery-5434", Skip: -1},
		{RecordIDs: []string{a.ID}, TemplateID: "avery-5434", StartRow: 5, StartCol: 2, Skip: 1},
		{Sample: maxSample + 1, TemplateID: "avery-5434"},
	}
	for i, req := range bad {
		w := e.do(t, "POST", "/api/print/preview", req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d: status %d, body %s", i, w.Code, w.Body.String())
		}
	}
	wantJSONError(t, e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{"nope"}, TemplateID: "avery-5434"}), http.StatusNotFound)
	wantJSONError(t, e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{a.ID}, TemplateID: "nope"}), http.StatusNotFound)
	wantJSONError(t, e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{a.ID}, TemplateID: "avery-5434", BackgroundID: "nope"}), http.StatusNotFound)
}

func TestPrintPDF(t *testing.T) {
	e := newEnv(t, false)
	title := "Custom title"
	a := e.mustCreate(t, model.Record{Name: "Metallica — Essential", Items: media("apple_music://album/1"), Label: model.LabelOverrides{Title: &title}})
	b := e.mustCreate(t, model.Record{Name: "No Art", Items: media("apple_music://album/1")})
	e.art.fail[b.ID] = true

	w := e.do(t, "POST", "/api/print/pdf", printRequest{RecordIDs: []string{a.ID, b.ID}, TemplateID: "avery-5260", BackgroundID: "black"})
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("content type %q", ct)
	}
	if !strings.HasPrefix(w.Body.String(), "%PDF") {
		t.Fatalf("body %q", w.Body.String())
	}
	// Titles in print order (the override, then the name's artist part).
	if cd := w.Header().Get("Content-Disposition"); !regexp.MustCompile(`^inline; filename="custom-title-no-art-[a-z2-7]{8}\.pdf"; filename\*=UTF-8''custom-title-no-art-[a-z2-7]{8}\.pdf$`).MatchString(cd) {
		t.Fatalf("disposition %q", cd)
	}
	job := e.lbl.lastJob(t)
	if len(job.Items) != 2 || job.Background.ID != "black" {
		t.Fatalf("job %+v", job)
	}
	if job.Items[0].Title != "Custom title" || job.Items[0].Subtitle != "Essential" || job.Items[0].Art == nil {
		t.Fatalf("item 0 %+v", job.Items[0])
	}
	if job.Items[1].Title != "No Art" || job.Items[1].Art != nil {
		t.Fatalf("item 1 %+v", job.Items[1])
	}
	if got := w.Header().Get("X-Missing-Art"); got != "1" {
		t.Fatalf("X-Missing-Art = %q, want 1", got)
	}
}

func TestPrintPDFFormPostWithInlineTemplate(t *testing.T) {
	e := newEnv(t, false)
	tpl := userTemplate()
	tpl.ID = "unsaved"
	raw, _ := json.Marshal(printRequest{Template: &tpl, Sample: 24, Outline: true})
	form := url.Values{"job": {string(raw)}}.Encode()

	w := e.do(t, "POST", "/api/print/pdf", form, header("Content-Type", "application/x-www-form-urlencoded"))
	wantStatus(t, w, http.StatusOK)
	job := e.lbl.lastJob(t)
	if job.Template.ID != "unsaved" || !job.Outline || len(job.Items) != 24 || job.Items[0].Title != "Artist" {
		t.Fatalf("job %+v", job)
	}
	if len(e.art.calls) != 0 {
		t.Fatal("outline preview must not fetch art")
	}

	wantJSONError(t, e.do(t, "POST", "/api/print/pdf", "x=1", header("Content-Type", "application/x-www-form-urlencoded")), http.StatusBadRequest)

	// A new template has no id yet; the preview still works.
	tpl.ID = ""
	raw, _ = json.Marshal(printRequest{Template: &tpl, Sample: 1, Outline: true})
	wantStatus(t, e.do(t, "POST", "/api/print/pdf", string(raw)), http.StatusOK)
	if job := e.lbl.lastJob(t); job.Template.ID == "" {
		t.Fatal("inline template without id must get a placeholder id")
	}

	tpl.Grid.Cols = 0
	raw, _ = json.Marshal(printRequest{Template: &tpl, Sample: 1})
	wantJSONError(t, e.do(t, "POST", "/api/print/pdf", string(raw)), http.StatusBadRequest)
}

func TestUI(t *testing.T) {
	e := newEnv(t, false)

	w := e.do(t, "GET", "/", nil)
	wantStatus(t, w, http.StatusOK)
	body := w.Body.String()
	if !strings.Contains(body, `<base href="./">`) || !strings.Contains(body, `src="app.js"`) {
		t.Fatalf("index:\n%s", body)
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("cache-control %q", w.Header().Get("Cache-Control"))
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("csp %q", csp)
	}
	if csp := e.do(t, "GET", "/app.js", nil).Header().Get("Content-Security-Policy"); csp == "" {
		t.Fatal("assets have no CSP")
	}

	w = e.do(t, "GET", "/", nil, header("X-Ingress-Path", "/api/hassio_ingress/abc123"))
	if !strings.Contains(w.Body.String(), `<base href="/api/hassio_ingress/abc123/">`) {
		t.Fatalf("ingress base missing:\n%s", w.Body.String())
	}
	w = e.do(t, "GET", "/", nil, header("X-Ingress-Path", `/x"><script>alert(1)</script>`))
	if strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), `<base href="./">`) {
		t.Fatal("unsafe ingress path must be ignored")
	}
	for p, want := range map[string]string{
		"/api/hassio_ingress/abc-12_3.x/": "/api/hassio_ingress/abc-12_3.x/",
		"/a":                              "/a/",
		"//evil.example/x":                "./",
		"/a//b":                           "./",
		"/a/../b":                         "./",
		"/..":                             "./",
		"/a/./b":                          "./",
		"/":                               "./",
		"a/b":                             "./",
		"/a b":                            "./",
		"/a/b?c":                          "./",
	} {
		w := e.do(t, "GET", "/", nil, header("X-Ingress-Path", p))
		if !strings.Contains(w.Body.String(), `<base href="`+want+`">`) {
			t.Errorf("X-Ingress-Path %q: want base %q", p, want)
		}
	}

	for _, f := range []string{"app.js", "lib.js", "fuzzy.js", "records.js", "tags.js", "print.js", "templates.js", "backgrounds.js", "settings.js"} {
		w := e.do(t, "GET", "/"+f, nil)
		wantStatus(t, w, http.StatusOK)
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Fatalf("%s content type %q", f, ct)
		}
	}
	w = e.do(t, "GET", "/app.css", nil)
	wantStatus(t, w, http.StatusOK)

	wantJSONError(t, e.do(t, "GET", "/api/nope", nil), http.StatusNotFound)
	wantStatus(t, e.do(t, "GET", "/fuzzy_test.mjs", nil), http.StatusNotFound)
}

func TestSupervisorClientFilter(t *testing.T) {
	e := newEnv(t, true)
	cases := map[string]int{
		"172.30.32.2:4567":       http.StatusOK,
		"127.0.0.1:1":            http.StatusOK,
		"[::1]:1":                http.StatusOK,
		"[::ffff:172.30.32.2]:1": http.StatusOK,
		"172.30.32.3:1":          http.StatusForbidden,
		"192.168.1.10:1":         http.StatusForbidden,
		"garbage":                http.StatusForbidden,
	}
	for addr, want := range cases {
		w := e.do(t, "GET", "/api/records", nil, remote(addr), header("X-Forwarded-For", "172.30.32.2"))
		if w.Code != want {
			t.Errorf("%s: status %d, want %d", addr, w.Code, want)
		}
	}
	// Supervisor's watchdog calls /healthz directly, from its own address.
	for _, addr := range []string{"192.168.1.10:1", "172.30.32.1:1"} {
		wantStatus(t, e.do(t, "GET", "/healthz", nil, remote(addr)), http.StatusOK)
	}

	// Outside Supervisor (dev mode) any client is accepted.
	dev := newEnv(t, false)
	wantStatus(t, dev.do(t, "GET", "/api/records", nil, remote("192.168.1.10:1")), http.StatusOK)
}

func TestPDFNames(t *testing.T) {
	job := func(titles ...string) labels.Job {
		j := labels.Job{Template: labels.Template{ID: "avery-5260"}, Background: labels.Background{ID: "black"}, Copies: 1, StartRow: 1, StartCol: 1}
		for _, title := range titles {
			j.Items = append(j.Items, labels.Item{Title: title})
		}
		return j
	}
	hashed := regexp.MustCompile(`^(.*)-([a-z2-7]{8})$`)
	split := func(name string) (string, string) {
		m := hashed.FindStringSubmatch(name)
		if m == nil {
			t.Fatalf("name %q has no hash", name)
		}
		return m[1], m[2]
	}

	// Each title gets its share of 35 characters, at least 8; a title
	// shorter than its share leaves the rest to the others.
	for _, c := range []struct {
		titles []string
		want   string
	}{
		{[]string{"Five Finger Death Punch"}, "five-finger-death-punch"},
		{[]string{"Five Finger Death Punch", "The Rasmus"}, "five-finger-death-punch-the-rasmus"},
		{[]string{"Five Finger Death Punch", "The Rasmus", "Disturbed"}, "five-finger-de-the-rasmus-disturbed"},
		{[]string{"Five Finger Death Punch", "The Rasmus", "Disturbed", "Linkin Park"}, "five-fin-the-rasm-disturbe-linkin-p"},
		// A short name leaves its budget to the others, wherever it is.
		{[]string{"The Hu", "Five Finger Death Punch"}, "the-hu-five-finger-death-punch"},
		{[]string{"Five Finger Death Punch", "The Hu", "Disturbed"}, "five-finger-death-the-hu-disturbed"},
		{[]string{"The Hu", "Five Finger Death Punch", "Linkin Park"}, "the-hu-five-finger-deat-linkin-park"},
		{[]string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P", "Q", "R", "S"}, "a-b-c-d-e-f-g-h-i-j-k-l-m-n-o-p-q-r"},
	} {
		ascii, _ := pdfNames(job(c.titles...), c.titles)
		if got, _ := split(ascii); got != c.want {
			t.Errorf("%q: name = %q, want %q", c.titles, got, c.want)
		}
	}

	ascii, utf := pdfNames(job("The Rasmus", "Disturbed"), []string{"a", "b"})
	if ascii != utf {
		t.Errorf("utf %q != ascii %q for Latin titles", utf, ascii)
	}

	// Cyrillic stays in the UTF-8 name only; a cut never leaves a trailing dash.
	ascii, utf = pdfNames(job("Володимир Івасюк", "AC/DC"), []string{"a", "b"})
	if got, _ := split(ascii); got != "ac-dc" {
		t.Errorf("ascii = %q", ascii)
	}
	if got, _ := split(utf); got != "володимир-івасюк-ac-dc" {
		t.Errorf("utf = %q", utf)
	}
	if got, _ := split(func() string { a, _ := pdfNames(job("Мова"), []string{"a"}); return a }()); got != "labels" {
		t.Errorf("all non-ASCII: ascii = %q, want labels", got)
	}
	if got, _ := split(func() string {
		a, _ := pdfNames(job("abcdefghij-klmnopqrst-uvwxyz-abcde-fghi"), []string{"a"})
		return a
	}()); strings.HasSuffix(got, "-") || len(got) > pdfNameLen {
		t.Errorf("cut name %q", got)
	}

	// The hash follows the order and the settings.
	_, h1 := split(func() string { a, _ := pdfNames(job("A", "B"), []string{"a", "b"}); return a }())
	_, h2 := split(func() string { a, _ := pdfNames(job("B", "A"), []string{"b", "a"}); return a }())
	j := job("A", "B")
	j.Copies = 2
	_, h3 := split(func() string { a, _ := pdfNames(j, []string{"a", "b"}); return a }())
	_, h4 := split(func() string { a, _ := pdfNames(job("A", "B"), []string{"a", "b"}); return a }())
	if h1 == h2 || h1 == h3 || h1 != h4 {
		t.Errorf("hashes: order %s/%s, copies %s, same %s", h1, h2, h3, h4)
	}
}

func TestDefaultBackground(t *testing.T) {
	e := newEnv(t, false)
	a := e.mustCreate(t, model.Record{Name: "Metallica — Essential", Items: media("apple_music://album/1")})
	print := func() string {
		t.Helper()
		w := e.do(t, "POST", "/api/print/pdf", printRequest{RecordIDs: []string{a.ID}, TemplateID: "avery-5260"})
		wantStatus(t, w, http.StatusOK)
		return e.lbl.lastJob(t).Background.ID
	}
	if got := print(); got != model.DefaultBackgroundID {
		t.Fatalf("no default: background %q, want white", got)
	}

	wantStatus(t, e.do(t, "PUT", "/api/print/default-background/nope", nil), http.StatusNotFound)
	w := e.do(t, "PUT", "/api/print/default-background/black", nil)
	wantStatus(t, w, http.StatusOK)
	if got := e.settings.Settings().Print.DefaultBackground; got != "black" {
		t.Fatalf("saved default %q", got)
	}
	if got := print(); got != "black" {
		t.Errorf("background %q, want the default black", got)
	}

	// A default that no longer exists falls back to white.
	if err := e.settings.Update(func(v *model.Settings) error { v.Print.DefaultBackground = "deleted"; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := print(); got != model.DefaultBackgroundID {
		t.Errorf("deleted default: background %q, want white", got)
	}
}

func TestPrintNestedRecord(t *testing.T) {
	e := newEnv(t, false)
	inner := e.mustCreate(t, model.Record{Name: "Disturbed — Divisible", Items: media("Some name", "apple_music://album/7")})
	outer := e.mustCreate(t, model.Record{Name: "Kitchen", Items: []model.RecordItem{{Record: inner.ID}}})

	w := e.do(t, "POST", "/api/print/pdf", printRequest{RecordIDs: []string{outer.ID}, TemplateID: "avery-5260"})
	wantStatus(t, w, http.StatusOK)
	job := e.lbl.lastJob(t)
	if len(job.Items) != 1 || job.Items[0].Title != "Kitchen" || job.Items[0].Art == nil {
		t.Fatalf("items %+v", job.Items)
	}
	// The label is the outer record's; the art is looked up for the inner link.
	if sub := e.art.lastSubject(t); sub.ID != outer.ID || sub.URL != "apple_music://album/7" {
		t.Fatalf("subject %+v", sub)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="kitchen-`) {
		t.Fatalf("disposition %q", cd)
	}

	e.art.fail[outer.ID] = true
	w = e.do(t, "POST", "/api/print/preview", printRequest{RecordIDs: []string{outer.ID}, TemplateID: "avery-5260"})
	if got := decode[previewResponse](t, w); len(got.MissingArt) != 1 || got.MissingArt[0] != (recordRef{ID: outer.ID, Name: "Kitchen"}) {
		t.Fatalf("missing art %+v", got.MissingArt)
	}
}

type fakePicker struct{ previews []string }

func (f *fakePicker) Candidates(_ context.Context, s model.Subject, offset, limit int) (artwork.CandidatePage, error) {
	if offset > 0 {
		return artwork.CandidatePage{Items: []artwork.Candidate{{URL: "https://art/more", Label: "More", Kind: artwork.KindAlbum}}, Next: -1}, nil
	}
	return artwork.CandidatePage{Items: []artwork.Candidate{{URL: "https://art/" + s.ID + "/" + s.Name + "/" + s.URL, Label: "Cover", Kind: artwork.KindCover},
		{URL: "https://art/artist", Label: "Artist", Kind: artwork.KindArtist}}, Next: limit}, nil
}

func (f *fakePicker) SourceURL(_ context.Context, s model.Subject) (string, error) {
	if s.Label.Artwork != nil {
		return *s.Label.Artwork, nil
	}
	if s.URL == "" {
		return "", errors.New("no art")
	}
	return "https://art/" + s.URL, nil
}

func (f *fakePicker) Preview(_ context.Context, u string) (image.Image, error) {
	f.previews = append(f.previews, u)
	if u == "" {
		return nil, errors.New("blocked")
	}
	return image.NewRGBA(image.Rect(0, 0, 300, 300)), nil
}

func TestArtPicker(t *testing.T) {
	e := newEnv(t, false)
	file := "covers/a.jpg"
	a := e.mustCreate(t, model.Record{Name: "A", Items: media("apple_music://album/1")})
	b := e.mustCreate(t, model.Record{Name: "B", Items: media("apple_music://album/2"), Label: model.LabelOverrides{Artwork: &file}})
	empty := e.mustCreate(t, model.Record{Name: "Empty"})
	mix := e.mustCreate(t, model.Record{Name: "Mix", Items: []model.RecordItem{
		{Record: a.ID}, {Record: b.ID}, {Record: empty.ID}, {Media: "Some name"}, {Media: "apple_music://album/1"}, {Media: "apple_music://album/3"},
	}})

	// One link: its candidates, paged, looked up under the record's name.
	page := decode[pickerPageResponse](t, e.do(t, "GET", "/api/records/"+a.ID+"/artwork/candidates", nil))
	want := "https://art/a/A/apple_music://album/1"
	if got := page.Items; len(got) != 2 || page.Next != pickerPage || got[0].URL != want || got[0].Preview != previewPath(want) {
		t.Fatalf("record candidates %+v", page)
	}
	page = decode[pickerPageResponse](t, e.do(t, "GET", "/api/records/"+a.ID+"/artwork/candidates?offset=12", nil))
	if len(page.Items) != 1 || page.Next != -1 {
		t.Fatalf("second page %+v", page)
	}
	wantJSONError(t, e.do(t, "GET", "/api/records/"+a.ID+"/artwork/candidates?offset=-1", nil), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "GET", "/api/records/nope/artwork/candidates", nil), http.StatusNotFound)

	// Several items: the art of each, in order. A file override previews
	// through its record; names, empty records and "others" have no art;
	// the art of A and of its own link is the same URL and shows once.
	got := decode[pickerPageResponse](t, e.do(t, "GET", "/api/records/"+mix.ID+"/artwork/candidates", nil))
	if it := got.Items; len(it) != 3 || got.Next != -1 ||
		it[0].Label != "A" || it[0].Kind != "item" ||
		it[1].URL != file || it[1].Preview != "api/records/"+b.ID+"/artwork" ||
		it[2].Label != "apple_music://album/3" || it[2].URL != "https://art/apple_music://album/3" {
		t.Fatalf("mix candidates %+v", got)
	}
	got = decode[pickerPageResponse](t, e.do(t, "GET", "/api/records/"+mix.ID+"/artwork/candidates?offset=1", nil))
	if len(got.Items) != 0 || got.Next != -1 {
		t.Fatalf("mix second page %+v", got)
	}

	w := e.do(t, "GET", "/api/artwork/preview?url=https%3A%2F%2Fart%2Fx&size=64", nil)
	wantStatus(t, w, http.StatusOK)
	if e.picker.previews[0] != "https://art/x" {
		t.Fatalf("previews %v", e.picker.previews)
	}
	wantJSONError(t, e.do(t, "GET", "/api/artwork/preview", nil), http.StatusNotFound)
	wantJSONError(t, e.do(t, "GET", "/api/artwork/preview?url=x&size=1", nil), http.StatusBadRequest)
}
