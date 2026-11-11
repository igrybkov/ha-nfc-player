# NFC Player

Scan an NFC tag and the music linked to it plays in the room where you
scanned it. The app also keeps a list of your tags and prints labels for
them.

## How it works

1. Home Assistant fires a `tag_scanned` event when a tag reader sees a tag.
   Only readers of the integrations in **Settings → Readers** count
   (ESPHome by default).
2. The app finds the tag in its registry. It matches by linked tag ID first,
   then by tag name. The name is cut at the first `|` and compared without
   case, so `Metallica — Essential | v2` finds `Metallica — Essential`.
3. A tag with no entry whose name matches a pool's name or id plays that
   pool. The name is matched the same way, so `Kitchen | fridge card` plays
   the Kitchen pool, and `All tags` plays a random tag.
4. A name like `Artist — Album` with no entry is searched in Music Assistant,
   and the first album or playlist plays. Apple Music hits come before
   Spotify hits.
5. It finds the speaker in the reader's area and plays the music there.
   Sonos speakers play natively. Music Assistant is the fallback.
   Apple Music stations (`music.apple.com/.../station/...`) play on Sonos
   only: Music Assistant can't play them.
6. Volume: the tag's volume, else the area's, else the default volume.

## Setup

1. Install the app and start it.
2. Open **NFC Player** in the sidebar.
3. Add a tag: **Tags → New tag**. Use the tag's name as Home Assistant
   knows it and paste an Apple Music or Spotify link. You can also link tag IDs from
   the list of tags Home Assistant knows.
4. Open **Settings** in the app to set area volumes, players and readers.

Coming from the old "Play Music when NFC tag is scanned" automation? Copy
`examples/tags.yaml` and `examples/settings.yaml` from the repository into
`config_dir`. They hold the old Music Map and room volumes. You can copy them
before or after the first start: the app writes nothing to `config_dir` until
you save something, and it picks up a new file on its next read.

## Spotify

A tag, pool item or play event can use a Spotify link:
`https://open.spotify.com/album/...`, `/playlist/...`, `/track/...` or
`/artist/...`. A share link with `?si=...` or `/intl-xx/` works as is.
`spotify:album:...` and `spotify://album/...` URIs work too.

- Sonos plays Spotify albums, playlists and tracks natively.
- Sonos can't play a Spotify artist link, so artists go to Music Assistant.
- Link Spotify in the Sonos app for the native path. Add the Spotify
  provider in Music Assistant for artists and as the fallback.
- Cover art comes from Spotify.

## Options

| Option | Default | What it does |
|---|---|---|
| `log_level` | `info` | `debug`, `info`, `warn` or `error`. |
| `config_dir` | `/homeassistant/nfc_player` | Where the YAML files live. |

## Files

The app keeps your data as plain YAML under `config_dir`:

- `tags.yaml`: the tag registry.
- `pools.yaml`: pools of music (see below).
- `settings.yaml`: rooms, volumes, players, print offsets.
- `templates.yaml`: your own label templates.
- `backgrounds.yaml`: your own label backgrounds.

A file that does not exist reads as the defaults. The app creates it the
first time you save on that screen, so an untouched `config_dir` stays empty
and git sees no new files. You can edit these files by hand or with git. The
app reloads a file when it changes on disk.

Downloaded cover art is cached in the app's `/data` folder.

The UI opens only through Home Assistant (Ingress). Its pages set a strict
Content-Security-Policy and can only be framed by Home Assistant itself.

## Screens

- **Tags**: the registry. Search, edit, link tag IDs, preview art and test
  play in an area.
- **Pools**: lists of music to pick from at random, and a play box (see
  below).
- **Unknown scans**: recent scans that matched nothing, with **Create entry**.
  **Rename** sets the tag's name in Home Assistant, so a new tag gets a
  real name before you create its entry.
  **All recent scans** below it shows every scan, event and UI play with
  its result and error.
- **Print**: make a label PDF (see below).
- **Templates** and **Backgrounds**: the built-ins are read-only. Duplicate
  one to make your own. **Make default** on a background makes the Print
  screen start with it (white until you pick one).
- **Settings**: default volume (1 to 100), per-area volume (0 to 100), Sonos
  and Music Assistant entity per area, player order and fallback, reader
  integrations, print inset and bleed (outer bleed adds fill on the
  paper-edge side of the first and last columns).

## Pools and play events

A pool is a named list of music. Each play picks one item at random, and
never the same item twice in a row. Two Home Assistant events start and stop
music through the app, so a button, a switch or a dashboard tile can play a
pool, a tag or any album in one `event` action.

### Pools

Edit pools on the **Pools** tab, or by hand in `pools.yaml`:

```yaml
version: 1
pools:
  - id: kitchen
    name: Kitchen
    shuffle: true      # null = the item decides
    repeat: all        # null = the item decides
    volume: null       # null = the item decides, then the area volume
    player: null       # null = the item decides, then the player order
    items:
      - tag: kenny-rogers-21-number-ones                 # a tag id
      - media: Cat Stevens — Teaser and the Firecat     # any media text
      - media: https://music.apple.com/us/artist/the-dead-south/668262442
```

- `id` is lowercase letters, digits and `-`. Left empty, it comes from the
  name. `all` is taken.
- Each item has a `tag` (the id of a tag that plays a URL) or a `media`.
  Media is a music link, an `Artist — Album` name, or a name that Music
  Assistant searches for.
- The pool `all` is built in. It holds every tag that plays a URL.
- Settings, strongest first: the event, the pool, the tag, the defaults
  (no shuffle, repeat all, area volume, player order).

