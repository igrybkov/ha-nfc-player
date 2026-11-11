// Records screen: searchable record list with a play box, and the record editor.
import {
  h, get, post, put, del, toast, run, highlight, thumb, bumpArt, searchList, artURL,
  field, num, readNum, select, confirmDelete, focusIfDesktop, playerNames, areaSelect, splitName, artPicker, sized,
} from './lib.js';

export async function render(view, rest, params) {
  if (rest[0] === 'new') return editor(view, null, params);
  if (rest[0] === 'edit' && rest[1]) return editor(view, rest[1]);
  return list(view);
}

const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
const editURL = (id) => `#/records/edit/${encodeURIComponent(id)}`;
const hasOthers = (r) => (r.items ?? []).some((i) => i.others);
// isURL mirrors records.IsURL: such media text is a link with art of its own.
const isURL = (m) => /^(https?:\/\/|(apple_music|spotify)(--[a-z0-9]+)?:\/\/|spotify:[a-z]+:)/i.test(m.trim());

// mediaThumb is the art of a media link, as thumb() shows a record's.
function mediaThumb(media, size = 32) {
  const img = h('img', {
    class: 'thumb', loading: 'lazy', alt: '', width: size, height: size,
    src: `api/artwork/media?url=${encodeURIComponent(media.trim())}&size=${size * 2}`,
  });
  img.addEventListener('error', () => img.classList.add('missing'), { once: true });
  return img;
}

function summary(r) {
  const items = r.items ?? [];
  const n = items.filter((i) => !i.others).length;
  return [
    r.id,
    n > 0 && plural(n, 'item'),
    hasOthers(r) && 'all other records',
    !items.length && 'no items',
    r.repeat && `repeat ${r.repeat}`,
    r.volume != null && `vol ${r.volume}`,
    r.player,
  ].filter(Boolean).join(' · ');
}

async function list(view) {
  const records = await get('api/records');
  const sl = searchList({
    items: records,
    text: (r) => r.name,
    placeholder: 'Search records (type anywhere)',
    empty: 'No records yet. Use "New record".',
    row: (r, pos) => h('div', { class: 'row' },
      thumb(r),
      h('div', { class: 'grow' },
        h('div', { class: 'title' }, highlight(r.name, pos)),
        h('div', { class: 'muted small', text: summary(r) }))),
    onEnter: (r) => { location.hash = editURL(r.id); },
  });
  view.replaceChildren(
    h('div', { class: 'toolbar' },
      h('h1', { text: `Records (${records.length})` }),
      h('a', { class: 'button primary', href: '#/records/new', text: 'New record' })),
    playBox(records),
    sl.el,
  );
  focusIfDesktop(sl.input);
  return sl.destroy;
}

