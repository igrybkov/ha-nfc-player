// Pools screen: pool list with a play box, and the pool editor.
import {
  h, get, post, put, del, toast, run, highlight, thumb, bumpArt, searchList, artURL, poolEntry,
  field, num, readNum, select, confirmDelete, focusIfDesktop, playerNames, areaSelect, splitName, artPicker, sized,
} from './lib.js';

// ALL is the built-in pool of every tag. The API lists stored pools only.
export const ALL = { id: 'all', name: 'All tags' };

export async function render(view, rest, params) {
  if (rest[0] === 'new') return editor(view, null, params);
  if (rest[0] === 'edit' && rest[1]) return editor(view, rest[1]);
  return list(view);
}

// poolName gives the display name of a pool id, also for "all" and for an
// id that names no pool.
export function poolName(pools, id) {
  if (id === ALL.id) return ALL.name;
  const p = pools.find((x) => x.id === id);
  return p ? p.name : `${id} (missing)`;
}

const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;

function summary(p) {
  return [
    p.id,
    plural(p.items.length, 'item'),
    p.shuffle != null && (p.shuffle ? 'shuffle' : 'no shuffle'),
    p.repeat != null && `repeat ${p.repeat}`,
    p.volume != null && `vol ${p.volume}`,
    p.player,
  ].filter(Boolean).join(' · ');
}


async function list(view) {
  const [{ pools }, entries] = await Promise.all([get('api/pools'), get('api/tags')]);
  const sl = searchList({
    items: pools,
    text: (p) => p.name,
    placeholder: 'Search pools (type anywhere)',
    empty: 'No pools yet. Use "New pool". "All tags" is built in.',
    row: (p, pos) => h('div', { class: 'row' },
      thumb(poolEntry(p)),
      h('div', { class: 'grow' },
        h('div', { class: 'title' }, highlight(p.name, pos)),
        h('div', { class: 'muted small', text: summary(p) }))),
    onEnter: (p) => { location.hash = `#/pools/edit/${encodeURIComponent(p.id)}`; },
  });
  view.replaceChildren(
    h('div', { class: 'toolbar' },
      h('h1', { text: `Pools (${pools.length})` }),
      h('a', { class: 'button primary', href: '#/pools/new', text: 'New pool' })),
    playBox(pools, entries),
    sl.el,
  );
  return sl.destroy;
}

// playBox plays a pool, a tag or any media text in an area, through the
// same path as an nfc_player_play event.
function playBox(pools, entries) {
  const byName = [...entries].sort((a, b) => a.name.localeCompare(b.name));
  const kind = select([['pool', 'Pool'], ['tag', 'Tag'], ['media', 'Media']], 'pool', { 'aria-label': 'Source' });
  const pool = select([[ALL.id, ALL.name], ...pools.map((p) => [p.id, p.name])], ALL.id, { 'aria-label': 'Pool' });
  const tag = select(byName.map((e) => [e.id, e.name]), '', { 'aria-label': 'Tag' });
  const media = h('input', { 'aria-label': 'Media', autocomplete: 'off', spellcheck: 'false',
    placeholder: 'URL, Artist — Album, or a name' });
  const area = areaSelect();
  const result = h('p', { class: 'small muted', role: 'status' });
  const sources = { pool, tag, media };

  const sync = () => {
    for (const [k, el] of Object.entries(sources)) el.hidden = k !== kind.value;
  };
  kind.addEventListener('change', sync);
  sync();

  const say = (text, cls = 'muted') => { result.className = `small ${cls}`; result.textContent = text; };
  const describe = (r) => {
    if (r.stopped) return `Stopped on ${r.player}.`;
    const what = r.entry_id ? (entries.find((e) => e.id === r.entry_id)?.name ?? r.entry_id) : r.media;
    return `Playing ${what || 'it'}${r.pool_id ? ` from ${poolName(pools, r.pool_id)}` : ''} on ${r.player}.`;
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
      cmd[kind.value] = sources[kind.value].value;
      if (!cmd[kind.value]) throw new Error(`Pick a ${kind.value}.`);
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
    h('div', { class: 'play-row' }, kind, pool, tag, media),
    h('div', { class: 'play-row' }, area, playBtn, stopBtn),
    result);
}

function blankPool(params) {
  return {
    id: '', name: params?.get('name') || '', items: [], shuffle: null, repeat: null, volume: null, player: null,
    label: { title: null, subtitle: null, artwork: null },
  };
}

