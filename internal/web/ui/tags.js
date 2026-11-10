// Tags screen: searchable entry list and the entry editor.
import {
  h, get, post, put, del, toast, run, highlight, thumb, artURL, bumpArt, searchList,
  field, num, readNum, select, confirmDelete, splitName, focusIfDesktop,
} from './lib.js';

const KNOWN_PLAYERS = ['sonos', 'music_assistant'];

export async function render(view, rest, params) {
  if (rest[0] === 'new') return editor(view, null, params);
  if (rest[0] === 'edit' && rest[1]) return editor(view, rest[1], params);
  return list(view);
}

function kindOf(url) {
  const m = /music\.apple\.com\/[a-z]{2}\/(album|playlist|song|artist|station)\//.exec(url)
    || /^apple_music:\/\/(\w+)\//.exec(url);
  return m ? m[1] : 'link';
}

async function list(view) {
  const entries = await get('api/tags');
  const sl = searchList({
    items: entries,
    text: (e) => e.name,
    placeholder: 'Search tags (type anywhere)',
    empty: 'No tags yet. Use "New tag" or create one from Unknown scans.',
    row: (e, pos) => h('div', { class: 'row' },
      thumb(e),
      h('div', { class: 'grow' },
        h('div', { class: 'title' }, highlight(e.name, pos)),
        h('div', { class: 'muted small' },
          kindOf(e.url),
          e.shuffle && ' · shuffle',
          e.repeat !== 'all' && ` · repeat ${e.repeat}`,
          e.volume != null && ` · vol ${e.volume}`,
          e.tag_ids.length > 0 && ` · ${e.tag_ids.length} tag id${e.tag_ids.length > 1 ? 's' : ''}`)),
    ),
    onEnter: (e) => { location.hash = `#/tags/edit/${encodeURIComponent(e.id)}`; },
  });
  view.replaceChildren(
    h('div', { class: 'toolbar' },
      h('h1', { text: `Tags (${entries.length})` }),
      h('a', { class: 'button primary', href: '#/tags/new', text: 'New tag' })),
    sl.el,
  );
  focusIfDesktop(sl.input);
  return sl.destroy;
}

function blankEntry(params) {
  return {
    id: '', name: params.get('name') || '', tag_ids: params.get('tag_id') ? [params.get('tag_id')] : [],
    url: '', shuffle: false, repeat: 'all', volume: null, player: null,
    label: { title: null, subtitle: null, artwork: null },
  };
}

