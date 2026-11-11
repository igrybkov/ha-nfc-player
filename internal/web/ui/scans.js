// Unknown scans: recent scans that matched no entry, with "Rename" (the tag
// in HA) and "Create entry". The history below also lists play events and
// UI commands.
import { h, get, put, toast, run } from './lib.js';

const fmtTime = (t) => new Date(t).toLocaleString();

// Old rows have no source; they are all tag scans.
const isTagScan = (s) => !s.source || s.source === 'tag';

// what describes a row: the tag name for a scan, else the command's
// description plus the pool and media it played.
function what(s) {
  if (isTagScan(s)) return s.tag_name || s.tag_id;
  const detail = [s.pool_id && `pool ${s.pool_id}`, s.media].filter((x) => x && x !== s.tag_name);
  return [s.tag_name || '(stop)', detail.length > 0 && h('div', { class: 'muted small', text: detail.join(' · ') })];
}

// The tag's name in HA now; it differs from the recorded one after a rename.
const nameOf = (s) => s.ha_name || s.tag_name;

function createLink(s) {
  const q = new URLSearchParams();
  if (nameOf(s)) q.set('name', nameOf(s).split('|')[0].trim());
  if (s.tag_id) q.set('tag_id', s.tag_id);
  return `#/tags/new?${q}`;
}

// A pool matches tags by name only, so the link carries no tag id.
function poolLink(s) {
  const q = new URLSearchParams();
  if (nameOf(s)) q.set('name', nameOf(s).split('|')[0].trim());
  return `#/pools/new?${q}`;
}

// unknownRow shows an unknown tag. Rename swaps the title for an input
// that saves the name to the HA tag, so the tag and its entry share it.
function unknownRow(s, reload) {
  const li = h('li', {});
  const info = h('div', { class: 'muted small', text: `${s.tag_id} · ${s.area_id || 'no area'} · ${fmtTime(s.time)}` });
  const show = () => li.replaceChildren(h('div', { class: 'row' },
    h('div', { class: 'grow' }, h('div', { class: 'title', text: nameOf(s) || '(no name)' }), info),
    s.tag_id && h('button', { type: 'button', text: 'Rename', onclick: edit }),
    h('a', { class: 'button', href: poolLink(s), text: 'Create pool',
      title: 'A pool plays for every tag with its name. Rename the tag first if its name is a placeholder.' }),
    h('a', { class: 'button primary', href: createLink(s), text: 'Create entry' })));

  function edit() {
    const input = h('input', { type: 'text', value: nameOf(s) || '', maxlength: 200, 'aria-label': 'New tag name',
      placeholder: 'Artist — Album' });
    const save = h('button', { type: 'submit', class: 'primary', text: 'Save' });
    const form = h('form', { class: 'inline grow', onsubmit: (ev) => {
      ev.preventDefault();
      const name = input.value.trim();
      if (!name) { toast('Enter a name.', 'error'); return; }
      if (name === nameOf(s)) { show(); return; }
      run(save, async () => {
        await put(`api/ha/tags/${encodeURIComponent(s.tag_id)}`, { name });
        toast(`Renamed in Home Assistant: ${name}`);
        reload();
      });
    } }, input, save, h('button', { type: 'button', text: 'Cancel', onclick: show }));
    input.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') show(); });
    li.replaceChildren(h('div', { class: 'stack' }, form, info));
    input.focus();
    input.select();
  }
  show();
  return li;
}

export async function render(view) {
  const scans = await get('api/scans');
  // One row per tag; scans are newest first, so the first one wins.
  const seen = new Set();
  const unknown = scans.filter((s) => {
    if (!isTagScan(s) || s.result !== 'no_match' || s.matches_entry || s.matches_pool) return false;
    const key = s.tag_id || `name|${s.tag_name}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });

  view.replaceChildren(
    h('div', { class: 'toolbar' },
      h('h1', { text: 'Unknown scans' }),
      h('button', { type: 'button', text: 'Reload', onclick: () => render(view) })),
    unknown.length
      ? h('ul', { class: 'list plain' }, unknown.map((s) => unknownRow(s, () => render(view))))
      : h('p', { class: 'muted', text: 'No unknown scans. Scan a new tag and it shows up here.' }),
    h('details', { class: 'history' },
      h('summary', { text: `All recent scans (${scans.length})` }),
      h('div', { class: 'table-wrap' }, h('table', {},
        h('thead', {}, h('tr', {}, ['Time', 'Source', 'Tag or media', 'Area', 'Entry', 'Player', 'Result'].map((c) => h('th', { text: c })))),
        h('tbody', {}, scans.map((s) => h('tr', { class: s.result },
          h('td', { text: fmtTime(s.time) }),
          h('td', { text: s.source || 'tag' }),
          h('td', {}, what(s)),
          h('td', { text: s.area_id || s.entity }),
          h('td', {}, s.entry_id ? h('a', { href: `#/tags/edit/${encodeURIComponent(s.entry_id)}`, text: s.entry_id }) : ''),
          h('td', { text: s.player }),
          h('td', { title: s.error || '', text: s.error ? `${s.result}: ${s.error}` : s.result }))))))),
  );
}
