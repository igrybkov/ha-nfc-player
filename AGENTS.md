# AGENTS.md

This file guides AI coding agents (Claude Code, Codex, Cursor and others) that work in this repository.

NFC Player is a Home Assistant app (add-on). A single static Go binary listens for NFC tag scans over the HA websocket API, plays music in the scanner's room, and serves a web UI (via Ingress) to manage records and print labels to PDF. The add-on manifest lives in `nfc_player/` (`config.yaml`, `DOCS.md`, `CHANGELOG.md`).

## Commands

```sh
make build     # static binary ./nfc-player
make test      # go test -race ./...
make test-ui   # node --test internal/web/ui/*_test.mjs
make vet
make lint      # staticcheck, pinned version run via `go run`
make run-dev   # needs HA_URL and HA_TOKEN in .env; UI at http://localhost:8099
```

Single test: `go test -race ./internal/scan -run TestName`.

CI runs vet, test, lint, test-ui, and a cross-build for linux/amd64 and arm64. Run all four `make` checks before you call a change done.

Test hooks driven by env vars:
- `UPDATE_GOLDEN=1 go test ./internal/migrate -run TestV1ToV2` rewrites `testdata/v1to2.golden.yaml`.
- `PREVIEW_DIR=<dir>` (TestPreview) and `LABELS_PREVIEW_DIR=<dir>` (render tests) in `internal/labels` write PNG/PDF output to look at.
- `GOLDEN_DIR=<dir> go test ./internal/labels -run TestGoldenRender` writes cases for `docs/reference/golden.py`, which compares label geometry against the original Python script. See the docstring in that file.

## Architecture

`cmd/nfc-player/main.go` wires everything in `run()`. The e2e test (`cmd/nfc-player/e2e_test.go`) calls `run()` with a fake HA server from `internal/ha/hatest`. Read `run()` first to see how the packages connect.

Flow of a scan: `ha` (websocket client, reconnects forever) emits `tag_scanned` → `scan` looks up the record in `records` and picks what to play → `rooms` maps the HA area to media players and volume → `player.Router` walks the player chain (`sonos`, then `music_assistant`) with fallback. `scan` also handles the `nfc_player_play` / `nfc_player_stop` events and serves as the play service for the UI.

Shared types and interfaces (`SettingsSource`, `ScanLog`, `PlayService`, `ArtworkSource`) live in `internal/model`. Packages depend on these interfaces, so tests swap in fakes.

Storage:
- User data is plain YAML in `config_dir` (records, settings, templates, themes). Cache goes to `DATA_DIR`.
- `store` does atomic writes, reloads on file change, and migrates in memory on read. Nothing is written until the user saves.
- `migrate` owns the schema version (`migrate.Current`). To change a file format, add a step that works on YAML nodes only (never app types), bump `Current`, and add `testdata/v<N>/`. Steps stay forever. File names in `Files` are never removed. See the package doc.
- `config` merges user templates and themes with built-ins from `internal/labels/builtin/*.yaml`.

Labels (`internal/labels`): the geometry is a port of `docs/reference/nfc-music-labels.py`, tuned against real paper. Units are points, origin top-left, Y down. PDF and PNG preview both draw through `drawSlot`, so the preview matches print. A template is the paper (with a format: small, large, dot). A theme is colors plus art placement per format.

Artwork (`internal/artwork`): a resolution chain (override → cached index → Spotify oEmbed → iTunes lookup / og:image → iTunes search), also ported from the Python script. It has an SSRF guard: only allowed hosts for chain fetches, and every dial refuses private, loopback and similar IPs, checked on the dialed IP. Keep that guard intact when touching HTTP code here.

Web (`internal/web`): JSON API (`api_*.go`) plus an embedded vanilla-JS UI in `internal/web/ui/`. Responses send a strict CSP (`default-src 'self'`). No inline `<script>` and no `on…=` attributes; put all UI code in the `.js` modules. Under Supervisor the server accepts only the Ingress gateway. Outside it, `LISTEN` defaults to loopback.

## Extending

- New player: implement `player.Player` (`Name`, `Supports`, `Play`) in a package under `internal/player/`, then pass it to `player.NewRouter` in `main.go`.
- New art placement: add an `Art` value in `internal/labels/theme.go` and a case in `drawLabel` (`draw.go`).
- Built-in template or theme: edit `internal/labels/builtin/templates.yaml` or `themes.yaml`. Sample covers for previews come from `assets/samples/gen.go`.

## Release

Set `version` in `nfc_player/config.yaml`, add a `## X.Y.Z` section to `nfc_player/CHANGELOG.md`, and push to `main`. Do not push tags. The release workflow builds and pushes the image, then creates the tag and the GitHub release. A dependency update merged by Renovate does not release anything until the version is bumped.

## Conventions

- Comments and docs use short, plain sentences (Simplified Technical English style). Match that tone.
- `internal/deps` blank-imports module deps so `go mod tidy` keeps them. Remove an import there once real code uses that dependency.
