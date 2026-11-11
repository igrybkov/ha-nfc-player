# NFC Player

Scan an NFC tag and the music linked to it plays in the room where you
scanned it. The app keeps a list of records, which says what each tag name
plays, and prints labels for them.

## How it works

1. Home Assistant fires a `tag_scanned` event when a tag reader sees a tag.
   Only readers of the integrations in **Settings → Readers** count
   (ESPHome by default).
2. The app finds the record with the tag's name. The name is cut at the
   first `|` and compared without case, so `Metallica — Essential | v2`
   finds `Metallica — Essential`. A record's id matches too.
3. A record holds one or more items. A scan plays one of them (see
   **Records** below).
4. A name like `Artist — Album` with no record is searched in Music
   Assistant, and the first album or playlist plays. Apple Music hits come
   before Spotify hits.
5. It finds the speaker in the reader's area and plays the music there.
   Sonos speakers play natively. Music Assistant is the fallback.
   Apple Music stations (`music.apple.com/.../station/...`) play on Sonos
   only: Music Assistant can't play them.
6. Volume: the area's volume (else the default volume), plus the record's
   volume offset.

## Setup

1. Install the app and start it.
2. Open **NFC Player** in the sidebar.
3. Scan a new tag, then open **Tags**. Rename the tag if its name is a
   placeholder, and press **Create record**. Add an Apple Music or Spotify
   link, and save.
4. Open **Settings** in the app to set area volumes, players and readers.

Coming from the old "Play Music when NFC tag is scanned" automation? Copy
`examples/records.yaml` and `examples/settings.yaml` from the repository
into `config_dir`. They hold the old Music Map and room volumes. You can
copy them before or after the first start: the app writes nothing to
`config_dir` until you save something, and it picks up a new file on its
next read.

## Spotify

A record item or play event can use a Spotify link:
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

- `records.yaml`: what each tag name plays.
- `settings.yaml`: rooms, volumes, players, print offsets.
- `templates.yaml`: your own label templates.
- `backgrounds.yaml`: your own label backgrounds.

A file that does not exist reads as the defaults. The app creates it the
first time you save on that screen, so an untouched `config_dir` stays empty
and git sees no new files. You can edit these files by hand or with git. The
app reloads the files when one changes on disk.

Each file holds a `version`. When a new app version changes the format, it
reads files of every older version and converts them in memory. It writes
nothing until you save something. That first save writes every file in the
new format and deletes files the new format no longer uses. Your git
history keeps the old files.

- Version 2 (app 0.4.0) merged `tags.yaml` and `pools.yaml` into
  `records.yaml`.
- Version 3 (app 0.4.1) turned a record's `volume` into `volume_offset`.
  An old volume becomes null (no offset), since it can't know the room;
  the log names each record that had one. It also dropped "all other
  records" items.
- Version 4 (app 0.4.2) added item names. Nothing converts; an app of
  version 3 can't write these files, so it can't drop the names.

While a file does not parse, or comes from a newer app version, the app
keeps using what it read last and refuses to save, so it never overwrites
your edit. The error shows in the log.

Downloaded cover art is cached in the app's `/data` folder.

The UI opens only through Home Assistant (Ingress). Its pages set a strict
Content-Security-Policy and can only be framed by Home Assistant itself.

## Screens

- **Records**: what each tag name plays. Search, edit, preview art, test
  play in an area, and a play box (see below).
- **Tags**: every tag Home Assistant knows, newest scan first, with the
  record each one plays. **No record** shows the tags that play nothing.
  **Rename** sets the tag's name in Home Assistant. **Create record** starts
  a record with the tag's name. **Delete** removes the tag from Home
  Assistant. **All recent scans** below shows every scan, event and UI play
  with its result and error.
- **Print**: make a label PDF (see below).
- **Templates** and **Backgrounds**: the built-ins are read-only. Duplicate
  one to make your own. **Make default** on a background makes the Print
  screen start with it (white until you pick one).
- **Settings**: default volume (1 to 100), per-area volume (0 to 100), Sonos
  and Music Assistant entity per area, player order and fallback, reader
  integrations, print inset and bleed (outer bleed adds fill on the
  paper-edge side of the first and last columns).

## Records and play events

A record is a tag name and what it plays. Two Home Assistant events start
and stop music through the app, so a button, a switch or a dashboard tile
can play a record or any album in one `event` action.

### Records

Edit records on the **Records** tab, or by hand in `records.yaml`:

