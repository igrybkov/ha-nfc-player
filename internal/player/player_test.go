package player_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/igrybkov/ha-nfc-player/internal/catalog"
	"github.com/igrybkov/ha-nfc-player/internal/model"
	"github.com/igrybkov/ha-nfc-player/internal/player"
	"github.com/igrybkov/ha-nfc-player/internal/rooms"
)

type settings struct{ s model.Settings }

func (s *settings) Settings() model.Settings { return s.s }

func chainSettings(fallback bool, chain ...string) *settings {
	s := model.DefaultSettings()
	s.Players.Default = chain
	s.Players.Fallback = fallback
	return &settings{s}
}

// fakePlayer records the requests it gets.
type fakePlayer struct {
	name       string
	kinds      []catalog.Kind
	takesQuery bool
	err        error
	got        []player.Request
}

func (p *fakePlayer) Name() string { return p.name }
func (p *fakePlayer) Supports(req player.Request) bool {
	if req.Query != nil {
		return p.takesQuery
	}
	return slices.Contains(p.kinds, req.Media.Kind)
}
func (p *fakePlayer) Play(ctx context.Context, req player.Request) error {
	p.got = append(p.got, req)
	return p.err
}

type fakeSearcher struct {
	media catalog.Media
	err   error
	calls int
}

func (s *fakeSearcher) Search(ctx context.Context, area rooms.Area, q player.Query) (catalog.Media, error) {
	s.calls++
	return s.media, s.err
}

var (
	album    = catalog.Media{Kind: catalog.KindAlbum, ID: "1", URL: "https://music.apple.com/us/album/x/1"}
	station  = catalog.Media{Kind: catalog.KindStation, ID: "ra.1"}
	allKinds = []catalog.Kind{catalog.KindAlbum, catalog.KindPlaylist, catalog.KindSong, catalog.KindArtist}
)

func TestRouterFirstSupportingPlayerWins(t *testing.T) {
	sonos := &fakePlayer{name: "sonos", kinds: allKinds}
	ma := &fakePlayer{name: "music_assistant", kinds: append(allKinds, catalog.KindStation), takesQuery: true}
	r := player.NewRouter(chainSettings(true, "sonos", "music_assistant"), nil, sonos, ma)

	name, err := r.Play(context.Background(), player.Request{Media: album}, "")
	if err != nil || name != "sonos" || len(sonos.got) != 1 || len(ma.got) != 0 {
		t.Fatalf("album: %q, %v; sonos=%d ma=%d", name, err, len(sonos.got), len(ma.got))
	}
	// Sonos can't play a station: skipped, not an error.
	name, err = r.Play(context.Background(), player.Request{Media: station}, "")
	if err != nil || name != "music_assistant" {
		t.Fatalf("station: %q, %v", name, err)
	}
}

func TestRouterFallback(t *testing.T) {
	boom := errors.New("boom")
	sonos := &fakePlayer{name: "sonos", kinds: allKinds, err: boom}
	ma := &fakePlayer{name: "music_assistant", kinds: allKinds}

	r := player.NewRouter(chainSettings(true, "sonos", "music_assistant"), nil, sonos, ma)
	name, err := r.Play(context.Background(), player.Request{Media: album}, "")
	if err != nil || name != "music_assistant" || len(sonos.got) != 1 {
		t.Fatalf("with fallback: %q, %v", name, err)
	}

	sonos.got, ma.got = nil, nil
	r = player.NewRouter(chainSettings(false, "sonos", "music_assistant"), nil, sonos, ma)
	name, err = r.Play(context.Background(), player.Request{Media: album}, "")
	if !errors.Is(err, boom) || name != "" || len(ma.got) != 0 {
		t.Fatalf("without fallback: %q, %v, ma=%d", name, err, len(ma.got))
	}

	// Every player fails: all errors come back.
	ma.err = errors.New("bang")
	r = player.NewRouter(chainSettings(true, "sonos", "music_assistant"), nil, sonos, ma)
	_, err = r.Play(context.Background(), player.Request{Media: album}, "")
	if !errors.Is(err, boom) || !errors.Is(err, ma.err) {
		t.Fatalf("all fail: %v", err)
	}
}

func TestRouterSettingsReadPerCall(t *testing.T) {
	sonos := &fakePlayer{name: "sonos", kinds: allKinds}
	ma := &fakePlayer{name: "music_assistant", kinds: allKinds}
	s := chainSettings(true, "sonos", "music_assistant")
	r := player.NewRouter(s, nil, sonos, ma)
	s.s.Players.Default = []string{"music_assistant", "sonos"}
	if name, _ := r.Play(context.Background(), player.Request{Media: album}, ""); name != "music_assistant" {
		t.Fatalf("chain change not applied: %q", name)
	}
}