// playBox plays a record or any media text in an area, through the same
// path as an nfc_player_play event.
function playBox(records) {
  const byName = [...records].sort((a, b) => a.name.localeCompare(b.name));
  const kind = select([['record', 'Record'], ['media', 'Media']], 'record', { 'aria-label': 'Source' });
  const record = select(byName.map((r) => [r.id, r.name]), '', { 'aria-label': 'Record' });
  const media = h('input', { 'aria-label': 'Media', autocomplete: 'off', spellcheck: 'false',
    placeholder: 'URL, Artist — Album, or a name' });
  const area = areaSelect();
  const result = h('p', { class: 'small muted', role: 'status' });
  const sources = { record, media };

  const sync = () => {
    for (const [k, el] of Object.entries(sources)) el.hidden = k !== kind.value;
  };
  kind.addEventListener('change', sync);
  sync();

  const say = (text, cls = 'muted') => { result.className = `small ${cls}`; result.textContent = text; };
  const describe = (r) => {
    if (r.stopped) return `Stopped on ${r.player}.`;
    const what = r.record_id ? (records.find((x) => x.id === r.record_id)?.name ?? r.record_id) : r.media;
    const picked = r.record_id && r.media && r.media !== what ? ` (${r.media})` : '';
    return `Playing ${what || 'it'}${picked} on ${r.player}.`;
  };
  // Errors go to the result line, not a toast: it is where the user looks.
  const act = (btn, fn) => run(btn, async () => {
    try {
      if (!area.value) throw new Error('Pick an area.');
      say('Working…');
      say(await fn());
    } catch (e) {
      say(e.message, 'error');
    }
  });
  const play = () => act(playBtn, async () => {
    const cmd = { area: area.value };
    if (kind.value === 'media') {
      cmd.media = media.value.trim();
      if (!cmd.media) throw new Error('Enter media to play.');
    } else {
      cmd.record = record.value;
      if (!cmd.record) throw new Error('Pick a record.');
    }
    return describe(await post('api/play', cmd));
  });
  const playBtn = h('button', { type: 'button', class: 'primary', text: 'Play', onclick: play });
  const stopBtn = h('button', { type: 'button', text: 'Stop', onclick: () => act(stopBtn, async () => {
    const r = await post('api/stop', { area: area.value });
    return `Stopped on ${r.player}.`;
  }) });
  media.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); play(); }
  });

  return h('fieldset', { class: 'play-box' }, h('legend', { text: 'Play' }),
    h('div', { class: 'play-row' }, kind, record, media),
    h('div', { class: 'play-row' }, area, playBtn, stopBtn),
    result);
}

function blankRecord(params) {
  return {
    id: '', name: params?.get('name') || '', aliases: [], items: [], shuffle: null, repeat: null, volume: null, player: null,
    label: { title: null, subtitle: null, artwork: null },
  };
}

