# ha-nfc-player: build plan

A Home Assistant app (Supervisor add-on) written in Go. It plays music when an
NFC tag is scanned, keeps a registry of tags, and prints labels for them.
It replaces the `Play Music when NFC tag is scanned` automation, the
`play_music_in_a_room` / `play_apple_music_artist` scripts and the
`bin/nfc-music-labels` script in the `homeassistant` repo.

Goals, in order:

1. Small footprint. Static binary, `FROM scratch` image, low idle RAM.
2. Modular. Players, label layouts, label templates and backgrounds are
   pluggable. Templates and backgrounds can be added from the UI.
3. No behavior regressions against the current automation.
4. One multi-arch image (`linux/amd64`, `linux/arm64`) on GHCR.

## 1. What the current system does (must keep working)

Source: automation `1709949609238` and `script.play_music_in_a_room` in the
`homeassistant` repo.

- Trigger: `tag_scanned` event. Only from readers that are ESPHome devices
  (`device_id` belongs to an `esphome` integration device).
- Tag lookup is by **tag name**, not tag ID. The name is cut at the first `|`
  and trimmed. Names look like `Metallica — Essential`.
- Music Map: tag name → URL or `{url, shuffle, repeat, volume}`.
  Defaults: `shuffle: false`, `repeat: all`.
- Volume: entry volume, else per-area volume (`Family Room: 30`, `Office: 15`,
  `Master Bathroom: 25`, `Master Bedroom: 25`), else 15. Value is percent.
- Unknown name of the form `Artist — Album` (one `—`): search Music Assistant
  for album/playlist with that artist and name, take the first
  `apple_music://` result, convert to a share link, play it.
- Room player: Sonos `media_player` entities in the reader's area.
  Preference by device model: `Arc`, `Five`, `One`, `Port`, then any.
- Playback sequence: `media_stop` → `volume_set` → `repeat_set` →
  `shuffle_set` → `play_media` (`media_content_type: music`, share link).
- Apple Music artist link (`music.apple.com/<cc>/artist/...`): Sonos can't
  play it. Look up the artist's songs with the iTunes API
  (`https://itunes.apple.com/lookup?id=<id>&entity=song&limit=50&country=us`,
  content type is `text/javascript`, body is JSON), play the first song with
  `enqueue: replace` and add the rest with `enqueue: add`. Song URL format:
  `https://music.apple.com/us/song/track/<trackId>`.
- `apple_music://<kind>/<id>` URIs convert to
  `https://music.apple.com/us/<kind>/<kind>-<id>/<id>`.

Current Music Map (seed data for migration):

| Name | URL | shuffle | repeat |
|---|---|---|---|
| Metallica — Essential | https://music.apple.com/us/playlist/metallica-essentials/pl.e55da0d4a18140e3b1120339e2608aa5 | true | all |
| Disturbed — Divisible | https://music.apple.com/us/album/divisive/1646032515 | | |
| Creedence Clearwater Revival — Essential | https://music.apple.com/us/playlist/creedence-clearwater-revival-essentials/pl.a64e22c649a64385b037a8f1a1559cb1 | true | all |
| Queen — Bohemian Rhapsody (The Original Soundtrack) | https://music.apple.com/us/album/bohemian-rhapsody-the-original-soundtrack/6781027361 | | |
| Volodymyr Ivasiuk | https://music.apple.com/us/artist/%D0%B2%D0%BE%D0%BB%D0%BE%D0%B4%D0%B8%D0%BC%D0%B8%D1%80-%D1%96%D0%B2%D0%B0%D1%81%D1%8E%D0%BA/1797008889 | true | all |
| Ray Charles — The Ultimate Collection | https://music.apple.com/us/album/the-atlantic-hits/1544753335 | true | all |
| Kenny Rogers — 21 Number Ones | https://music.apple.com/us/playlist/kenny-rogers-essentials/pl.97c239b65c7e4e3eb786f27c74790b8b | true | all |

## 2. Repository layout

