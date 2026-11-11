# NFC Player for Home Assistant

A Home Assistant app that plays music when you scan an NFC tag. It keeps a
registry of your tags and prints labels for them.

It is a single static Go binary in a small `FROM scratch` image. It
runs on `amd64` and `aarch64`.

## Features

- Scan a tag, and its album, playlist, song, artist or station plays in the room of
  the tag reader.
- Native Sonos playback from Apple Music links. Music Assistant is the
  fallback.
- Spotify links too: Sonos plays albums, playlists and tracks; Music
  Assistant plays artists.
- Per-room volume, and per-record shuffle, repeat and volume. Albums play
  in order by default; artists and playlists shuffle.
- Records: a tag name plays one album, or a random pick from a list that
  can hold other lists.
- Play and stop from any automation with the `nfc_player_play` and
  `nfc_player_stop` events.
- Records and all Home Assistant tags in a web UI in the sidebar
  (Ingress): rename and delete tags, see what each one plays.
- Recent scans list, to see what matched and what failed.
- Label printing to PDF. Layouts, templates and backgrounds are pluggable.
  You can add templates and backgrounds from the UI.
- Data lives as plain YAML in your Home Assistant config folder, so it is
  in your backups and in git.

## Install

1. In Home Assistant, go to **Settings → Apps → App store**.
2. Open the menu (top right), then **Repositories**.
3. Add `https://github.com/igrybkov/ha-nfc-player`.
4. Find **NFC Player** in the store and install it.
5. Start it and open **NFC Player** in the sidebar.

See [nfc_player/DOCS.md](nfc_player/DOCS.md) for options, files, and the
play events with automation examples.

## Develop

You need Go (see `go.mod`) and Docker with buildx.

```sh
make build     # static binary ./nfc-player
make test      # go test -race ./...
make test-ui   # node --test for the UI helpers
make vet       # go vet ./...
make lint      # staticcheck
make docker    # multi-arch image build (no push)
make run-dev   # run locally against a real Home Assistant
```

`make run-dev` talks to Home Assistant over its websocket API. Put these in
`.env` (git ignores it):

```sh
HA_URL=https://homeassistant.local:8123
HA_TOKEN=<long-lived access token>
```

Data goes to `./data` and config files to `./dev-config`. The UI is at
<http://localhost:8099>. Set `LISTEN` to change the address.

The UI has no login. Outside Supervisor, `LISTEN` defaults to
`127.0.0.1:8099`, so only your own machine can reach it. Under Supervisor
it is `:8099`, and the app only accepts the Ingress gateway. In a plain
`docker run`, a loopback address is unreachable from the host: set
`-e LISTEN=:8099` and publish the port to the interface you trust, e.g.
`-p 127.0.0.1:8099:8099`.

UI responses carry a Content-Security-Policy (`default-src 'self'`, no
inline scripts, `frame-ancestors 'self'`). Keep new UI code in the `.js`
modules: inline `<script>` and `on…=` attributes will not run.

`make docker` needs a buildx builder that supports multi-platform builds
(the `docker-container` driver, or the containerd image store). Pick one
with `make docker BUILDER=<name>`.

`nfc-player -healthcheck` exits 0 when the local server answers `/healthz`.
The image uses it as its Docker health check.

Seed files for the migration from the old automation are in `examples/`.

**Change the file format.** Files in `config_dir` carry a schema version.
Add a step to `internal/migrate` that takes the current version to the
next, bump `migrate.Current`, and add fixtures under
`internal/migrate/testdata/v<N>/`. Steps work on YAML nodes, never on app
types, and stay forever: files of any older version load. See the package
doc.

## Extend

**Add a player.** Write a type that implements `player.Player` (`Name`,
`Supports`, `Play`) in a new package under `internal/player/`. Pass it to
`player.NewRouter` in `cmd/nfc-player/main.go`. Its name can then be used in
`settings.yaml` (`players.default`) and per record.

**Add a label layout.** Add a file in `internal/labels/layouts/` with a type
that implements `labels.Layout` (`ID`, `Name`, `Draw`). Register it in
`init()` with `labels.RegisterLayout`. It shows up in `api/layouts` and the
UI.

**Add a template.** Add one on the templates page of the UI. It is saved to
`templates.yaml`. To ship a built-in one, add it to
`internal/labels/builtin/templates.yaml`.

**Add a background.** Same as templates: the UI saves to `backgrounds.yaml`.
Built-ins live in `internal/labels/builtin/backgrounds.yaml`.

## Release

1. Set `version` in `nfc_player/config.yaml` and add a section for it in
   `nfc_player/CHANGELOG.md` (`## X.Y.Z`).
2. Commit and push to `main`. Wait for CI to pass.
3. Tag and push: `git tag vX.Y.Z && git push origin vX.Y.Z`.

The release workflow checks that the tag matches `version`. Then it pushes
one multi-arch image (`linux/amd64`, `linux/arm64`):
`ghcr.io/igrybkov/ha-nfc-player:X.Y.Z` and `:latest`.

It also creates a GitHub release with the notes from the changelog.

**First release only.** GHCR may create the package as private. Open it on
GitHub (Packages → ha-nfc-player → Package settings → Change visibility)
and make it public. Check with a logged-out
`docker pull ghcr.io/igrybkov/ha-nfc-player:X.Y.Z`.

## License

MIT. See [LICENSE](LICENSE). The bundled Noto Sans fonts are under the SIL
Open Font License (`assets/fonts/OFL.txt`).