async function editor(view, id, params) {
  const [records, settings] = await Promise.all([get('api/records'), get('api/settings')]);
  const record = id ? records.find((r) => r.id === id) : blankRecord(params);
  if (!record) {
    view.replaceChildren(h('p', { class: 'error', text: `No record with id ${id}.` }), h('a', { href: '#/records', text: 'Back to records' }));
    return null;
  }
  const byID = new Map(records.map((r) => [r.id, r]));
  const usedBy = id ? records.filter((r) => r.id !== id && (r.items ?? []).some((i) => i.record === id)) : [];

  const name = h('input', { value: record.name, required: true, placeholder: 'Artist — Album' });
  const rid = h('input', { value: record.id, autocomplete: 'off', spellcheck: 'false', placeholder: 'From name' });

  // Saved as explicit booleans. A missing value gets the default.
  const sh = record.shuffle ?? {};
  const shuffleBox = (key, text, def) => {
    const box = h('input', { type: 'checkbox', checked: sh[key] ?? def });
    return [key, box, h('label', { class: 'check' }, box, ` ${text}`)];
  };
  const shuffles = [
    shuffleBox('albums', 'Shuffle albums', false),
    shuffleBox('artists', 'Shuffle artists', true),
    shuffleBox('playlists', 'Shuffle playlists', true),
    shuffleBox('other', 'Shuffle the rest', true),
  ];
  const repeat = select([['', 'All'], ['one', 'One'], ['off', 'Off']], record.repeat === 'all' ? '' : record.repeat ?? '');
  const volume = num(record.volume, { min: 0, max: 100, step: 1, placeholder: 'Room default' });
  const player = select([['', 'Default chain'], ...playerNames(settings).map((p) => [p, p])], record.player ?? '');

  const label = record.label ?? {};
  const title = h('input', { value: label.title ?? '' });
  const subtitle = h('input', { value: label.subtitle ?? '' });
  const artwork = h('input', { value: label.artwork ?? '', placeholder: 'Automatic' });
  const syncPlaceholders = () => {
    const [t, s] = splitName(name.value);
    title.placeholder = t || 'From name';
    subtitle.placeholder = s || 'From name';
  };
  name.addEventListener('input', syncPlaceholders);
  syncPlaceholders();

  // Art preview, as the label prints it.
  const art = h('img', { class: 'art', alt: 'Artwork', width: 200, height: 200 });
  art.addEventListener('error', () => art.classList.add('missing'));
  const showArt = (refresh) => {
    if (!id) return;
    art.classList.remove('missing');
    if (refresh) art.addEventListener('load', () => bumpArt(id), { once: true });
    art.src = artURL(record, 400) + (refresh ? `&refresh=1&t=${Date.now()}` : '');
  };
  showArt(false);

  // Items. A record item whose record was deleted stays in the list,
  // marked, so the user sees why the server refuses it and removes it.
  const items = (record.items ?? []).map((i) => (i.others ? { others: true } : i.record ? { record: i.record } : { media: i.media }));
  const itemList = h('ol', { class: 'list plain items' });
  const move = (i, d) => {
    [items[i], items[i + d]] = [items[i + d], items[i]];
    drawItems();
  };
  const itemRow = (it, i) => {
    const r = it.record ? byID.get(it.record) : null;
    const text = it.others
      ? h('div', { class: 'grow' },
        h('div', { class: 'title', text: 'All other records' }),
        h('div', { class: 'muted small', text: 'every record not in this list' }))
      : it.record
        ? h('div', { class: 'grow' },
          h('div', { class: 'title', text: r ? r.name : it.record }),
          r ? h('div', { class: 'muted small', text: 'record' })
            : h('div', { class: 'error small', text: 'record not found, remove it to save' }))
        : h('div', { class: 'grow' },
          h('div', { class: 'title', text: it.media }),
          h('div', { class: 'muted small', text: 'media' }));
    const art = r ? thumb(r, 32) : it.media && isURL(it.media) ? mediaThumb(it.media) : h('span', { class: 'thumb s32' });
    return h('li', {}, h('div', { class: 'row' }, art, text,
      h('button', { type: 'button', class: 'icon', 'aria-label': 'Move up', text: '↑', disabled: i === 0, onclick: () => move(i, -1) }),
      h('button', { type: 'button', class: 'icon', 'aria-label': 'Move down', text: '↓', disabled: i === items.length - 1, onclick: () => move(i, 1) }),
      h('button', { type: 'button', class: 'icon', 'aria-label': 'Remove', text: '×', onclick: () => { items.splice(i, 1); drawItems(); } })));
  };
  const othersBtn = h('button', { type: 'button', text: 'Add all other records', onclick: () => {
    items.push({ others: true });
    drawItems();
  } });
  function drawItems() {
    // Detach the old rows first so their thumbnails can be reused.
    itemList.replaceChildren();
    itemList.replaceChildren(...items.map(itemRow));
    if (!items.length) itemList.append(h('li', { class: 'empty', text: 'No items yet. Add media or a record.' }));
    othersBtn.disabled = items.some((i) => i.others);
  }
  drawItems();

  const picker = searchList({
    items: records.filter((r) => r.id !== id),
    text: (r) => r.name,
    placeholder: 'Find a record to add',
    empty: 'No other records yet.',
    row: (r, pos) => h('div', { class: 'row' }, thumb(r, 32), h('div', { class: 'grow title' }, highlight(r.name, pos))),
    onEnter: (r) => {
      items.push({ record: r.id });
      drawItems();
      toast(`Added ${r.name}`);
    },
  });

  const media = h('input', { autocomplete: 'off', spellcheck: 'false', placeholder: 'URL, Artist — Album, or a name' });
  const addMedia = () => {
    const v = media.value.trim();
    if (!v) return;
    items.push({ media: v });
    media.value = '';
    drawItems();
  };
  media.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); addMedia(); }
  });

  const area = areaSelect();

  const opt = (input) => (input.value.trim() === '' ? null : input.value.trim());
  const collect = () => ({
    id: rid.value.trim(),
    name: name.value,
    aliases: record.aliases ?? [],
    items,
    shuffle: Object.fromEntries(shuffles.map(([k, box]) => [k, box.checked])),
    repeat: repeat.value || null,
    volume: readNum(volume),
    player: player.value || null,
    label: { title: opt(title), subtitle: opt(subtitle), artwork: opt(artwork) },
  });

  const save = h('button', { type: 'submit', class: 'primary', text: id ? 'Save' : 'Create' });
  const form = h('form', { class: 'editor', onsubmit: (ev) => {
    ev.preventDefault();
    run(save, async () => {
      const body = collect();
      const saved = id ? await put(`api/records/${encodeURIComponent(id)}`, body) : await post('api/records', body);
      // Records that list this one show its art too.
      for (const r of [saved, ...usedBy]) bumpArt(r.id);
      toast(`Saved ${saved.name}`);
      const to = editURL(saved.id);
      // Same id: the hash stays, so ask the router to reload the screen.
      if (location.hash === to) window.dispatchEvent(new HashChangeEvent('hashchange'));
      else location.hash = to;
    });
  } },
  h('div', { class: 'toolbar' },
    h('a', { href: '#/records', class: 'button', text: '← Records' }),
    h('h1', { text: id ? record.name : 'New record' })),
  h('div', { class: 'cols' },
    h('div', { class: 'stack' },
      field('Name', name, 'Matched against the HA tag name. Text after "|" is ignored.'),
      field('ID', rid, id
        ? 'Used by events and other records. Old ids keep working after a change.'
        : 'Used by events and other records. Empty = from the name.'),
      usedBy.length > 0 && h('p', { class: 'muted small', text: `Used by ${plural(usedBy.length, 'record')}: ${usedBy.map((r) => r.name).join(', ')}.` }),
      h('fieldset', {}, h('legend', { text: 'Items' }),
        h('p', { class: 'muted small', text: 'Each play picks one item at random. A record item picks from its own items.' }),
        itemList),
      h('fieldset', {}, h('legend', { text: 'Playback' }),
        h('div', { class: 'grid2' }, shuffles.map((s) => s[2])),
        h('div', { class: 'grid2' },
          field('Repeat', repeat),
          field('Volume %', volume)),
        field('Player', player)),
      h('fieldset', {}, h('legend', { text: 'Label' }),
        field('Title', title), field('Subtitle', subtitle),
        field('Artwork', artwork, 'Image URL or a path under the config dir.'),
        id && artPicker({
          path: `api/records/${encodeURIComponent(id)}/artwork/candidates`,
          input: artwork,
          onPick: (preview) => { art.classList.remove('missing'); art.src = preview ? sized(preview, 400) : artURL(record, 400); },
        }))),
    h('div', { class: 'stack' },
      id && h('div', { class: 'art-box' }, art,
        h('button', { type: 'button', text: 'Refresh art', onclick: () => showArt(true) })),
      h('fieldset', {}, h('legend', { text: 'Add media' }),
        h('div', { class: 'inline' }, media, h('button', { type: 'button', text: 'Add', onclick: addMedia })),
        h('p', { class: 'muted small', text: 'An Apple Music or Spotify URL, "Artist — Album", or a name Music Assistant searches for.' })),
      h('fieldset', { class: 'picker' }, h('legend', { text: 'Add a record' }), picker.el, othersBtn),
      id && h('fieldset', {}, h('legend', { text: 'Test play' }), area,
        h('button', { type: 'button', text: 'Play', onclick: (ev) => run(ev.currentTarget, async () => {
          if (!area.value) throw new Error('Pick an area.');
          const r = await post(`api/records/${encodeURIComponent(id)}/play`, { area_id: area.value });
          toast(`Playing on ${r.player}`);
        }) })))),
  h('div', { class: 'actions' },
    save,
    id && h('button', { type: 'button', class: 'danger', text: 'Delete', onclick: (ev) => {
      const what = usedBy.length ? `${record.name} (used by ${plural(usedBy.length, 'record')})` : record.name;
      if (!confirmDelete(what)) return;
      run(ev.currentTarget, async () => {
        await del(`api/records/${encodeURIComponent(id)}`);
        toast(`Deleted ${record.name}`);
        location.hash = '#/records';
      });
    } })));

  view.replaceChildren(form);
  if (!id) focusIfDesktop(name);
  return picker.destroy;
}