```
.
├── cmd/nfc-player/main.go        wiring only: config, storage, HA client, players, web
├── internal/
│   ├── model/                    shared types and interfaces (section 5)
│   ├── deps/                     blank imports that pin go.mod dependencies
│   ├── config/                   runtime config (env + /data/options.json)
│   ├── store/                    YAML file storage with atomic writes + mutex
│   ├── registry/                 tag entries: model, validation, CRUD on store
│   ├── ha/                       HA websocket client (auth, events, services, registries)
│   ├── rooms/                    area → player entity resolution, per-area volume
│   ├── catalog/                  music URL parsing, Apple/iTunes lookups, artwork URLs
│   ├── player/                   Player interface, router, fallback chain
│   │   ├── sonos/                native Sonos via share links
│   │   └── musicassistant/       Music Assistant via music_assistant.* services
│   ├── scan/                     tag_scanned handler: event → entry → room → router
│   ├── artwork/                  image fetch + disk cache + square crop
│   ├── labels/                   label engine
│   │   ├── template.go           Template model + validation + built-ins (embedded YAML)
│   │   ├── background.go         Background model + built-ins
│   │   ├── layout.go             Layout interface + registry ("full", "side")
│   │   ├── layouts/              one file per layout
│   │   ├── sheet.go              slot math: start row/col, skip, copies, bleed rules
│   │   └── pdf.go                PDF writer wrapper, fonts
│   └── web/                      HTTP server, JSON API, Ingress handling
│       └── ui/                   embedded static UI (HTML, CSS, vanilla JS)
├── assets/fonts/                 embedded OFL fonts with Cyrillic (Noto Sans Regular/Bold)
├── nfc_player/                   the HA app definition
│   ├── config.yaml
│   ├── DOCS.md
│   ├── CHANGELOG.md
│   ├── icon.png / logo.png
│   └── translations/en.yaml
├── repository.yaml               HA app repository manifest
├── Dockerfile                    multi-stage, cross-compiles, FROM scratch
├── .github/workflows/
│   ├── ci.yaml                   vet, test, build on PRs and main
│   └── release.yaml              multi-arch image to GHCR on tag v*
├── Makefile                      build, test, lint, run-dev, docker
└── docs/PLAN.md                  this file
```

Module path: `github.com/igrybkov/ha-nfc-player`. Go 1.27.

Dependencies (keep this list short, standard library first):

- `github.com/coder/websocket`: HA websocket.
- `go.yaml.in/yaml/v3`: YAML storage.
- A PDF library with TTF embedding and subsetting, JPEG/PNG images, and
  fill/rect drawing. First choice `github.com/signintech/gopdf`. Check it is
  still maintained before use. Hide it behind `labels/pdf.go`.
- `golang.org/x/image/draw`: resampling for art.

No web framework. `net/http` with Go 1.22+ routing patterns.

## 3. Runtime and storage

The app runs under Supervisor with:

- `homeassistant_api: true`. Websocket at `ws://supervisor/core/websocket`,
  auth with env `SUPERVISOR_TOKEN`.
- `ingress: true`, `ingress_port: 8099`, `panel_icon: mdi:nfc-variant`,
  `panel_title: NFC Player`.
- `map: [homeassistant_config:rw]`. HA config appears at `/homeassistant`.
- Persistent data at `/data`.

Dev mode (outside Supervisor): env `HA_URL` (e.g. `https://hass.example`) and
`HA_TOKEN`, `DATA_DIR`, `CONFIG_DIR`, `LISTEN` (default `:8099`).

Files:

| Path | Content | Why there |
|---|---|---|
| `/homeassistant/nfc_player/tags.yaml` | tag registry | user data, tracked by the GitOps repo |
| `/homeassistant/nfc_player/settings.yaml` | rooms, volumes, players, print offsets | same |
| `/homeassistant/nfc_player/templates.yaml` | user label templates | same |
| `/homeassistant/nfc_player/backgrounds.yaml` | user backgrounds | same |
| `/data/cache/art/` | downloaded art, keyed by sha256 of URL | cache, in HA backups, not in git |
| `/data/cache/artwork.json` | entry key → resolved art URL | same |
| `/data/options.json` | app options from Supervisor UI | standard |

The config dir path is configurable (`config_dir` option) so it can be moved.
All YAML writes are atomic (write temp, fsync, rename) under a process mutex.
Files are re-read when their mtime changes, so a `git pull` on HA is picked up
without a restart.

