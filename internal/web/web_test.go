package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/labels"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/registry"
	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// --- fakes for other tracks' components ---

type fakePlay struct {
	mu     sync.Mutex
	played []string // entry id @ area
	err    error
	panic  bool
	areas  []model.AreaInfo
}

func (f *fakePlay) PlayEntry(_ context.Context, e model.Entry, areaID string) (string, error) {
	if f.panic {
		panic("boom")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.played = append(f.played, e.ID+"@"+areaID)
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

type fakeArt struct {
	mu       sync.Mutex
	fail     map[string]bool // entry id → error
	calls    []string
	refreshs int
}

func (f *fakeArt) Artwork(_ context.Context, e model.Entry, refresh bool) (image.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, e.ID)
	if refresh {
		f.refreshs++
	}
	if f.fail[e.ID] {
		return nil, errors.New("no art")
	}
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	return img, nil
}

type fakeTags struct {
	tags []ha.Tag
	err  error
}

func (f *fakeTags) Tags(context.Context) ([]ha.Tag, error) { return f.tags, f.err }

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
	reg      *registry.Registry
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
	tpls, err := config.NewTemplateStore(store.Open(filepath.Join(dir, config.TemplatesFile), func() []labels.Template { return []labels.Template{} }))
	if err != nil {
		t.Fatal(err)
	}
	bgs, err := config.NewBackgroundStore(store.Open(filepath.Join(dir, config.BackgroundsFile), func() []labels.Background { return []labels.Background{} }))
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{
		reg:      registry.New(store.Open(filepath.Join(dir, config.TagsFile), registry.DefaultTagsFile)),
		settings: config.NewSettingsStore(store.Open(filepath.Join(dir, config.SettingsFile), model.DefaultSettings)),
		play:     &fakePlay{areas: []model.AreaInfo{{ID: "office", Name: "Office", SonosEntity: "media_player.office", Volume: 15}}},
		scans:    &fakeScans{},
		art:      &fakeArt{fail: map[string]bool{}},
		tags:     &fakeTags{},
		lbl:      &fakeLabels{},
	}
	e.h = New(Deps{
		Registry: e.reg, Settings: e.settings, Templates: tpls, Backgrounds: bgs,
		Play: e.play, Scans: e.scans, Artwork: e.art, HATags: e.tags, Labels: e.lbl,
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

func (e *testEnv) mustCreate(t *testing.T, entry model.Entry) model.Entry {
	t.Helper()
	got, err := e.reg.Create(entry)
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

func TestTagsCRUD(t *testing.T) {
	e := newEnv(t, false)

	w := e.do(t, "POST", "/api/tags", map[string]any{"name": "Metallica — Essential", "url": "https://music.apple.com/us/playlist/x/pl.1", "shuffle": true})
	wantStatus(t, w, http.StatusCreated)
	created := decode[model.Entry](t, w)
	if created.ID != "metallica-essential" || created.Repeat != model.RepeatAll || created.TagIDs == nil {
		t.Fatalf("created %+v", created)
	}

	wantJSONError(t, e.do(t, "POST", "/api/tags", map[string]any{"name": "metallica — essential", "url": "u"}), http.StatusConflict)
	wantJSONError(t, e.do(t, "POST", "/api/tags", map[string]any{"name": "X"}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/tags", map[string]any{"name": "X", "url": "u", "volum": 3}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/tags", "{not json"), http.StatusBadRequest)

	w = e.do(t, "GET", "/api/tags", nil)
	wantStatus(t, w, http.StatusOK)
	if list := decode[[]model.Entry](t, w); len(list) != 1 {
		t.Fatalf("list %+v", list)
	}

	created.Volume = new(int)
	*created.Volume = 30
	w = e.do(t, "PUT", "/api/tags/metallica-essential", created)
	wantStatus(t, w, http.StatusOK)
	if got := decode[model.Entry](t, w); got.Volume == nil || *got.Volume != 30 {
		t.Fatalf("updated %+v", got)
	}
	wantJSONError(t, e.do(t, "PUT", "/api/tags/metallica-essential", map[string]any{"id": "other", "name": "X", "url": "u"}), http.StatusBadRequest)
	created.ID = ""
	wantJSONError(t, e.do(t, "PUT", "/api/tags/missing", created), http.StatusNotFound)

	wantStatus(t, e.do(t, "DELETE", "/api/tags/metallica-essential", nil), http.StatusNoContent)
	wantJSONError(t, e.do(t, "DELETE", "/api/tags/metallica-essential", nil), http.StatusNotFound)
}

func TestEmptyListIsArray(t *testing.T) {
	e := newEnv(t, false)
	w := e.do(t, "GET", "/api/tags", nil)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("body %q, want []", w.Body.String())
	}
}

func TestBodyTooLarge(t *testing.T) {
	e := newEnv(t, false)
	big := `{"name":"` + strings.Repeat("a", maxBodyBytes+10) + `","url":"u"}`
	wantJSONError(t, e.do(t, "POST", "/api/tags", big), http.StatusRequestEntityTooLarge)
}

func TestPlay(t *testing.T) {
	e := newEnv(t, false)
	en := e.mustCreate(t, model.Entry{Name: "A — B", URL: "u"})

	wantJSONError(t, e.do(t, "POST", "/api/tags/"+en.ID+"/play", map[string]string{}), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "POST", "/api/tags/nope/play", map[string]string{"area_id": "office"}), http.StatusNotFound)

	w := e.do(t, "POST", "/api/tags/"+en.ID+"/play", map[string]string{"area_id": "office"})
	wantStatus(t, w, http.StatusOK)
	if got := decode[playResponse](t, w); got.Player != "sonos" {
		t.Fatalf("player %q", got.Player)
	}
	if len(e.play.played) != 1 || e.play.played[0] != en.ID+"@office" {
		t.Fatalf("played %v", e.play.played)
	}

	e.play.err = errors.New("speaker offline")
	wantJSONError(t, e.do(t, "POST", "/api/tags/"+en.ID+"/play", map[string]string{"area_id": "office"}), http.StatusBadGateway)
}

func TestPanicBecomes500(t *testing.T) {
	e := newEnv(t, false)
	en := e.mustCreate(t, model.Entry{Name: "A", URL: "u"})
	e.play.panic = true
	wantJSONError(t, e.do(t, "POST", "/api/tags/"+en.ID+"/play", map[string]string{"area_id": "office"}), http.StatusInternalServerError)
}

func TestArtwork(t *testing.T) {
	e := newEnv(t, false)
	en := e.mustCreate(t, model.Entry{Name: "A", URL: "u"})

	w := e.do(t, "GET", "/api/tags/"+en.ID+"/artwork", nil)
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Fatalf("cache-control %q", cc)
	}
	img, err := jpeg.Decode(w.Body)
	if err != nil || img.Bounds().Dx() != 100 {
		t.Fatalf("decode: %v %v", err, img)
	}

	w = e.do(t, "GET", "/api/tags/"+en.ID+"/artwork?size=32&refresh=1", nil)
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

	wantJSONError(t, e.do(t, "GET", "/api/tags/"+en.ID+"/artwork?size=5", nil), http.StatusBadRequest)
	wantJSONError(t, e.do(t, "GET", "/api/tags/nope/artwork", nil), http.StatusNotFound)
	e.art.fail[en.ID] = true
	wantJSONError(t, e.do(t, "GET", "/api/tags/"+en.ID+"/artwork", nil), http.StatusNotFound)
}

func TestHATags(t *testing.T) {
	e := newEnv(t, false)
	byID := e.mustCreate(t, model.Entry{Name: "Linked", URL: "u", TagIDs: []string{"t1"}})
	byName := e.mustCreate(t, model.Entry{Name: "Metallica — Essential", URL: "u"})
	now := time.Now().UTC().Truncate(time.Second)
	e.tags.tags = []ha.Tag{
		{ID: "t1", Name: "whatever"},
		{ID: "t2", Name: "Metallica — Essential | kitchen", LastScanned: &now},
		{ID: "t3", Name: "Unknown"},
	}
	w := e.do(t, "GET", "/api/ha/tags", nil)
	wantStatus(t, w, http.StatusOK)
	got := decode[[]haTag](t, w)
	if len(got) != 3 || got[0].EntryID != byID.ID || got[1].EntryID != byName.ID || got[2].EntryID != "" {
		t.Fatalf("got %+v", got)
	}
	if got[1].LastScanned == nil || !got[1].LastScanned.Equal(now) {
		t.Fatalf("last_scanned %v", got[1].LastScanned)
	}

	e.tags.err = errors.New("disconnected")
	wantJSONError(t, e.do(t, "GET", "/api/ha/tags", nil), http.StatusBadGateway)
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
	e.scans.Add(model.Scan{TagID: "y", TagName: "Old", Result: model.ScanNoMatch})
	en := e.mustCreate(t, model.Entry{Name: "Old", URL: "u"})

	w := e.do(t, "GET", "/api/scans", nil)
	wantStatus(t, w, http.StatusOK)
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["tag_name"] != "Old" || got[0]["matches_entry"] != en.ID || got[1]["matches_entry"] != "" {
		t.Fatalf("got %+v", got)
	}
	if got[0]["result"] != "no_match" {
		t.Fatalf("scan fields not flattened: %+v", got[0])
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
	a := e.mustCreate(t, model.Entry{Name: "A — B", URL: "u"})
	b := e.mustCreate(t, model.Entry{Name: "C", URL: "u"})

	w := e.do(t, "POST", "/api/print/preview", printRequest{
		EntryIDs: []string{a.ID, b.ID}, TemplateID: "avery-5434", Copies: 2, StartRow: 2, StartCol: 2,
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
	if job.Background.ID != "white" || job.InsetMM != 1.5 || job.BleedMM != 1.5 || job.EdgeBleedMM != 0.4 {
		t.Fatalf("job %+v", job)
	}
	if len(e.art.calls) != 0 {
		t.Fatal("preview must not fetch art")
	}

	// Saved offset is used when the request has none.
	e.do(t, "POST", "/api/print/preview", printRequest{EntryIDs: []string{a.ID}, TemplateID: "avery-5260"})
	if job := e.lbl.lastJob(t); job.Offset.YMM != -0.75 {
		t.Fatalf("offset %+v", job.Offset)
	}
	e.do(t, "POST", "/api/print/preview", printRequest{EntryIDs: []string{a.ID}, TemplateID: "avery-5260", Offset: &model.Offset{XMM: 1}})
	if job := e.lbl.lastJob(t); job.Offset != (model.Offset{XMM: 1}) {
		t.Fatalf("offset %+v", job.Offset)
	}

	bad := []printRequest{
		{TemplateID: "avery-5434"},
		{EntryIDs: []string{a.ID}},
		{EntryIDs: []string{a.ID}, TemplateID: "avery-5434", StartRow: 6},
		{EntryIDs: []string{a.ID}, TemplateID: "avery-5434", StartCol: 3},
		{EntryIDs: []string{a.ID}, TemplateID: "avery-5434", Copies: 101},
		{EntryIDs: []string{a.ID}, TemplateID: "avery-5434", Skip: -1},
		{EntryIDs: []string{a.ID}, TemplateID: "avery-5434", StartRow: 5, StartCol: 2, Skip: 1},
		{Sample: maxSample + 1, TemplateID: "avery-5434"},
	}
	for i, req := range bad {
		w := e.do(t, "POST", "/api/print/preview", req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d: status %d, body %s", i, w.Code, w.Body.String())
		}
	}
	wantJSONError(t, e.do(t, "POST", "/api/print/preview", printRequest{EntryIDs: []string{"nope"}, TemplateID: "avery-5434"}), http.StatusNotFound)
	wantJSONError(t, e.do(t, "POST", "/api/print/preview", printRequest{EntryIDs: []string{a.ID}, TemplateID: "nope"}), http.StatusNotFound)
	wantJSONError(t, e.do(t, "POST", "/api/print/preview", printRequest{EntryIDs: []string{a.ID}, TemplateID: "avery-5434", BackgroundID: "nope"}), http.StatusNotFound)
}

func TestPrintPDF(t *testing.T) {
	e := newEnv(t, false)
	title := "Custom title"
	a := e.mustCreate(t, model.Entry{Name: "Metallica — Essential", URL: "u", Label: model.LabelOverrides{Title: &title}})
	b := e.mustCreate(t, model.Entry{Name: "No Art", URL: "u"})
	e.art.fail[b.ID] = true

	w := e.do(t, "POST", "/api/print/pdf", printRequest{EntryIDs: []string{a.ID, b.ID}, TemplateID: "avery-5260", BackgroundID: "black"})
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("content type %q", ct)
	}
	if !strings.HasPrefix(w.Body.String(), "%PDF") {
		t.Fatalf("body %q", w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="labels-avery-5260.pdf"`) {
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

	w = e.do(t, "GET", "/", nil, header("X-Ingress-Path", "/api/hassio_ingress/abc123"))
	if !strings.Contains(w.Body.String(), `<base href="/api/hassio_ingress/abc123/">`) {
		t.Fatalf("ingress base missing:\n%s", w.Body.String())
	}
	w = e.do(t, "GET", "/", nil, header("X-Ingress-Path", `/x"><script>alert(1)</script>`))
	if strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), `<base href="./">`) {
		t.Fatal("unsafe ingress path must be ignored")
	}

	for _, f := range []string{"app.js", "lib.js", "fuzzy.js", "tags.js", "scans.js", "print.js", "templates.js", "backgrounds.js", "settings.js"} {
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
		w := e.do(t, "GET", "/api/tags", nil, remote(addr), header("X-Forwarded-For", "172.30.32.2"))
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
	wantStatus(t, dev.do(t, "GET", "/api/tags", nil, remote("192.168.1.10:1")), http.StatusOK)
}