```yaml
version: 4
records:
  - id: kitchen
    name: Kitchen
    aliases: []
    items:
      - record: kenny-rogers-21-number-ones          # another record
      - media: Cat Stevens — Teaser and the Firecat  # any media text
      - media: https://music.apple.com/us/artist/the-dead-south/668262442
        name: The Dead South                         # shown in the editor
    shuffle:
      albums: false     # null = default: albums play in order
      artists: null     # null = default: shuffle
      playlists: null
      other: null       # songs, stations and names to search
    repeat: all         # null = all
    volume_offset: 10   # added to the area volume; null = 0
    player: null        # null = the player order
    label:
      title: null       # null = from the name
      subtitle: null
      artwork: null     # null = the first item's art
```

- `id` is lowercase letters, digits and `-`. Left empty, it comes from the
  name. Change it in the editor and the old id keeps working in events.
- Each item has one of:
  - `media`: a music link, an `Artist — Album` name, or a name that Music
    Assistant searches for.
  - `record`: the id of another record. Records can hold records to any
    depth. A loop is skipped.
- A link may have a `name`. The editor fills it from Spotify or Apple
  when you add the link, and you can change it. It is only shown in the
  editor. Spotify does not name private playlists or personal mixes; type
  a name for those.
- `volume_offset` (-100 to 100) is added to the volume of the area it plays
  in, so one record is louder or quieter everywhere. A play event's
  `volume` is a fixed volume and wins.
- A play picks one item at random. If that item is a record, it picks one
  of that record's items, and so on down. So in a record of five artists,
  each artist gets one play in five, however many albums it holds. The
  same item is not picked twice in a row when there is another.
- A record holds each item once. Two links to the same album count as
  one, even with different share parameters (`?si=...`). The editor says
  so when you add one again, and a hand-edited duplicate is dropped.
- The settings of the record you play apply, also to an item picked from
  inside another record. Shuffle depends on the kind of media picked.
- `aliases` are old ids: the old tag and pool ids from app 0.3, and ids
  you renamed. Events may name them. The editor keeps them.
- `label` sets the printed label. Without `artwork`, the record shows the
  art of its first item that has some.

The **Records** tab also has a play box. Pick a record or type any media,
pick an area, and press **Play** or **Stop**. It works the same way as the
events below.

### `nfc_player_play`

Plays music. Give a target (`area` or `player`) and one of `media` or
`record`. When more than one is set, the first non-empty one in the order
`media`, `record`, `tag`, `pool` wins, so an empty template result falls
through to the next.

| Key | Value | What it does |
|---|---|---|
| `area` | area id | Play in this area, on its speaker, as for a tag scan. |
| `player` | entity id | Play on this `media_player` only. It must be a Sonos or Music Assistant player. |
| `media` | text | A music link, `Artist — Album`, or a name to search. |
| `record` | text | A record id, old id or name. |
| `tag`, `pool` | text | Old names for `record`, from app 0.3. They still work. |
| `volume` | 0 to 100 | Volume in percent. |
| `shuffle` | true / false | For every kind of media. Not set: the record decides. |
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

A button hold toggles the kitchen record. A script may return music to
play instead. The script returns an empty `media` to keep the record:

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
        record: kitchen
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
        record: kitchen
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

On **Print**, pick records, copies, a template and a background. A label
shows the record's name, so a tag written with that name plays it. Click a
slot on the sheet to start there; used slots stay empty. **Generate PDF**
opens the PDF; print it at 100% (no scaling).

**Choose image…** under a record's artwork shows images to pick from. For
a record with one Apple Music link, it offers the cover, then the artists,
albums and playlists its page links to, the most linked first; more load
as you scroll. A record with more items offers the art of each item. A
Spotify link offers its cover only. The pick fills the artwork field; press
**Save**.

If a picked record has no cover art, a warning under the sheet names it
before you print. Those labels print without art. Open the record to set
an artwork URL or file, or press **Refresh art** to look it up again.

Art lookups only go to Apple (Apple Music pages, the iTunes API and its
image servers) and Spotify (its oEmbed API and image servers). An artwork URL you set yourself may point at any public
site, but never at an address on your local network.

If your printer shifts the page, set the offset in mm and press **Save
offsets**. It is saved per template. Positive X moves the print right,
positive Y moves it up.

## Support

Open an issue at <https://github.com/igrybkov/ha-nfc-player/issues>.