async function editor(view, id, params) {
  const [entries, settings] = await Promise.all([get('api/tags'), get('api/settings')]);
  const entry = id ? entries.find((e) => e.id === id) : blankEntry(params);
  if (!entry) {
    view.replaceChildren(h('p', { class: 'error', text: `No tag with id ${id}.` }), h('a', { href: '#/tags', text: 'Back to tags' }));
    return;
  }

  const name = h('input', { value: entry.name, required: true, placeholder: 'Artist — Album' });
  // type=text: the browser's URL check rejects apple_music:// (underscore in
  // the scheme). The server validates the link.
  const url = h('input', { value: entry.url, type: 'text', inputmode: 'url', autocomplete: 'off', spellcheck: 'false',
    required: true, placeholder: 'https://music.apple.com/…' });
  const shuffle = h('input', { type: 'checkbox', checked: entry.shuffle });
  const repeat = select([['all', 'All'], ['one', 'One'], ['off', 'Off']], entry.repeat);
  const volume = num(entry.volume, { min: 0, max: 100, step: 1, placeholder: 'Room default' });
  const players = [...new Set([...settings.players.default, ...KNOWN_PLAYERS])];
  const player = select([['', 'Default chain'], ...players.map((p) => [p, p])], entry.player ?? '');
  const title = h('input', { value: entry.label.title ?? '', placeholder: '' });
  const subtitle = h('input', { value: entry.label.subtitle ?? '', placeholder: '' });
  const artwork = h('input', { value: entry.label.artwork ?? '', placeholder: 'Automatic' });

  const syncPlaceholders = () => {
    const [t, s] = splitName(name.value);
    title.placeholder = t || 'From name';
    subtitle.placeholder = s || 'From name';
  };
  name.addEventListener('input', syncPlaceholders);
  syncPlaceholders();

  // Linked HA tag ids.
  const tagIDs = [...entry.tag_ids];
  const chips = h('div', { class: 'chips' });
  const picker = h('select', { 'aria-label': 'Link an HA tag' });
  let haTags = [];
  const drawChips = () => {
    chips.replaceChildren(...tagIDs.map((t) => {
      const known = haTags.find((x) => x.id === t);
      return h('span', { class: 'chip' }, known ? `${known.name || '(no name)'} · ${t}` : t,
        h('button', { type: 'button', class: 'icon', 'aria-label': `Unlink ${t}`, text: '×',
          onclick: () => { tagIDs.splice(tagIDs.indexOf(t), 1); drawChips(); } }));
    }));
    picker.replaceChildren(h('option', { value: '', text: haTags.length ? 'Link an HA tag…' : 'No HA tags loaded' }),
      ...haTags.filter((t) => !tagIDs.includes(t.id)).map((t) => h('option', { value: t.id },
        `${t.name || '(no name)'} · ${t.id}${t.entry_id && t.entry_id !== entry.id ? ` (→ ${t.entry_id})` : ''}`)));
  };
  picker.addEventListener('change', () => {
    if (picker.value) tagIDs.push(picker.value);
    drawChips();
  });
  drawChips();
  get('api/ha/tags').then((t) => { haTags = t; drawChips(); }).catch((e) => toast(`HA tags: ${e.message}`, 'error'));

  // Art preview.
  const art = h('img', { class: 'art', alt: 'Artwork', width: 200, height: 200 });
  art.addEventListener('error', () => art.classList.add('missing'));
  const showArt = (refresh) => {
    if (!entry.id) return;
    art.classList.remove('missing');
    // Once refreshed art loads, list and Print thumbnails must ask again.
    if (refresh) art.addEventListener('load', () => bumpArt(entry.id), { once: true });
    art.src = artURL(entry, 400) + (refresh ? `&refresh=1&t=${Date.now()}` : '');
  };
  showArt(false);

  // Test play.
  const area = h('select', { 'aria-label': 'Area' }, h('option', { value: '', text: 'Loading areas…' }));
  get('api/ha/areas').then((areas) => {
    area.replaceChildren(...areas.map((a) => h('option', { value: a.id }, a.name)));
  }).catch((e) => area.replaceChildren(h('option', { value: '', text: `Areas unavailable: ${e.message}` })));

  const opt = (input) => (input.value.trim() === '' ? null : input.value.trim());
  const collect = () => ({
    id: entry.id,
    name: name.value,
    tag_ids: tagIDs,
    url: url.value,
    shuffle: shuffle.checked,
    repeat: repeat.value,
    volume: readNum(volume),
    player: player.value || null,
    label: { title: opt(title), subtitle: opt(subtitle), artwork: opt(artwork) },
  });

  const save = h('button', { type: 'submit', class: 'primary', text: id ? 'Save' : 'Create' });
  const form = h('form', { class: 'editor', onsubmit: (ev) => {
    ev.preventDefault();
    run(save, async () => {
      const body = collect();
      const saved = id ? await put(`api/tags/${encodeURIComponent(id)}`, body) : await post('api/tags', body);
      toast(`Saved ${saved.name}`);
      location.hash = `#/tags/edit/${encodeURIComponent(saved.id)}`;
      if (id) { Object.assign(entry, saved); showArt(false); }
    });
  } },
  h('div', { class: 'toolbar' },
    h('a', { href: '#/tags', class: 'button', text: '← Tags' }),
    h('h1', { text: id ? entry.name : 'New tag' })),
  h('div', { class: 'cols' },
    h('div', { class: 'stack' },
      field('Name', name, 'Matched against the HA tag name. Text after "|" is ignored.'),
      field('Music URL', url, 'Apple Music link or apple_music:// URI.'),
      h('div', { class: 'grid3' },
        h('label', { class: 'check' }, shuffle, ' Shuffle'),
        field('Repeat', repeat),
        field('Volume %', volume)),
      field('Player', player),
      h('fieldset', {}, h('legend', { text: 'Linked HA tag IDs' }),
        h('p', { class: 'muted small', text: 'A linked ID wins over the name match.' }),
        chips, picker),
      h('fieldset', {}, h('legend', { text: 'Label' }),
        field('Title', title), field('Subtitle', subtitle),
        field('Artwork', artwork, 'Image URL or a path under the config dir.'))),
    h('div', { class: 'stack' },
      entry.id && h('div', { class: 'art-box' }, art,
        h('button', { type: 'button', text: 'Refresh art', onclick: () => showArt(true) })),
      entry.id && h('fieldset', {}, h('legend', { text: 'Test play' }), area,
        h('button', { type: 'button', text: 'Play', onclick: (ev) => run(ev.currentTarget, async () => {
          if (!area.value) throw new Error('Pick an area.');
          const r = await post(`api/tags/${encodeURIComponent(entry.id)}/play`, { area_id: area.value });
          toast(`Playing on ${r.player}`);
        }) })))),
  h('div', { class: 'actions' },
    save,
    entry.id && h('button', { type: 'button', class: 'danger', text: 'Delete', onclick: (ev) => {
      if (!confirmDelete(entry.name)) return;
      run(ev.currentTarget, async () => {
        await del(`api/tags/${encodeURIComponent(entry.id)}`);
        toast(`Deleted ${entry.name}`);
        location.hash = '#/tags';
      });
    } })));

  view.replaceChildren(form);
  focusIfDesktop(id ? url : name);
}