App options (`/data/options.json`), kept minimal: `log_level`, `config_dir`.
Everything else lives in `settings.yaml` and is edited in the UI.

## 4. Data model

### Tag entry (`tags.yaml`)

```yaml
version: 1
tags:
  - id: metallica-essential          # stable slug, generated from name
    name: Metallica — Essential      # matched against HA tag name
    tag_ids: []                      # optional HA tag IDs; match by ID wins over name
    url: https://music.apple.com/us/playlist/metallica-essentials/pl.e55da0d4a18140e3b1120339e2608aa5
    shuffle: true
    repeat: all                      # off | one | all
    volume: null                     # percent override, null = room default
    player: null                     # player name override, null = settings default chain
    label:
      title: null                    # override; default from name split on "—"
      subtitle: null
      artwork: null                  # URL or path under config_dir; null = auto
```

Matching order on scan: `tag_ids` contains event `tag_id` → normalized name
equal (cut at `|`, trim, case-insensitive) → `Artist — Album` search fallback.

### Settings (`settings.yaml`)

```yaml
version: 1
readers:
  integrations: [esphome]            # only scans from devices of these integrations
players:
  default: [sonos, music_assistant]  # try in order; first that supports the media and succeeds
  fallback: true                     # on error, try the next player in the chain
rooms:
  default_volume: 15
  areas:
    family_room: {volume: 30, sonos_entity: null, ma_entity: null}
    office: {volume: 15}
    master_bathroom: {volume: 25}
    master_bedroom: {volume: 25}
  sonos_model_preference: [Arc, Five, One, Port]
music_assistant:
  config_entry_id: null              # auto-detect if null
print:
  offsets:                           # mm, per template id
    avery-5260: {x: 0, y: -0.75}
  inset_mm: 1.5
  bleed_mm: 1.5
  edge_bleed_mm: 0.4
```

Area keys are HA `area_id`s.

### Label template (`templates.yaml` + built-ins)

```yaml
- id: avery-5260
  name: Avery 5260 (address, 30 per sheet)
  builtin: true
  page: {width_in: 8.5, height_in: 11}
  label: {width_in: 2.625, height_in: 1}
  grid: {cols: 3, rows: 10}
  margin: {left_in: 0.1875, top_in: 0.5}
  gap: {col_in: 0.125, row_in: 0}
  layout: side
  font: {title_pt: 11, subtitle_pt: 8.5}
- id: avery-5434
  name: Avery 5434 (4×6, 10 per sheet)
  builtin: true
  page: {width_in: 4, height_in: 6}
  label: {width_in: 1.5, height_in: 1}
  grid: {cols: 2, rows: 5}
  margin: {left_in: 0.46875, top_in: 0.5}
  gap: {col_in: 0.0625, row_in: 0}
  layout: full
  font: {title_pt: 8, subtitle_pt: 6.5}
```

Built-ins are embedded in the binary and read-only. The UI can duplicate a
built-in and edit the copy. Validation: grid must fit the page, all sizes > 0,
`layout` must be a registered layout, ids unique.

### Background (`backgrounds.yaml` + built-ins)

```yaml
- id: white
  name: White
  builtin: true
  background: "#FFFFFF"
  text: "#000000"
  fade: null           # full layout: gradient color under text; null = background
- id: black
  name: Black
  builtin: true
  background: "#000000"
  text: "#FFFFFF"
```

Bleed applies when `background` is not white (see section 7).

## 5. Module contracts

These signatures are the contract between work tracks. Change them only by
updating this file.

Phase 0 writes every type and interface in this section in full (fields,
constants, method sets), so all tracks compile against the same definitions.
Only function bodies are stubs.

### Shared types (package `model`, owned by phase 0, read-only for tracks)

