# Changelog

## 0.5.2

- A scan now unmutes the speaker before the music starts. Before, a
  soundbar muted from the TV remote played the music in silence. Turn
  this off in **Settings** with **Unmute the speaker when music starts**
  (`rooms.unmute_on_play`, on by default).

## 0.5.1

The tag v0.5.0 failed its build and was never released; 0.5.1 is that
release.


- Backgrounds are now **themes**. A theme sets the colors, and for each
  label format (small label, large label, dot) where the art goes: left,
  right, on top, at the bottom, behind the text, or none. It can print
  the art in gray. The fade settings show only where art sits behind the
  text.
- The theme editor previews the theme on every template you have, with no
  art and with three made-up covers. The server draws the previews with
  the print code, so they show what prints. Type your own title and
  subtitle to try them.
- Templates have a format in place of a layout. **Dot** templates print
  round labels: the art and fill are cut to a circle and the text stays
  inside it. With art beside the text it sits next to the art; otherwise
  it is centered.
- Files move to version 5: `backgrounds.yaml` becomes `themes.yaml`, a
  template's `layout: full` becomes `format: small` and `side` becomes
  `large`, and the default background becomes the default theme. Labels
  print as before.
- Reorder a record's items by dragging them by their handle (⠿). It works
  with a mouse and on touch screens. With the handle focused, the up and
  down arrow keys move the item too. The ↑ and ↓ buttons are gone.

## 0.4.2

- A record holds each item once. Links that point to the same album,
  playlist or artist count as one, whatever their share parameters, and
  names match without regard to case or spacing. The editor refuses a
  duplicate; a file with duplicates plays without them and drops them on
  the next save.
- Links in a record have a name ("Deep Focus" rather than the link). The
  editor fills it from Spotify or Apple when you add a link; edit it in
  place. The link stays below it, and opens on click. Older items show
  the looked-up name greyed out until you save. Files move to version 4.
- Spotify's own playlists that oEmbed gives no cover for now get their
  cover from the embed player.

## 0.4.1

- A record's volume is now an offset from the room's volume (-100 to
  100). Files convert on read; an old fixed volume becomes no offset.
- "All other records" is gone.
- The record editor: Save, Delete and an "Unsaved changes" mark stay at
  the top while you scroll. Items, add media and add a record sit
  together; the label and its art sit together.
- Label title and subtitle hints show exactly what prints, or nothing.
- Area lists show only areas with a speaker.
- Records are sorted by name everywhere.
- A search list marks its first row only while you type in it, so nothing
  looks picked before you choose.

## 0.4.0

- **Records** replace tags and pools. A record is a tag name and what it
  plays: one or more items, each a music link or name, another record, or
  all other records. A play picks an item at random, then goes down into
  it, so every artist in a mix gets a fair share.
- Shuffle per kind of media: albums play in order by default; artists,
  playlists and the rest shuffle. Four checkboxes per record.
- The **Tags** screen lists every Home Assistant tag with the record it
  plays. Rename, delete or create a record from there. It replaces Unknown
  scans.
- Files carry a schema version and older ones convert on read.
  `tags.yaml` and `pools.yaml` become `records.yaml`; templates and
  backgrounds files get a version. Nothing is written until you save;
  that save writes every file in the new format.
- Linked tag ids are gone: records match tags by name. Play events with
  `tag:` or `pool:` still work, and `record:` is the new key.

## 0.3.2

- **Unknown scans → Rename** renames the tag in Home Assistant. The row
  then shows the new name, and **Create entry** starts with it.
- **Unknown scans → Create pool** starts a pool named after the tag. A tag
  whose name plays a pool no longer shows as unknown.
- Pools have a label (title, subtitle, artwork) and show their art. A pool
  without artwork uses its first item's; pool tags without art use the
  pool's.
- **Print** lists pools next to tags. A pool label prints the pool's name,
  so a tag written with that name plays the pool.
- **Choose image…** under a label's artwork picks the art from a list. An
  Apple Music link offers its cover, then every artist, album and playlist
  its page links to, loading more as you scroll. A pool offers its items'
  art. Spotify offers its cover only.

## 0.3.1

- Pick the default background on the Backgrounds screen (**Make default**).
  The Print screen starts with it.
- Labels in the first and last columns bleed 1 mm more toward the paper
  edge, where printers drift most (**Settings → Outer bleed mm**).

## 0.3.0

- Spotify: tags, pools and play events take Spotify links
  (`open.spotify.com/...`, also with `?si=` or `/intl-xx/`) and
  `spotify:` URIs. Sonos plays albums, playlists and tracks. Music
  Assistant plays artists, since Sonos refuses a Spotify artist link.
  Cover art comes from Spotify.
- The `Artist — Album` search found nothing when Music Assistant returned
  provider URIs with an instance suffix (`apple_music--P6Nxuakq://...`).
  Those hits now play. Apple Music hits rank before Spotify hits.
- Label PDFs embed complete fonts. Some printers (HP LaserJet) silently
  dropped the old PDFs and printed nothing.
- Label PDFs are named after what they print: the label titles in print
  order, each shortened so one long name doesn't crowd out the rest (short
  names leave their spare room to long ones), and a short hash of the job
  (`five-finger-de-the-rasmus-disturbed-k3x9q2mf.pdf`).

## 0.2.2

- A scanned tag with no entry plays the pool its name matches (name or id,
  cut at "|", any case). "All tags" plays a random tag.
- Apple Music stations play on Sonos. Music Assistant can't play them, so
  they no longer fail there with "NotImplementedError".

## 0.2.1

- Shuffle on an artist tag now starts with a random song. Sonos always
  started at the artist's top song.
- Repeat and shuffle are set again after the queue is loaded on Sonos, since
  loading a new queue turned repeat off.

## 0.2.0

- Pools: named lists of tags and media in `pools.yaml`, edited on the new
  Pools tab. A play picks one item at random and never the same one twice
  in a row. "All tags" is a built-in pool.
- A tag can play a pool instead of one URL.
- `nfc_player_play` and `nfc_player_stop` events start or stop music from
  any automation, by area or by player. Play takes a pool, tag or media
  text, plus volume, shuffle, repeat, Music Assistant radio mode and toggle.
- A play box on the Pools tab plays or stops a pool, tag or media in a room.
- Media text that is not a link is searched by name in Music Assistant.
- The Scans screen shows where each play came from: tag, event or UI.
- The Print screen shows the art and name of each label on the sheet map.

## 0.1.1

- Selecting labels on the Print screen or searching a list no longer
  reloads every thumbnail.

## 0.1.0

- First release.
- Play Apple Music albums, playlists, songs and artists when an NFC tag is
  scanned. Sonos is played natively; Music Assistant is the fallback.
- Tag registry with a web UI in the Home Assistant sidebar.
- Print labels for tags as PDF, with pluggable layouts, templates and
  backgrounds.