async function editor(view, id, params) {
  const [{ pools }, entries, settings] = await Promise.all([get('api/pools'), get('api/tags'), get('api/settings')]);
  const pool = id ? pools.find((p) => p.id === id) : blankPool(params);
  if (!pool) {
    view.replaceChildren(h('p', { class: 'error', text: `No pool with id ${id}.` }), h('a', { href: '#/pools', text: 'Back to pools' }));
    return null;
  }
  const byID = new Map(entries.map((e) => [e.id, e]));
  const usedBy = entries.filter((e) => id && e.pool === id);

  const name = h('input', { value: pool.name, required: true, placeholder: 'Kitchen' });
  const pid = h('input', { value: pool.id, autocomplete: 'off', spellcheck: 'false', placeholder: 'From name' });
  // Nil settings defer to the item, so each select has an "Item decides" option.
  const shuffle = select([['', 'Item decides'], ['true', 'On'], ['false', 'Off']],
    pool.shuffle == null ? '' : String(pool.shuffle));
  const repeat = select([['', 'Item decides'], ['all', 'All'], ['one', 'One'], ['off', 'Off']], pool.repeat ?? '');
  const volume = num(pool.volume, { min: 0, max: 100, step: 1, placeholder: 'Item decides' });
  const player = select([['', 'Item decides'], ...playerNames(settings).map((p) => [p, p])], pool.player ?? '');
  const label = pool.label ?? { title: null, subtitle: null, artwork: null };
  const title = h('input', { value: label.title ?? '' });
  const subtitle = h('input', { value: label.subtitle ?? '' });
  const artwork = h('input', { value: label.artwork ?? '', placeholder: 'First item\'s art' });
  const syncPlaceholders = () => {
    const [t, s] = splitName(name.value);
    title.placeholder = t || 'From name';
    subtitle.placeholder = s || 'None';
  };
  name.addEventListener('input', syncPlaceholders);
  syncPlaceholders();

  // Art preview, as the label prints it.
  const art = h('img', { class: 'art', alt: 'Artwork', width: 200, height: 200 });
  art.addEventListener('error', () => art.classList.add('missing'));
  const showArt = (refresh) => {
    if (!id) return;
    const pe = poolEntry(pool);
    art.classList.remove('missing');
    if (refresh) art.addEventListener('load', () => bumpArt(pe.id), { once: true });
    art.src = artURL(pe, 400) + (refresh ? `&refresh=1&t=${Date.now()}` : '');
  };
  showArt(false);

  // Items. A tag item whose entry was deleted, or that now plays a pool,
  // stays in the list, marked: the server refuses to save the pool with it,
  // so the user sees why and removes it.
  const items = pool.items.map((i) => ({ ...i }));
  const itemList = h('ol', { class: 'list plain items' });
  const move = (i, d) => {
    [items[i], items[i + d]] = [items[i + d], items[i]];
    drawItems();
  };
  const itemRow = (it, i) => {
    const e = it.tag ? byID.get(it.tag) : null;
    const art = e?.url ? thumb(e, 32) : h('span', { class: 'thumb s32' });
    const text = it.tag
      ? h('div', { class: 'grow' },
        h('div', { class: 'title', text: e ? e.name : it.tag }),
        !e ? h('div', { class: 'error small', text: 'tag not found, remove it to save' })
          : e.pool ? h('div', { class: 'error small', text: 'tag plays a pool, remove it to save' })
            : h('div', { class: 'muted small', text: 'tag' }))
      : h('div', { class: 'grow' },
        h('div', { class: 'title', text: it.media }),
        h('div', { class: 'muted small', text: 'media' }));
    return h('li', {}, h('div', { class: 'row' }, art, text,
      h('button', { type: 'button', class: 'icon', 'aria-label': 'Move up', text: '↑', disabled: i === 0, onclick: () => move(i, -1) }),
      h('button', { type: 'button', class: 'icon', 'aria-label': 'Move down', text: '↓', disabled: i === items.length - 1, onclick: () => move(i, 1) }),
      h('button', { type: 'button', class: 'icon', 'aria-label': 'Remove', text: '×', onclick: () => { items.splice(i, 1); drawItems(); } })));
  };
  function drawItems() {
    // Detach the old rows first so their thumbnails can be reused.
    itemList.replaceChildren();
    itemList.replaceChildren(...items.map(itemRow));
    if (!items.length) itemList.append(h('li', { class: 'empty', text: 'No items yet. Add a tag or media.' }));
  }
  drawItems();

  // Pool tags can't be items (no nesting), so the picker leaves them out.
  const picker = searchList({
    items: entries.filter((e) => !e.pool),
    text: (e) => e.name,
    placeholder: 'Find a tag to add',
    empty: 'No tags yet.',
    row: (e, pos) => h('div', { class: 'row' }, thumb(e, 32), h('div', { class: 'grow title' }, highlight(e.name, pos))),
    onEnter: (e) => {
      items.push({ tag: e.id });
      drawItems();
      toast(`Added ${e.name}`);
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

  const opt = (input) => (input.value.trim() === '' ? null : input.value.trim());
  const collect = () => ({
    id: pid.value.trim(),
    name: name.value,
    items,
    shuffle: shuffle.value === '' ? null : shuffle.value === 'true',
    repeat: repeat.value || null,
    volume: readNum(volume),
    player: player.value || null,
    label: { title: opt(title), subtitle: opt(subtitle), artwork: opt(artwork) },
  });
  // Pool tags show the art of the pool's first item, which may have changed.
  const bumpPoolTags = (poolID) => {
    for (const e of entries) if (e.pool === poolID) bumpArt(e.id);
  };

  const save = h('button', { type: 'submit', class: 'primary', text: id ? 'Save' : 'Create' });
  const form = h('form', { class: 'editor', onsubmit: (ev) => {
    ev.preventDefault();
    run(save, async () => {
      const body = collect();
      const saved = id ? await put(`api/pools/${encodeURIComponent(id)}`, body) : await post('api/pools', body);
      bumpPoolTags(saved.id);
      bumpArt(`pool:${saved.id}`);
      if (id && id !== saved.id) bumpPoolTags(id); // tags of the old id lost their art
      toast(`Saved ${saved.name}`);
      location.hash = `#/pools/edit/${encodeURIComponent(saved.id)}`;
    });
  } },
  h('div', { class: 'toolbar' },
    h('a', { href: '#/pools', class: 'button', text: '← Pools' }),
    h('h1', { text: id ? pool.name : 'New pool' })),
  h('div', { class: 'cols' },
    h('div', { class: 'stack' },
      field('Name', name),
      field('ID', pid, id
        ? 'Used by events and pool tags. Tags and automations that use the old id stop working after a change.'
        : 'Used by events and pool tags. Empty = from the name.'),
      h('div', { class: 'grid3' },
        field('Shuffle', shuffle),
        field('Repeat', repeat),
        field('Volume %', volume)),
      field('Player', player),
      usedBy.length > 0 && h('p', { class: 'muted small', text: `Used by ${plural(usedBy.length, 'tag')}: ${usedBy.map((e) => e.name).join(', ')}.` }),
      h('fieldset', {}, h('legend', { text: 'Items' }),
        h('p', { class: 'muted small', text: 'Each play picks one at random, never the same one twice in a row.' }),
        itemList),
      h('fieldset', {}, h('legend', { text: 'Label' }),
        field('Title', title), field('Subtitle', subtitle),
        field('Artwork', artwork, 'Image URL or a path under the config dir. Pool tags without art of their own use it too.'),
        id && artPicker({
          path: `api/pools/${encodeURIComponent(id)}/artwork/candidates`,
          input: artwork,
          onPick: (preview) => { art.classList.remove('missing'); art.src = preview ? sized(preview, 400) : artURL(poolEntry(pool), 400); },
        }))),
    h('div', { class: 'stack' },
      id && h('div', { class: 'art-box' }, art,
        h('button', { type: 'button', text: 'Refresh art', onclick: () => showArt(true) })),
      h('fieldset', { class: 'picker' }, h('legend', { text: 'Add a tag' }), picker.el),
      h('fieldset', {}, h('legend', { text: 'Add media' }),
        h('div', { class: 'inline' }, media, h('button', { type: 'button', text: 'Add', onclick: addMedia })),
        h('p', { class: 'muted small', text: 'An Apple Music or Spotify URL, "Artist — Album", or a name Music Assistant searches for.' })))),
  h('div', { class: 'actions' },
    save,
    id && h('button', { type: 'button', class: 'danger', text: 'Delete', onclick: (ev) => {
      const what = usedBy.length ? `${pool.name} (used by ${plural(usedBy.length, 'tag')})` : pool.name;
      if (!confirmDelete(what)) return;
      run(ev.currentTarget, async () => {
        await del(`api/pools/${encodeURIComponent(id)}`);
        bumpPoolTags(id);
        toast(`Deleted ${pool.name}`);
        location.hash = '#/pools';
      });
    } })));

  view.replaceChildren(form);
  if (!id) focusIfDesktop(name);
  return picker.destroy;
}