```go
package model

type Repeat string // "off" | "one" | "all"

type Entry struct {               // one tag entry, see section 4
    ID, Name string
    TagIDs   []string
    URL      string
    Shuffle  bool
    Repeat   Repeat
    Volume   *int
    Player   *string
    Label    LabelOverrides
}
type LabelOverrides struct{ Title, Subtitle, Artwork *string }

type Settings struct { /* every field from section 4 settings.yaml, with yaml tags */ }
type AreaSettings struct{ Volume *int; SonosEntity, MAEntity *string }
type Offset struct{ XMM, YMM float64 }

type ScanResult string // "played" | "no_match" | "ignored" | "error"
type Scan struct {
    Time     time.Time
    TagID    string
    TagName  string
    DeviceID string
    AreaID   string
    EntryID  string     // empty when no match
    Player   string     // player that handled it
    Result   ScanResult
    Error    string
}

// Read side of the registry, used by scan. Implemented by registry (track C).
type EntryLookup interface {
    ByTagID(tagID string) (Entry, bool)
    ByName(name string) (Entry, bool)   // name already normalized by caller
    All() []Entry
}
// Settings source, implemented by store/config (track C), read by A and B.
type SettingsSource interface{ Settings() Settings }
// Scan history: scan (track A) writes, web (track C) reads. Implemented in
// scan as an in-memory ring buffer of 50.
type ScanLog interface {
    Add(Scan)
    Recent() []Scan
}
// Used by web for test play and the areas screen. Implemented by scan (track A).
type PlayService interface {
    PlayEntry(ctx context.Context, entry Entry, areaID string) (player string, err error)
    Areas(ctx context.Context) ([]AreaInfo, error)
}
type AreaInfo struct{ ID, Name, SonosEntity, MAEntity string; Volume int }
// Used by labels and web. Implemented by artwork (track B).
type ArtworkSource interface {
    Artwork(ctx context.Context, e Entry, refresh bool) (image.Image, error) // square, cached
}
```

### ha

```go
type Client interface {
    SubscribeEvents(ctx context.Context, eventType string, fn func(Event)) error
    CallService(ctx context.Context, domain, service string, data map[string]any, target *Target, returnResponse bool) (json.RawMessage, error)
    Areas(ctx context.Context) ([]Area, error)
    Devices(ctx context.Context) ([]Device, error)
    Entities(ctx context.Context) ([]Entity, error)
    States(ctx context.Context) ([]State, error)
    Tags(ctx context.Context) ([]Tag, error) // websocket "tag/list"
}
```

Implementation: one websocket connection, request ids, reconnect with backoff,
re-subscribe after reconnect, registries cached and refreshed on
`*_registry_updated` events. A `Fake` implementation lives in `ha/hatest`
for tests.

### catalog

```go
type Kind string // album, playlist, song, artist, station, unknown
type Media struct {
    URL     string
    Kind    Kind
    ID      string
    Country string
}
func Parse(raw string) (Media, error)         // share links and apple_music:// URIs
func (m Media) ShareURL() string
type Apple interface {
    ArtistSongs(ctx context.Context, artistID string, limit int) ([]Song, error)
}
```

Artwork resolution is not in `catalog`. It lives in `artwork` (track B) and
may call `catalog.Parse`.

### player

```go
type Request struct {
    Media   catalog.Media
    Query   *Query   // set for "Artist — Album" search fallback; Media is empty then
    Area    rooms.Area
    Volume  int      // percent
    Shuffle bool
    Repeat  string   // off | one | all
}
type Player interface {
    Name() string                              // "sonos", "music_assistant"
    Supports(req Request) bool
    Play(ctx context.Context, req Request) error
}
type Router struct{ /* ordered players, fallback flag */ }
func (r *Router) Play(ctx context.Context, req Request, override string) error
```

Adding a player means one new package that implements `Player` and one line in
`main.go` to register it.

Sonos player: entity from `rooms` (area override, else model preference).
Supports album, playlist, song, artist (artist via iTunes song queue).
Sequence as in section 1.

Music Assistant player: MA `media_player` entity in the same area (area
override, else the entity of the `music_assistant` integration in that area).
Supports all kinds, plus `Query`. Uses `music_assistant.play_media` with
`media_id` (share URL or `apple_music://` URI), `media_type`, `enqueue:
replace`, then `shuffle_set`/`repeat_set`/`volume_set` on the MA entity.
`Query` uses `music_assistant.search` with `return_response`.

The router resolves a `Query` before handing it to a player that can't search:
it asks the MA player to search, then passes the resulting share URL on.

