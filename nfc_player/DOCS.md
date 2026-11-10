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
3. A name like `Artist — Album` with no entry is searched in Music Assistant,
   and the first Apple Music album or playlist plays.
4. It finds the speaker in the reader's area and plays the music there.
   Sonos speakers play natively. Music Assistant is the fallback.
5. Volume: the tag's volume, else the area's, else the default volume.

## Setup

1. Install the app and start it.
2. Open **NFC Player** in the sidebar.
3. Add a tag: **Tags → New tag**. Use the tag's name as Home Assistant
   knows it and paste an Apple Music link. You can also link tag IDs from
   the list of tags Home Assistant knows.
4. Open **Settings** in the app to set area volumes, players and readers.

Coming from the old "Play Music when NFC tag is scanned" automation? Copy
`examples/tags.yaml` and `examples/settings.yaml` from the repository into
`config_dir`. They hold the old Music Map and room volumes. You can copy them
before or after the first start: the app writes nothing to `config_dir` until
you save something, and it picks up a new file on its next read.

## Options

| Option | Default | What it does |
|---|---|---|
| `log_level` | `info` | `debug`, `info`, `warn` or `error`. |
| `config_dir` | `/homeassistant/nfc_player` | Where the YAML files live. |

## Files

The app keeps your data as plain YAML under `config_dir`:

- `tags.yaml`: the tag registry.
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
- **Unknown scans**: recent scans that matched nothing, with **Create entry**.
  **All recent scans** below it shows every scan with its result and error.
- **Print**: make a label PDF (see below).
- **Templates** and **Backgrounds**: the built-ins are read-only. Duplicate
  one to make your own.
- **Settings**: default volume (1 to 100), per-area volume (0 to 100), Sonos
  and Music Assistant entity per area, player order and fallback, reader
  integrations, print inset and bleed.

## Labels

On **Print**, pick tags, copies, a template and a background. Click a slot
on the sheet to start there; used slots stay empty. **Generate PDF** opens
the PDF; print it at 100% (no scaling).

If a picked tag has no cover art, a warning under the sheet names it before
you print. Those labels print without art. Open the tag to set an artwork
URL or file, or press **Refresh art** to look it up again.

Art lookups only go to Apple (Apple Music pages, the iTunes API and its
image servers). An artwork URL you set yourself may point at any public
site, but never at an address on your local network.

If your printer shifts the page, set the offset in mm and press **Save
offsets**. It is saved per template. Positive X moves the print right,
positive Y moves it up.

## Support

Open an issue at <https://github.com/igrybkov/ha-nfc-player/issues>.
