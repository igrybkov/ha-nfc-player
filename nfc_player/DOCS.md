# NFC Player

Scan an NFC tag and the music linked to it plays in the room where you
scanned it. The app also keeps a list of your tags and prints labels for
them.

## How it works

1. Home Assistant fires a `tag_scanned` event when a tag reader sees a tag.
2. The app finds the tag in its registry. It matches by tag ID first, then by
   tag name.
3. It finds the speaker in the reader's area and plays the music there.
   Sonos speakers play natively. Music Assistant is the fallback.

## Setup

1. Install the app and start it.
2. Open **NFC Player** in the sidebar.
3. Add a tag: paste an Apple Music link and pick the tag from the list of
   tags Home Assistant knows.
4. Open **Settings** in the app to set rooms, volumes and print offsets.

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

You can edit these files by hand or with git. The app reloads a file when it
changes on disk.

Downloaded cover art is cached in the app's `/data` folder.

## Labels

Pick tags, a template and a start position on the sheet. The app makes a PDF
to print. Use the print offsets in settings if your printer shifts the page.

## Support

Open an issue at <https://github.com/igrybkov/ha-nfc-player/issues>.