### labels

```go
type Layout interface {
    ID() string                // "side", "full"
    Name() string
    Draw(c Canvas, cell Rect, item Item, bg Background, t Template) error
}
func RegisterLayout(l Layout)
type Item struct {
    Title, Subtitle string
    Art     image.Image // already cropped square; nil = no art
}
type Job struct {
    Template   Template
    Background Background
    Items      []Item
    Copies     int
    StartRow, StartCol int // 1-based
    Skip       int
    Offset     struct{ XMM, YMM float64 }
    InsetMM, BleedMM, EdgeBleedMM float64
    Outline    bool
}
func Render(w io.Writer, job Job) error // writes the PDF
func SheetMap(t Template, job Job) [][]SlotState // used, printed, free; for the UI grid
```

`Canvas` wraps the PDF library: `Rect`, `Image`, `Text`, `TextWidth`,
`SetFill`, gradients if the library supports them, else pre-composite in Go.

### web API (JSON, all paths relative for Ingress)

| Method | Path | Purpose |
|---|---|---|
| GET | `api/tags` | list entries |
| POST | `api/tags` | create |
| PUT | `api/tags/{id}` | update |
| DELETE | `api/tags/{id}` | delete |
| POST | `api/tags/{id}/play` | test play: body `{area_id}` |
| GET | `api/tags/{id}/artwork` | resolved art (cached JPEG) |
| GET | `api/ha/tags` | HA tag registry, for linking tag IDs |
| GET | `api/ha/areas` | areas with resolved Sonos/MA entities |
| GET | `api/scans` | last 50 scans: time, tag, area, matched entry, result |
| GET/PUT | `api/settings` | settings |
| GET/POST | `api/templates` | list / create |
| PUT/DELETE | `api/templates/{id}` | update / delete (user templates only) |
| GET/POST | `api/backgrounds` | list / create |
| PUT/DELETE | `api/backgrounds/{id}` | update / delete (user only) |
| GET | `api/layouts` | registered layouts |
| POST | `api/print/preview` | `{job}` → sheet map JSON |
| POST | `api/print/pdf` | `{job}` → `application/pdf` |
| GET | `healthz` | liveness |

Ingress: the UI uses relative URLs only. Honor `X-Ingress-Path` for any
absolute link. Accept connections only from `172.30.32.2` when running under
Supervisor (Ingress gateway), plus localhost.

## 6. UI

Plain HTML, CSS and vanilla JS modules, embedded with `embed`. No build step.
Follows HA light/dark via `prefers-color-scheme`. Screens:

1. **Tags.** Searchable list with art thumbnails. Fuzzy subsequence search
   (`mess` finds `Metallica — Essential`). Arrow keys move, Enter edits.
   Edit form: name, linked HA tag IDs (picker from `api/ha/tags`), URL,
   shuffle, repeat, volume, player override, label overrides, art preview.
   "Test play" with an area picker.
2. **Unknown scans.** Recent scans that matched nothing, with "Create entry"
   prefilled from the tag name and ID.
3. **Print.** Pick entries (same fuzzy search, multi-select), copies, template,
   background, start slot by clicking the sheet grid (shows used, to print,
   free), skip count, offsets. "Generate PDF" opens the PDF in a new tab.
   "Save offsets" stores them for the template.
4. **Templates.** List built-ins and user templates. Create, duplicate, edit,
   delete user ones. Live outline preview (`api/print/pdf` with `outline`).
5. **Backgrounds.** Same pattern: color pickers for background, text, fade.
6. **Settings.** Areas table (volume, Sonos/MA entity override), player chain
   order, fallback switch, reader integrations, print inset/bleed values.

## 7. Label rendering (port of `bin/nfc-music-labels`)

Port these rules exactly. The Python script is the reference; copy it into
`docs/reference/nfc-music-labels.py` for the track that needs it.

- Units: points. 1 in = 72 pt. 1 mm = 72/25.4 pt.
- Slot origin: `x = margin.left + col*(label.w + gap.col) + offset.x`,
  `y` from top: `margin.top + row*(label.h + gap.row) - offset.y`
  (positive `y` offset moves the print **up**; check against the script and
  keep its sign convention).
