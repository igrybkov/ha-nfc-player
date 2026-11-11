// Command nfc-player is the NFC Player Home Assistant app: it plays music when
// an NFC tag is scanned and serves the UI for records and labels.
//
// main only parses flags and loads config; run wires the components so the
// end-to-end test can start the whole app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/igrybkov/ha-nfc-player/internal/artwork"
	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/config"
	"github.com/igrybkov/ha-nfc-player/internal/ha"
	"github.com/igrybkov/ha-nfc-player/internal/migrate"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/player/musicassistant"
	"github.com/igrybkov/ha-nfc-player/internal/player/sonos"
	"github.com/igrybkov/ha-nfc-player/internal/records"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
	"github.com/igrybkov/ha-nfc-player/internal/scan"
	"github.com/igrybkov/ha-nfc-player/internal/store"
	"github.com/igrybkov/ha-nfc-player/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	// shutdownTimeout bounds the stop after SIGTERM. The binary is PID 1
	// under Supervisor (init: false), and Docker kills it after 10s anyway.
	shutdownTimeout = 10 * time.Second
	// scanRestartDelay is the pause before the scan handler subscribes again
	// after HA refused the subscription.
	scanRestartDelay = 5 * time.Second
	healthTimeout    = 3 * time.Second
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	healthcheck := flag.Bool("healthcheck", false, "exit 0 if the local server answers /healthz, else 1")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *healthcheck {
		// Before config.Load: a health probe must not need HA_URL/HA_TOKEN.
		if err := checkHealth(os.Getenv("LISTEN")); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		logger.Error("listen", "addr", cfg.Listen, "err", err)
		os.Exit(1)
	}
	if err := run(ctx, cfg, ln, options{Logger: logger}); err != nil {
		logger.Error("stopped", "err", err)
		os.Exit(1)
	}
}

// options are the parts of run that tests replace. Zero values mean the
// production defaults.
type options struct {
	Logger  *slog.Logger
	Artwork model.ArtworkSource // nil = artwork.Cache under DataDir/cache
}

// run wires the app, serves HTTP on ln and blocks until ctx is done or the
// server fails. HA being unreachable is not an error: the websocket client
// keeps reconnecting and the UI still loads. On return everything is
// stopped and ln is closed.
func run(ctx context.Context, cfg config.Config, ln net.Listener, opts options) error {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Storage. A missing file reads as its defaults and is created on the
	// first save, so nothing is written to config_dir until the user saves.
	// Files of an older schema are migrated in memory; see package migrate.
	files := migrate.Open(cfg.ConfigDir, log)
	recs := records.New(store.In(files, records.FileName, records.DefaultFile))
	settings := config.NewSettingsStore(store.In(files, config.SettingsFileName, model.DefaultSettings))
	templates, err := config.NewTemplateStore(store.In(files, config.TemplatesFileName, config.DefaultTemplatesFile))
	if err != nil {
		ln.Close()
		return err
	}
	themes, err := config.NewThemeStore(store.In(files, config.ThemesFileName, config.DefaultThemesFile))
	if err != nil {
		ln.Close()
		return err
	}

	// Home Assistant and playback. The MA player also serves the
	// "Artist — Album" search for players that can't search.
	client := ha.New(ha.Options{URL: cfg.HAURL, Token: cfg.HAToken, Logger: log})
	resolver := rooms.New(client, settings)
	ma := musicassistant.New(client, settings)
	router := player.NewRouter(settings, ma, sonos.New(client, &catalog.ITunes{}), ma)
	scans := scan.NewRing(scan.LogSize)
	handler := scan.New(scan.Deps{
		HA: client, Records: recs, Settings: settings, Rooms: resolver,
		Router: router, Log: scans, Logger: log,
	})

	art := opts.Artwork
	var (
		picker web.ArtPicker
		titles web.TitleSource
	)
	if art == nil {
		cache := &artwork.Cache{Dir: filepath.Join(cfg.DataDir, "cache"), ConfigDir: cfg.ConfigDir, Logger: log}
		art, picker, titles = cache, cache, cache
	}

	srv := web.New(web.Deps{
		Records: recs, Settings: settings, Templates: templates, Themes: themes,
		Play: handler, Scans: scans, Artwork: art, HATags: client, ArtPicker: picker, Titles: titles,
		Supervisor: cfg.Supervisor, Logger: log,
	})
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a PDF with art can take up to the 90s print timeout.
		IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	var wg sync.WaitGroup
	wg.Go(func() { _ = client.Run(ctx) }) // returns only when ctx is done
	wg.Go(func() { runScans(ctx, handler, log) })
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()
	log.Info("nfc-player started", "version", version, "listen", ln.Addr().String(),
		"supervisor", cfg.Supervisor, "config_dir", cfg.ConfigDir, "data_dir", cfg.DataDir)

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case runErr = <-serveErr:
		runErr = fmt.Errorf("http server: %w", runErr)
	}

	// Cancel first: it stops the HA client and the scan handler, and aborts
	// requests that wait on HA or on a print, so Shutdown does not wait for them.
	cancel()
	sctx, scancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer scancel()
	if err := httpSrv.Shutdown(sctx); err != nil {
		log.Warn("http shutdown", "err", err)
		_ = httpSrv.Close()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-sctx.Done():
		log.Warn("background tasks did not stop in time")
	}
	return runErr
}

// runScans runs the scan handler until ctx is done. Run fails only when HA
// refuses the subscription; try again after a pause instead of exiting.
func runScans(ctx context.Context, h *scan.Handler, log *slog.Logger) {
	for {
		err := h.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Error("scan handler stopped, restarting", "err", err, "in", scanRestartDelay)
		select {
		case <-time.After(scanRestartDelay):
		case <-ctx.Done():
			return
		}
	}
}

// checkHealth GETs /healthz on the loopback port of listen (LISTEN, default
// config.DefaultListen). Only the port is used: the server may listen on all
// interfaces, but the probe runs inside the container.
func checkHealth(listen string) error {
	if listen == "" {
		listen = config.DefaultListen
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("LISTEN %q: %w", listen, err)
	}
	c := &http.Client{Timeout: healthTimeout}
	resp, err := c.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}