The **Pools** tab also has a play box. Pick a pool, a tag or type any media,
pick an area, and press **Play** or **Stop**. It works the same way as the
events below.

### Pool tags

A tag can play a pool instead of a URL. In the tag editor, set **Plays** to
**Pool** and pick the pool (or **All tags**). Each scan then plays a random
item. The tag's volume and player beat the pool's. Shuffle and repeat come
from the pool and the item. The label art is the art of the pool's first
item that has a link.

You can also skip the editor: name the tag in HA after the pool (see
**How it works** above). A tag entry with the same name wins over the
pool.

A pool tag that names a pool you deleted fails when you scan it. The error
shows in **All recent scans**.

### `nfc_player_play`

Plays music. Give a target (`area` or `player`) and one of `media`, `tag` or
`pool`. When more than one is set, the first non-empty one in that order
wins, so an empty template result falls through to the next.

| Key | Value | What it does |
|---|---|---|
| `area` | area id | Play in this area, on its speaker, as for a tag scan. |
| `player` | entity id | Play on this `media_player` only. It must be a Sonos or Music Assistant player. |
| `media` | text | A music link, `Artist — Album`, or a name to search. |
| `tag` | text | A tag id or tag name. |
| `pool` | text | A pool id, or `all`. |
| `volume` | 0 to 100 | Volume in percent. |
| `shuffle` | true / false | |
| `repeat` | `off` / `one` / `all` | |
| `radio` | true / false | Music Assistant radio mode: keep playing similar music. Sonos can't, so Music Assistant plays it. It must be in the player order in **Settings**. |
| `toggle` | true / false | If the target is playing, stop it and do nothing else. |

Values may come from templates. `"30"`, `30` and `30.0` all mean 30.
`true`, `"on"`, `"yes"` and `1` mean true. An empty string means "not set".
A bad value makes the event fail with an error in **All recent scans**.

### `nfc_player_stop`

Stops what plays on the target.

| Key | Value | What it does |
|---|---|---|
| `area` | area id | Stop the speakers in this area. |
| `player` | entity id | Stop this `media_player`. |

Every event shows in **All recent scans**, with the source `event`. Play
and stop from the UI show with the source `ui`.

### Examples

A button hold toggles the kitchen pool. A script may return music to play
instead. The script returns an empty `media` to keep the pool:

```yaml
# scripts.yaml
kitchen_music_override:
  alias: Kitchen music override
  sequence:
    - variables:
        result:
          media: "{{ 'Christmas Classics' if now().month == 12 else '' }}"
    - stop: Done
      response_variable: result

# automations.yaml
- alias: Kitchen button hold plays music
  triggers:
    - trigger: state
      entity_id: event.kitchen_button_action
      # A reconnect restores the last event; don't treat it as a press.
      not_from: [unavailable, unknown]
      not_to: unavailable
  conditions:
    - condition: state
      entity_id: event.kitchen_button_action
      attribute: event_type
      state: long_press
  actions:
    - action: script.kitchen_music_override
      response_variable: override
    - event: nfc_player_play
      event_data:
        area: kitchen
        media: "{{ override.media | default('') }}"
        pool: kitchen
        toggle: true
```

Start a sound when a switch turns on and stop it when the switch turns off,
on one speaker:

```yaml
- alias: Fireplace sound
  triggers:
    - trigger: state
      entity_id: switch.fireplace
      to: "on"
      id: "on"
    - trigger: state
      entity_id: switch.fireplace
      to: "off"
  actions:
    - if:
        - condition: trigger
          id: "on"
      then:
        - event: nfc_player_play
          event_data:
            player: media_player.living_room
            media: https://music.apple.com/us/album/fireplace-sounds/1234567890 # your album link
            repeat: one
            volume: 20
      else:
        - event: nfc_player_stop
          event_data:
            player: media_player.living_room
```

A script that plays music in a room in radio mode. Call it from other
automations or from the UI:

```yaml
play_music_in_a_room:
  alias: Play music in a room
  mode: parallel
  fields:
    area:
      selector:
        area: {}
    media:
      selector:
        text: {}
  sequence:
    - event: nfc_player_play
      event_data:
        area: "{{ area }}"
        media: "{{ media }}"
        radio: true
```

A dashboard button. A dashboard can't fire an event, so a script fires it
and a tile runs the script:

```yaml
# scripts.yaml
kitchen_music:
  alias: Kitchen music
  icon: mdi:music
  sequence:
    - event: nfc_player_play
      event_data:
        area: kitchen
        pool: kitchen
        toggle: true
```

```yaml
# dashboard card
type: tile
entity: script.kitchen_music
name: Kitchen music
tap_action:
  action: perform-action
  perform_action: script.kitchen_music
```

## Labels

On **Print**, pick tags, copies, a template and a background. Click a slot
on the sheet to start there; used slots stay empty. **Generate PDF** opens
the PDF; print it at 100% (no scaling).

If a picked tag has no cover art, a warning under the sheet names it before
you print. Those labels print without art. Open the tag to set an artwork
URL or file, or press **Refresh art** to look it up again.

Art lookups only go to Apple (Apple Music pages, the iTunes API and its
image servers) and Spotify (its oEmbed API and image servers). An artwork URL you set yourself may point at any public
site, but never at an address on your local network.

If your printer shifts the page, set the offset in mm and press **Save
offsets**. It is saved per template. Positive X moves the print right,
positive Y moves it up.

## Support

Open an issue at <https://github.com/igrybkov/ha-nfc-player/issues>.
