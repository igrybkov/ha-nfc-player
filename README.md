# NFC Player for Home Assistant

A Home Assistant app that plays music when you scan an NFC tag. It keeps a
registry of your tags and prints labels for them.

It is a single static Go binary in a small `FROM scratch` image. It
runs on `amd64` and `aarch64`.

## Features

- Scan a tag, and its album, playlist, song or artist plays in the room of
  the tag reader.
- Native Sonos playback from Apple Music links. Music Assistant is the
  fallback.
- Per-room volume and per-tag shuffle, repeat and volume.
- Tag registry in a web UI in the Home Assistant sidebar (Ingress).
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

See [nfc_player/DOCS.md](nfc_player/DOCS.md) for options and files.

## Develop

You need Go (see `go.mod`) and Docker with buildx.

```sh
make build     # static binary ./nfc-player
make test      # go test -race ./...
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

`make docker` needs a buildx builder that supports multi-platform builds
(the `docker-container` driver, or the containerd image store).

## Extend

**Add a player.** Write a type that implements `player.Player` (`Name`,
`Supports`, `Play`) in a new package under `internal/player/`. Pass it to
`player.NewRouter` in `cmd/nfc-player/main.go`. Its name can then be used in
`settings.yaml` (`players.default`) and per tag.

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

The release workflow checks that the tag matches `version`. Then it pushes:

- `ghcr.io/igrybkov/ha-nfc-player:X.Y.Z` and `:latest` (multi-arch). The app
  uses this one.
- `ghcr.io/igrybkov/amd64-ha-nfc-player:X.Y.Z` and
  `ghcr.io/igrybkov/aarch64-ha-nfc-player:X.Y.Z`, a fallback in case
  Supervisor needs the `{arch}` image form.

It also creates a GitHub release with the notes from the changelog.

**First release only.** GHCR may create the packages as private. Open each
package on GitHub (Packages → package → Package settings → Change
visibility) and make it public. There are three packages: `ha-nfc-player`,
`amd64-ha-nfc-player` and `aarch64-ha-nfc-player`. Check with a logged-out
`docker pull ghcr.io/igrybkov/ha-nfc-player:X.Y.Z`.

## License

MIT. See [LICENSE](LICENSE). The bundled Noto Sans fonts are under the SIL
Open Font License (`assets/fonts/OFL.txt`).
