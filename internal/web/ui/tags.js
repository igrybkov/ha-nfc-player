// Tags screen: every HA tag with the record it plays, plus the scan history.
import { h, get, put, del, toast, run, highlight, thumb, searchList, focusIfDesktop } from './lib.js';

const fmtTime = (t) => new Date(t).toLocaleString();

// Tags never scanned have no time and sort last.
const byScanned = (a, b) => (b.last_scanned ? Date.parse(b.last_scanned) : 0) - (a.last_scanned ? Date.parse(a.last_scanned) : 0);

const createLink = (t) => `#/records/new?${new URLSearchParams({ name: (t.name || '').split('|')[0].trim() })}`;

// Old rows have no source; they are all tag scans.
const isTagScan = (s) => !s.source || s.source === 'tag';

// what describes a scan row: the tag name for a scan, else the command's
// description plus the media it played.
function what(s) {
  if (isTagScan(s)) return s.tag_name || s.tag_id;
  const detail = [s.media].filter((x) => x && x !== s.tag_name);
  return [s.tag_name || '(stop)', detail.length > 0 && h('div', { class: 'muted small', text: detail.join(' · ') })];
}

export async function render(view) {
  const [haTags, records, scans] = await Promise.all([get('api/ha/tags'), get('api/records'), get('api/scans')]);
  const byID = new Map(records.map((r) => [r.id, r]));
  let tags = [...haTags].sort(byScanned);
  let only = 'all'; // or 'none': tags without a record

  const title = h('h1');
  const visible = () => (only === 'none' ? tags.filter((t) => !byID.has(t.record_id)) : tags);

  // reload re-reads the tags after a rename or delete.
  async function reload() {
    tags = (await get('api/ha/tags')).sort(byScanned);
    sync();
  }

  // Row actions sit inside a clickable list item (see searchList); stop their clicks there.
  const stop = (ev) => ev.stopPropagation();

  // tagRow shows one tag. Rename swaps the title for an input that saves the
  // name to the HA tag, so the tag and its record share it.
  function tagRow(t) {
    const li = h('div');
    const rec = byID.get(t.record_id);
    const info = h('div', { class: 'muted small', text: `${t.id} · ${t.last_scanned ? `scanned ${fmtTime(t.last_scanned)}` : 'never scanned'}` });
    const show = (pos) => li.replaceChildren(h('div', { class: 'row' },
      rec ? thumb(rec, 32) : h('span', { class: 'thumb s32' }),
      h('div', { class: 'grow' },
        h('div', { class: 'title' }, t.name ? highlight(t.name, pos) : '(no name)'),
        info,
        rec ? h('div', { class: 'small' }, 'Plays ', h('a', { href: `#/records/edit/${encodeURIComponent(rec.id)}`, onclick: stop, text: rec.name }))
          : h('div', { class: 'muted small', text: 'No record' })),
      h('div', { class: 'actions', onclick: stop },
        !rec && h('a', { class: 'button primary', href: createLink(t), text: 'Create record' }),
        h('button', { type: 'button', text: 'Rename', onclick: edit }),
        h('button', { type: 'button', class: 'danger', text: 'Delete', onclick: (ev) => {
          if (!window.confirm(`Delete tag ${t.name || t.id} from Home Assistant?`)) return;
          run(ev.currentTarget, async () => {
            await del(`api/ha/tags/${encodeURIComponent(t.id)}`);
            toast(`Deleted ${t.name || t.id}`);
            await reload();
          });
        } }))));

    function edit() {
      const input = h('input', { type: 'text', value: t.name || '', maxlength: 200, 'aria-label': 'New tag name',
        placeholder: 'Artist — Album' });
      const saveBtn = h('button', { type: 'submit', class: 'primary', text: 'Save' });
      const form = h('form', { class: 'inline grow', onclick: stop, onsubmit: (ev) => {
        ev.preventDefault();
        const name = input.value.trim();
        if (!name) { toast('Enter a name.', 'error'); return; }
        if (name === t.name) { show(); return; }
        run(saveBtn, async () => {
          await put(`api/ha/tags/${encodeURIComponent(t.id)}`, { name });
          toast(`Renamed in Home Assistant: ${name}`);
          await reload();
        });
      } }, input, saveBtn, h('button', { type: 'button', text: 'Cancel', onclick: () => show() }));
      input.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') show(); });
      li.replaceChildren(h('div', { class: 'stack', onclick: stop }, form, info));
      input.focus();
      input.select();
    }
    return { li, show };
  }

  const sl = searchList({
    items: visible(),
    text: (t) => t.name || t.id,
    placeholder: 'Search tags (type anywhere)',
    empty: 'No HA tags yet. Scan a tag and it shows up here.',
    row: (t, pos) => {
      const { li, show } = tagRow(t);
      show(pos);
      return li;
    },
    onEnter: (t) => {
      const rec = byID.get(t.record_id);
      location.hash = rec ? `#/records/edit/${encodeURIComponent(rec.id)}` : createLink(t);
    },
  });

  const chip = (value, text) => h('button', { type: 'button', 'aria-pressed': String(only === value), text, onclick: () => {
    only = value;
    sync();
  } });
  const chips = h('div', { class: 'actions filters' });
  function sync() {
    title.textContent = `Tags (${tags.length})`;
    chips.replaceChildren(chip('all', 'All'), chip('none', `No record (${tags.filter((t) => !byID.has(t.record_id)).length})`));
    sl.setItems(visible());
  }
  sync();

  view.replaceChildren(
    h('div', { class: 'toolbar' }, title),
    chips,
    sl.el,
    h('details', { class: 'history' },
      h('summary', { text: `All recent scans (${scans.length})` }),
      h('div', { class: 'table-wrap' }, h('table', {},
        h('thead', {}, h('tr', {}, ['Time', 'Source', 'Tag or media', 'Area', 'Record', 'Player', 'Result'].map((c) => h('th', { text: c })))),
        h('tbody', {}, scans.map((s) => h('tr', { class: s.result },
          h('td', { text: fmtTime(s.time) }),
          h('td', { text: s.source || 'tag' }),
          h('td', {}, what(s)),
          h('td', { text: s.area_id || s.entity }),
          h('td', {}, s.record_id ? h('a', { href: `#/records/edit/${encodeURIComponent(s.record_id)}`, text: s.record_id }) : ''),
          h('td', { text: s.player }),
          h('td', { title: s.error || '', text: s.error ? `${s.result}: ${s.error}` : s.result }))))))),
  );
  focusIfDesktop(sl.input);
  return sl.destroy;
}
