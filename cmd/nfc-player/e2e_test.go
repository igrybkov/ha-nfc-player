package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/ha/hatest"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/scan"
)

const testToken = "e2e-token"

// solidArt is an ArtworkSource that never touches the network.
type solidArt struct{}

func (solidArt) Artwork(context.Context, model.Entry, bool) (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.RGBA{0xFF, 0, 0, 0xFF})
	return img, nil
}

// officeHA is a fake HA with an ESPHome tag reader and a Sonos One, both in
// the office.
func officeHA() *hatest.Fake {
	return &hatest.Fake{
		AreaList: []ha.Area{{AreaID: "office", Name: "Office"}},
		DeviceList: []ha.Device{
			{ID: "dev-reader", Name: "Office tag reader", Model: "m5stack-atom", AreaID: "office"},
			{ID: "dev-sonos", Name: "Office", Manufacturer: "Sonos", Model: "One", AreaID: "office"},
		},
		EntityList: []ha.Entity{
			{EntityID: "binary_sensor.office_tag_reader_status", DeviceID: "dev-reader", Platform: "esphome"},
			{EntityID: "media_player.office", DeviceID: "dev-sonos", Platform: "sonos"},
		},
		TagList: []ha.Tag{{ID: "tag-1", Name: "Metallica — Essential | anything"}},
	}
}

// testClient does not pool connections. A pooled transport can dial a spare
// connection that never sends a request, and http.Server.Shutdown waits up
// to 5s for such a connection, which is the shutdown limit checked below.
var testClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 30 * time.Second}

// app is a running run() with its base URL.
type app struct {
	base    string
	done    chan error
	stop    context.CancelFunc
	stopped bool
}

// startApp runs the wired app on a free loopback port, with the seed files
// from examples/ in a temp config dir.
func startApp(t *testing.T, haURL string) *app {
	t.Helper()
	cfgDir, dataDir := t.TempDir(), t.TempDir()
	for _, name := range []string{config.TagsFile, config.SettingsFile, config.PoolsFile} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "examples", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{LogLevel: "debug", ConfigDir: cfgDir, DataDir: dataDir, HAURL: haURL, HAToken: testToken}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &app{base: "http://" + ln.Addr().String(), done: make(chan error, 1), stop: cancel}
	go func() {
		a.done <- run(ctx, cfg, ln, options{Logger: slog.New(slog.DiscardHandler), Artwork: solidArt{}})
	}()
	t.Cleanup(func() { a.shutdown(t) })
	return a
}

// shutdown stops the app and checks it stops fast. Safe to call twice.
func (a *app) shutdown(t *testing.T) {
	t.Helper()
	if a.stopped {
		return
	}
	a.stopped = true
	a.stop()
	select {
	case err := <-a.done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop within 5s of cancel")
	}
}

