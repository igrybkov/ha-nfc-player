# Changelog

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