- Art resolution 600 dpi. Art drawn inside the label by `inset`.
- `side` layout: square art flush left (inside inset), text block vertically
  centered to the right. Title bold, subtitle regular. Wrap to lines, shrink
  font in steps, ellipsize any line still too wide.
- `full` layout: art fills the label, gradient fade to the background color
  under white or text-colored lines at the bottom. Composite the gradient in
  Go (no alpha bands).
- Non-white backgrounds: fill extends `bleed` past the label left, right and
  top. Downward bleed is full `bleed` only if the label below is printed in
  this job or the label is in the last row. Otherwise `edge_bleed`.
- Title/subtitle split: name split on the first `—` (em dash); left part is
  the artist, right part the album. No dash: whole name is the title.
- Start row/col 1-based. `skip` adds empty slots before the first label.
  Labels flow across columns then down rows, onto new pages as needed.
- Fonts: embedded Noto Sans Regular and Bold (OFL), Cyrillic required.
  Check that `Володимир Івасюк` renders.

Artwork resolution chain (port from the script):
1. Album URL: iTunes lookup by id, replace `/NNNxNNNbb.` with
   `/1200x1200bb.`.
2. Playlist or artist URL: page `og:image`, swap last path segment to
   `1200x1200bb.jpg`.
3. Fallback: iTunes search by name.
4. Manual override in the entry wins over all.

## 8. Packaging and release

`Dockerfile`:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/nfc-player ./cmd/nfc-player
FROM alpine:3 AS certs
RUN apk add --no-cache ca-certificates tzdata
FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=certs /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/nfc-player /nfc-player
ENTRYPOINT ["/nfc-player"]
```

Pin exact base image versions (no floating `alpine:3`) when writing it.

Note: Supervisor apps normally run an s6/bashio base. This one doesn't need
it: options are read from `/data/options.json` directly. Set `init: false`
in `config.yaml` so Supervisor doesn't add its own init.

`nfc_player/config.yaml` (key fields):

```yaml
name: NFC Player
slug: nfc_player
version: "0.1.0"
description: Play music from NFC tags and print labels for them
url: https://github.com/igrybkov/ha-nfc-player
arch: [amd64, aarch64]
image: ghcr.io/igrybkov/ha-nfc-player
init: false
homeassistant_api: true
ingress: true
ingress_port: 8099
panel_icon: mdi:nfc-variant
panel_title: NFC Player
map:
  - type: homeassistant_config
    read_only: false
options:
  log_level: info
  config_dir: /homeassistant/nfc_player
schema:
  log_level: list(debug|info|warn|error)
  config_dir: str
```

`image` is one multi-arch manifest. Supervisor pulls `image:version`, and
Docker picks the matching platform. If Supervisor turns out to need the
`{arch}` form, the release workflow also pushes
`ghcr.io/igrybkov/{arch}-ha-nfc-player` tags (amd64, aarch64) as a fallback.
Decide after the first real install; implement both pushes from the start.

`release.yaml`: on tag `v*`. Check that the tag equals `version` in
`nfc_player/config.yaml`. Set up QEMU is **not** needed (Go cross-compiles).
`docker/setup-buildx-action`, `docker/login-action` to GHCR with
`GITHUB_TOKEN` (`packages: write`), `docker/build-push-action` with
`platforms: linux/amd64,linux/arm64`, tags `:<version>` and `:latest`, OCI
labels, provenance on. Pin actions to full commit SHAs with a version comment.

`ci.yaml`: on push and PR. `go vet`, `go test -race ./...`,
`staticcheck` (pinned), build both arches, `docker build` without push.

Install: in HA, Settings → Apps → App store → Repositories, add
`https://github.com/igrybkov/ha-nfc-player`.

## 9. Testing

- `catalog`: table tests for URL parsing (album, playlist, song, artist with
  percent-encoded Cyrillic slug, `apple_music://`), share URL building.
- `ha`: test against an in-process fake websocket server (auth, request ids,
  events, reconnect).
- `player`: tests with `ha/hatest.Fake`. Assert the exact service call
  sequence for Sonos album, Sonos artist (iTunes via `httptest` server),
  MA album, MA query. Router fallback on error.
