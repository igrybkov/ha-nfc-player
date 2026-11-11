// Unknown scans: recent scans that matched no entry, with "Create entry".
// The history below also lists play events and UI commands.
import { h, get } from './lib.js';

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

function createLink(s) {
  const q = new URLSearchParams();
  if (s.tag_name) q.set('name', s.tag_name.split('|')[0].trim());
  if (s.tag_id) q.set('tag_id', s.tag_id);
  return `#/tags/new?${q}`;
}

export async function render(view) {
  const scans = await get('api/scans');
  // One row per tag; scans are newest first, so the first one wins.
  const seen = new Set();
  const unknown = scans.filter((s) => {
    if (!isTagScan(s) || s.result !== 'no_match' || s.matches_entry) return false;
    const key = `${s.tag_id}|${s.tag_name}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });

  view.replaceChildren(
    h('div', { class: 'toolbar' },
      h('h1', { text: 'Unknown scans' }),
      h('button', { type: 'button', text: 'Reload', onclick: () => render(view) })),
    unknown.length
      ? h('ul', { class: 'list plain' }, unknown.map((s) => h('li', {},
        h('div', { class: 'row' },
          h('div', { class: 'grow' },
            h('div', { class: 'title', text: s.tag_name || '(no name)' }),
            h('div', { class: 'muted small', text: `${s.tag_id} · ${s.area_id || 'no area'} · ${fmtTime(s.time)}` })),
          h('a', { class: 'button primary', href: createLink(s), text: 'Create entry' })))))
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