func TestRouterOverride(t *testing.T) {
	sonos := &fakePlayer{name: "sonos", kinds: allKinds}
	ma := &fakePlayer{name: "music_assistant", kinds: allKinds}
	r := player.NewRouter(chainSettings(true, "sonos", "music_assistant"), nil, sonos, ma)

	name, err := r.Play(context.Background(), player.Request{Media: album}, "music_assistant")
	if err != nil || name != "music_assistant" || len(sonos.got) != 0 {
		t.Fatalf("override: %q, %v", name, err)
	}
	// A failing override falls back to the rest of the chain.
	ma.err = errors.New("down")
	name, err = r.Play(context.Background(), player.Request{Media: album}, "music_assistant")
	if err != nil || name != "sonos" {
		t.Fatalf("override fallback: %q, %v", name, err)
	}
	if _, err := r.Play(context.Background(), player.Request{Media: album}, "spotify"); !errors.Is(err, player.ErrUnknownPlayer) {
		t.Fatalf("unknown override: %v", err)
	}
}

func TestRouterNoPlayer(t *testing.T) {
	sonos := &fakePlayer{name: "sonos", kinds: allKinds}
	// Unknown names in the chain are skipped.
	r := player.NewRouter(chainSettings(true, "airplay", "sonos"), nil, sonos)
	if _, err := r.Play(context.Background(), player.Request{Media: station}, ""); !errors.Is(err, player.ErrNoPlayer) {
		t.Fatalf("err = %v, want ErrNoPlayer", err)
	}
}

func TestRouterResolvesQueryForPlayerWithoutSearch(t *testing.T) {
	sonos := &fakePlayer{name: "sonos", kinds: allKinds}
	ma := &fakePlayer{name: "music_assistant", kinds: allKinds, takesQuery: true}
	found := catalog.Media{Kind: catalog.KindAlbum, ID: "42", URL: "apple_music://album/42"}
	search := &fakeSearcher{media: found}
	r := player.NewRouter(chainSettings(true, "sonos", "music_assistant"), search, sonos, ma)

	q := &player.Query{Artist: "Disturbed", Album: "Divisive"}
	name, err := r.Play(context.Background(), player.Request{Query: q, Volume: 30}, "")
	if err != nil || name != "sonos" {
		t.Fatalf("%q, %v", name, err)
	}
	got := sonos.got[0]
	if got.Query != nil || got.Media != found || got.Volume != 30 {
		t.Errorf("sonos got %+v", got)
	}

	// Sonos fails: MA gets the already resolved media, no second search.
	sonos.err = errors.New("down")
	name, err = r.Play(context.Background(), player.Request{Query: q}, "")
	if err != nil || name != "music_assistant" || search.calls != 2 {
		t.Fatalf("%q, %v, searches=%d", name, err, search.calls)
	}
	if ma.got[0].Query != nil || ma.got[0].Media != found {
		t.Errorf("ma got %+v", ma.got[0])
	}
}

func TestRouterQueryGoesStraightToSearchingPlayer(t *testing.T) {
	ma := &fakePlayer{name: "music_assistant", kinds: allKinds, takesQuery: true}
	search := &fakeSearcher{}
	r := player.NewRouter(chainSettings(true, "music_assistant"), search, ma)
	q := &player.Query{Artist: "A", Album: "B"}
	if _, err := r.Play(context.Background(), player.Request{Query: q}, ""); err != nil {
		t.Fatal(err)
	}
	if search.calls != 0 || ma.got[0].Query != q {
		t.Errorf("searches=%d, got %+v", search.calls, ma.got[0])
	}
}

func TestRouterSearchFailure(t *testing.T) {
	sonos := &fakePlayer{name: "sonos", kinds: allKinds}
	search := &fakeSearcher{err: player.ErrNotFound}
	r := player.NewRouter(chainSettings(true, "sonos"), search, sonos)
	_, err := r.Play(context.Background(), player.Request{Query: &player.Query{Artist: "A", Album: "B"}}, "")
	if !errors.Is(err, player.ErrNotFound) || len(sonos.got) != 0 {
		t.Fatalf("err = %v", err)
	}

	r = player.NewRouter(chainSettings(true, "sonos"), nil, sonos)
	_, err = r.Play(context.Background(), player.Request{Query: &player.Query{Artist: "A", Album: "B"}}, "")
	if !errors.Is(err, player.ErrNoSearcher) {
		t.Fatalf("no searcher: err = %v", err)
	}
}