- `scan`: event filtering by integration, name normalization (`|` cut), ID
  match beats name match, volume resolution order, unknown scan recorded.
- `labels`: slot math (start, skip, copies, page break), bleed rule
  (below printed / below free / last row), template validation. Render the
  two built-ins to PDF and check the file parses and contains the expected
  number of pages. Render PNG previews for a human check if a rasterizer is
  available (not required in CI).
- `store`: atomic write, reload on mtime change.
- `web`: handler tests with `httptest` for each endpoint group.

Target: `go test -race ./...` green, `go vet` clean, `staticcheck` clean.

## 10. Work tracks

Phase 0 (one agent, sequential): scaffold. `go.mod` with all dependencies
listed above, pinned by `internal/deps/deps.go` with blank imports so
`go mod tidy` never prunes them. Package `internal/model` written in full.
Every other package with its exported types and interfaces from section 5
(bodies `panic("TODO")` or minimal). `cmd/nfc-player/main.go` that compiles,
`Makefile`, `.gitignore` (binaries, `.env*`, `*.pdf` outside testdata, local
data dirs), copy of the Python reference script, fonts. Everything compiles.

Phase 1 (parallel, each owns its directories only):

| Track | Owns | Depends on |
|---|---|---|
| A. HA + playback | `internal/ha`, `internal/rooms`, `internal/catalog`, `internal/player/...`, `internal/scan` (incl. `ScanLog`, `PlayService`) | contracts |
| B. Labels | `internal/labels/...`, `internal/artwork` (incl. `ArtworkSource`), `assets/fonts` | contracts, `catalog.Parse` |
| C. Web + storage | `internal/web/...`, `internal/store`, `internal/registry` (incl. `EntryLookup`), `internal/config` (incl. `SettingsSource`) | contracts |
| D. Packaging | `Dockerfile`, `nfc_player/`, `repository.yaml`, `.github/`, `README.md` | none |

Rules for parallel tracks:

- Edit only files in your own directories. `internal/model` is read-only.
- Never run `go get` or `go mod tidy`, and never edit `go.mod`/`go.sum`.
  Need a dependency or a contract change? Stop and report it.
- Never `git commit`, `git add`, or switch branches. The coordinator commits.
- Never write tokens or secrets into the tree.
- `go build ./...` and `go test ./<your packages>/...` must pass at the end
  (use the phase 0 stubs for packages you don't own).

Phase 2 (one agent): wire `main.go`, migration seed (`tags.yaml` and
`settings.yaml` from section 1, in `examples/`), end-to-end run in dev mode
against a fake HA websocket server (scan event → expected service calls; UI
loads; PDF downloads). Golden geometry check: render the same jobs with
`bin/nfc-music-labels` (in `/Users/illia/Projects/homeassistant`, run with
`--no-map --image NAME=URL` or the map, `-y`) and the Go renderer, rasterize
both with `uv run --with pymupdf`, compare art and bleed rectangles for both
templates, with an offset and a partial sheet starting at row 2 col 2.
Compare boxes, not text (fonts differ). Build the image for both platforms,
measure image size and idle RSS. No live HA access in this phase.

Phase 3: review (separate agent), fix findings, tag `v0.1.0`, release.
The image must be public. The release workflow sets the
`org.opencontainers.image.source` label to the repo URL so the package links
to this public repo. If the first push still lands private, the user flips it
to public once in the package settings (GitHub has no API for this). Verify
an anonymous `docker pull` (logged out) before calling the release done.

## 11. Cutover (done with the user, not by agents)

1. Install the app from the repository. Check the UI loads in the sidebar.
2. Copy seed files to `/homeassistant/nfc_player/`. Check entries show.
3. Disable automation `1709949609238` on live HA. Scan each tag once.
4. After a week with no issues: delete the automation, the
   `play_apple_music_artist` script, the `itunes_lookup` rest_command and
   `bin/nfc-music-labels` from the `homeassistant` repo.
   `play_music_in_a_room` stays: other automations may use it.

## 12. Out of scope for v0.1

- Writing NFC tags from the app.
- Sources other than Apple Music (the model allows them; no code yet).
- Authentication beyond Ingress.