func (a *app) get(t *testing.T, path string) (int, string, []byte) {
	t.Helper()
	resp, err := testClient.Get(a.base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func (a *app) post(t *testing.T, path string, v any) (int, string, []byte) {
	t.Helper()
	raw, _ := json.Marshal(v)
	resp, err := testClient.Post(a.base+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEndToEnd(t *testing.T) {
	fake := officeHA()
	srv := hatest.NewServer(fake, testToken)
	defer srv.Close()
	a := startApp(t, srv.URL())

	// Scan: event → seed entry by name (cut at "|") → office Sonos One.
	waitFor(t, "tag_scanned subscription", func() bool { return srv.Subscribers(scan.EventType) > 0 })
	if n := srv.Emit(scan.EventType, map[string]any{
		"tag_id": "tag-1", "name": "Metallica — Essential | anything", "device_id": "dev-reader",
	}); n != 1 {
		t.Fatalf("event reached %d connections", n)
	}
	waitFor(t, "five service calls", func() bool { return len(fake.ServiceCalls()) >= 5 })

	target := &ha.Target{EntityID: []string{"media_player.office"}}
	url := "https://music.apple.com/us/playlist/metallica-essentials/pl.e55da0d4a18140e3b1120339e2608aa5"
	want := []hatest.ServiceCall{
		{Domain: "media_player", Service: "media_stop", Target: target},
		{Domain: "media_player", Service: "volume_set", Data: map[string]any{"volume_level": 0.15}, Target: target},
		{Domain: "media_player", Service: "repeat_set", Data: map[string]any{"repeat": "all"}, Target: target},
		{Domain: "media_player", Service: "shuffle_set", Data: map[string]any{"shuffle": true}, Target: target},
		{Domain: "media_player", Service: "play_media", Data: map[string]any{"media_content_id": url, "media_content_type": "music"}, Target: target},
	}
	if got := fake.ServiceCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("service calls:\n got %+v\nwant %+v", got, want)
	}

	// The scan log shows the play.
	var scans []model.Scan
	waitFor(t, "scan in the log", func() bool {
		_, _, body := a.get(t, "/api/scans")
		_ = json.Unmarshal(body, &scans)
		return len(scans) == 1
	})
	if s := scans[0]; s.Result != model.ScanPlayed || s.EntryID != "metallica-essential" || s.AreaID != "office" || s.Player != "sonos" {
		t.Errorf("scan = %+v", s)
	}

	checkUI(t, a)

	// A PDF with art for three seed entries on black, from row 2 column 2.
	code, ct, body := a.post(t, "/api/print/pdf", map[string]any{
		"entry_ids":   []string{"metallica-essential", "disturbed-divisible", "volodymyr-ivasiuk"},
		"template_id": "avery-5260", "background_id": "black", "start_row": 2, "start_col": 2,
	})
	if code != http.StatusOK || ct != "application/pdf" || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Errorf("print pdf: %d %s %.80q", code, ct, body)
	}

	// The areas screen resolves the office Sonos over the wire.
	code, _, body = a.get(t, "/api/ha/areas")
	var areas []model.AreaInfo
	if err := json.Unmarshal(body, &areas); code != http.StatusOK || err != nil {
		t.Fatalf("areas: %d %s", code, body)
	}
	if len(areas) != 1 || areas[0].SonosEntity != "media_player.office" || areas[0].Volume != 15 {
		t.Errorf("areas = %+v", areas)
	}

	a.shutdown(t)
}

// Play and stop events over the wire, then the web play box, on the seed
// files. The pool comes from examples/pools.yaml.
func TestEndToEndPlayEvents(t *testing.T) {
	fake := officeHA()
	srv := hatest.NewServer(fake, testToken)
	defer srv.Close()
	a := startApp(t, srv.URL())
	waitFor(t, "command subscriptions", func() bool {
		return srv.Subscribers(scan.PlayEventType) > 0 && srv.Subscribers(scan.StopEventType) > 0
	})

	// Values as an HA template renders them.
	url := "https://music.apple.com/us/album/divisive/1646032515"
	srv.Emit(scan.PlayEventType, map[string]any{"area": "office", "media": url, "volume": "40", "shuffle": "false", "pool": ""})
	waitFor(t, "five service calls", func() bool { return len(fake.ServiceCalls()) >= 5 })
	target := &ha.Target{EntityID: []string{"media_player.office"}}
	want := []hatest.ServiceCall{
		{Domain: "media_player", Service: "media_stop", Target: target},
		{Domain: "media_player", Service: "volume_set", Data: map[string]any{"volume_level": 0.4}, Target: target},
		{Domain: "media_player", Service: "repeat_set", Data: map[string]any{"repeat": "all"}, Target: target},
		{Domain: "media_player", Service: "shuffle_set", Data: map[string]any{"shuffle": false}, Target: target},
		{Domain: "media_player", Service: "play_media", Data: map[string]any{"media_content_id": url, "media_content_type": "music"}, Target: target},
	}
	if got := fake.ServiceCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("service calls:\n got %+v\nwant %+v", got, want)
	}

	// Stop with nothing playing stops the Sonos entity.
	srv.Emit(scan.StopEventType, map[string]any{"area": "office"})
	waitFor(t, "media_stop", func() bool { return len(fake.ServiceCalls()) >= 6 })
	if c := fake.ServiceCalls()[5]; c.Service != "media_stop" || !reflect.DeepEqual(c.Target, target) {
		t.Errorf("stop call = %+v", c)
	}
	var scans []model.Scan
	waitFor(t, "two events in the log", func() bool {
		_, _, body := a.get(t, "/api/scans")
		_ = json.Unmarshal(body, &scans)
		return len(scans) == 2
	})

	// The play box: a seed tag by id, synchronous.
	code, _, body := a.post(t, "/api/play", map[string]any{"area": "office", "tag": "kenny-rogers-21-number-ones"})
	var res model.PlayResult
	if err := json.Unmarshal(body, &res); code != http.StatusOK || err != nil || res.Player != "sonos" || res.EntryID != "kenny-rogers-21-number-ones" {
		t.Fatalf("play: %d %s", code, body)
	}

	// The seed pool is served.
	code, _, body = a.get(t, "/api/pools")
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"id":"kitchen"`)) {
		t.Errorf("pools: %d %.200s", code, body)
	}

	_, _, body = a.get(t, "/api/scans")
	if err := json.Unmarshal(body, &scans); err != nil || len(scans) != 3 {
		t.Fatalf("scans: %s", body)
	}
	// Newest first.
	if s := scans[0]; s.Source != model.SourceUI || s.Result != model.ScanPlayed {
		t.Errorf("ui scan = %+v", s)
	}
	if s := scans[1]; s.Source != model.SourceEvent || s.Result != model.ScanStopped || s.TagName != "stop" {
		t.Errorf("stop scan = %+v", s)
	}
	if s := scans[2]; s.Source != model.SourceEvent || s.Result != model.ScanPlayed || s.Media != url || s.AreaID != "office" {
		t.Errorf("play scan = %+v", s)
	}
	a.shutdown(t)
}

// TestStartsWithoutHA: HA down at startup must not stop the UI.
func TestStartsWithoutHA(t *testing.T) {
	a := startApp(t, "ws://127.0.0.1:1/api/websocket")
	checkUI(t, a)
	a.shutdown(t)
}

// checkUI checks the UI index, /healthz and the -healthcheck probe.
func checkUI(t *testing.T, a *app) {
	t.Helper()
	code, ct, body := a.get(t, "/")
	if code != http.StatusOK || !strings.HasPrefix(ct, "text/html") || !bytes.Contains(body, []byte("<script")) {
		t.Errorf("index: %d %s %.120q", code, ct, body)
	}
	if code, _, body := a.get(t, "/healthz"); code != http.StatusOK {
		t.Errorf("healthz: %d %s", code, body)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(a.base, "http://"))
	if err := checkHealth(":" + port); err != nil {
		t.Errorf("checkHealth: %v", err)
	}
}

func TestCheckHealthDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if err := checkHealth(addr); err == nil {
		t.Error("checkHealth on a closed port: want error")
	}
	if err := checkHealth("no-port"); err == nil {
		t.Error("checkHealth with a bad LISTEN: want error")
	}
}
