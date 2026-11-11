// Small helpers shared by the screens: DOM building, API calls, toasts and
// the keyboard-driven search list.
import { filter } from './fuzzy.js';

// h builds an element. attrs: class, text, on<event>, and any property or
// attribute. Children may be nodes, strings, arrays, null or false.
export function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k === 'text') el.textContent = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k in el && typeof v !== 'string') el[k] = v;
    else if (k === 'value' || k === 'checked' || k === 'selected' || k === 'disabled') el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c == null || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else el.append(c instanceof Node ? c : String(c));
  }
}

// API calls use relative URLs only, so they work under Ingress. signal
// (an AbortSignal) cancels the request.
export async function api(method, path, body, signal) {
  const opts = { method, headers: {}, signal };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const isJSON = (res.headers.get('Content-Type') || '').includes('application/json');
  const data = isJSON ? await res.json() : null;
  if (!res.ok) throw new Error((data && data.error) || `${res.status} ${res.statusText}`);
  return data;
}

export const get = (p) => api('GET', p);
export const post = (p, b, signal) => api('POST', p, b, signal);
export const put = (p, b) => api('PUT', p, b);
export const del = (p) => api('DELETE', p);

export function toast(msg, kind = 'ok') {
  const el = h('div', { class: `toast ${kind}`, role: 'status', text: msg });
  document.getElementById('toasts').append(el);
  setTimeout(() => el.remove(), kind === 'error' ? 6000 : 2500);
}

// run wraps an async action: shows errors as toasts and disables the button.
export async function run(btn, fn) {
  if (btn) btn.disabled = true;
  try {
    return await fn();
  } catch (e) {
    toast(e.message, 'error');
  } finally {
    if (btn) btn.disabled = false;
  }
}

// highlight wraps matched characters in <mark>.
export function highlight(text, positions) {
  if (!positions || !positions.length) return text;
  const set = new Set(positions);
  const out = [];
  let buf = '';
  let inMark = false;
  for (let i = 0; i < text.length; i++) {
    const m = set.has(i);
    if (m !== inMark) {
      if (buf) out.push(inMark ? h('mark', {}, buf) : buf);
      buf = '';
      inMark = m;
    }
    buf += text[i];
  }
  if (buf) out.push(inMark ? h('mark', {}, buf) : buf);
  return out;
}

// splitName mirrors the label split: "Artist — Album" → [artist, album].
export function splitName(name) {
  const i = name.indexOf('—');
  return i < 0 ? [name.trim(), ''] : [name.slice(0, i).trim(), name.slice(i + 1).trim()];
}

// normalizeName mirrors model.NormalizeName.
export const normalizeName = (n) => n.split('|')[0].trim().toLowerCase();

// artBumps holds, per entry id, when "Refresh art" last loaded new art.
const artBumps = new Map();

// bumpArt marks an entry's art as changed. Browsers reuse an image URL the
// page already loaded without asking the server, so the next artURL for the
// entry must differ. After a reload, the server's ETag catches the change.
export function bumpArt(id) {
  artBumps.set(id, Date.now());
}

// artURL changes when the art source (url, pool, override) or a refresh
// changes it. A pool entry's art comes from the pool's first item, so pool
// saves bump it too (see pools.js).
export function artURL(entry, size) {
  const src = entry.artKey ?? `${entry.url}|${entry.label?.artwork ?? ''}`;
  const v = hash(entry.pool ? `${src}|${entry.pool}` : src);
  const bump = artBumps.get(entry.id);
  const base = entry.artPath ?? `api/tags/${encodeURIComponent(entry.id)}/artwork`;
  return `${base}?size=${size}&v=${v}${bump ? `&r=${bump}` : ''}`;
}

// poolEntry shows a pool where an entry is expected: in thumbnails, and in
// the print picker. Its id is the print ref ("pool:<id>"); its art comes
// from the pool's artwork route and changes with its override or items.
export function poolEntry(p) {
  return {
    id: `pool:${p.id}`, name: p.name, url: '', pool: p.id, isPool: true,
    label: p.label ?? { title: null, subtitle: null, artwork: null },
    artPath: `api/pools/${encodeURIComponent(p.id)}/artwork`,
    artKey: JSON.stringify([p.label?.artwork ?? '', p.items ?? []]),
  };
}

function hash(s) {
  let x = 0;
  for (let i = 0; i < s.length; i++) x = (x * 31 + s.charCodeAt(i)) | 0;
  return (x >>> 0).toString(36);
}

// Thumbnails are reused by URL, so a list re-render (search, selection)
// moves the loaded image instead of fetching it again. A node that is on the
// page, or already handed out in this render, is never reused: two copies
// of one entry (print copies, a tag twice in a pool) get two elements.
const thumbs = new Map(); // src → img[]
const claimed = new Set(); // handed out since the current task began

export function thumb(entry, size = 48) {
  const src = artURL(entry, size * 2);
  if (!claimed.size) queueMicrotask(() => claimed.clear());
  const pool = thumbs.get(src) ?? [];
  let img = pool.find((n) => !n.isConnected && !claimed.has(n));
  if (!img) {
    img = h('img', { class: 'thumb', loading: 'lazy', alt: '', width: size, height: size, src });
    img.addEventListener('error', () => img.classList.add('missing'), { once: true });
    pool.push(img);
    thumbs.set(src, pool);
  }
  claimed.add(img);
  return img;
}

const isTyping = (el) => el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName));

// searchList is a fuzzy-searchable list driven by the keyboard.
// Arrow keys move, Enter calls onEnter, and letters typed anywhere on the
// page go to the search box. Returns {el, input, refresh, shown, setItems, destroy}.
export function searchList({ items, text, row, onEnter, placeholder = 'Search', empty = 'Nothing here yet.' }) {
  let all = items;
  let shown = [];
  let active = 0;
  const input = h('input', { type: 'search', class: 'search', placeholder, autocomplete: 'off', 'aria-label': placeholder });
  const list = h('ul', { class: 'list', role: 'listbox' });

  function render() {
    shown = filter(all, input.value, text);
    active = Math.min(active, Math.max(shown.length - 1, 0));
    // Detach the old rows first so their thumbnails can be reused.
    list.replaceChildren();
    list.replaceChildren(...shown.map((r, i) => {
      const li = h('li', {
        class: i === active ? 'active' : '',
        role: 'option',
        'aria-selected': String(i === active),
        onclick: () => { active = i; mark(); onEnter(r.item); },
      }, row(r.item, r.positions));
      return li;
    }));
    if (!shown.length) list.append(h('li', { class: 'empty', text: all.length ? 'No match.' : empty }));
  }

  function mark() {
    [...list.children].forEach((li, i) => {
      li.classList.toggle('active', i === active);
      li.setAttribute('aria-selected', String(i === active));
    });
    list.children[active]?.scrollIntoView({ block: 'nearest' });
  }

  function move(d) {
    if (!shown.length) return;
    active = (active + d + shown.length) % shown.length;
    mark();
  }

  input.addEventListener('input', () => { active = 0; render(); });

  function onKey(e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const inSearch = document.activeElement === input;
    if (!inSearch && isTyping(document.activeElement)) return;
    if (!inSearch && document.activeElement?.closest('button, a, summary')) return;
    if (document.querySelector('dialog[open]')) return;
    if (e.key === 'ArrowDown') { e.preventDefault(); move(1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); move(-1); }
    else if (e.key === 'Enter' && shown[active]) { e.preventDefault(); onEnter(shown[active].item); }
    else if (e.key === 'Escape' && inSearch) { input.value = ''; render(); }
    else if (!inSearch && e.key.length === 1 && e.key !== ' ') {
      // A printable key outside any field starts a search.
      input.focus();
    }
  }
  document.addEventListener('keydown', onKey);

  render();
  return {
    el: h('div', { class: 'search-list' }, input, list),
    input,
    refresh: render,
    shown: () => shown.map((r) => r.item),
    setItems(next) { all = next; render(); },
    destroy() { document.removeEventListener('keydown', onKey); },
  };
}

// field wraps a control with a label.
export function field(label, control, hint) {
  return h('label', { class: 'field' }, h('span', { text: label }), control, hint && h('small', { text: hint }));
}

export function num(value, attrs = {}) {
  return h('input', { type: 'number', inputmode: 'decimal', value: value ?? '', ...attrs });
}

// readNum returns a number, or null for an empty input.
export function readNum(input) {
  const v = input.value.trim();
  return v === '' ? null : Number(v);
}

export function select(options, value, attrs = {}) {
  return h('select', attrs, options.map(([v, label]) => h('option', { value: v, selected: v === value }, label)));
}

const KNOWN_PLAYERS = ['sonos', 'music_assistant'];

// playerNames lists the players to offer as an override: the settings
// chain first, then any known player not in it.
export function playerNames(settings) {
  return [...new Set([...settings.players.default, ...KNOWN_PLAYERS])];
}

// areaSelect is a select of HA areas. It fills in when api/ha/areas answers.
export function areaSelect() {
  const sel = h('select', { 'aria-label': 'Area' }, h('option', { value: '', text: 'Loading areas…' }));
  get('api/ha/areas').then((areas) => {
    sel.replaceChildren(...areas.map((a) => h('option', { value: a.id }, a.name)));
  }).catch((e) => sel.replaceChildren(h('option', { value: '', text: `Areas unavailable: ${e.message}` })));
  return sel;
}

// focusIfDesktop focuses a field unless the device is touch-first, where it
// would pop up the on-screen keyboard.
export function focusIfDesktop(el) {
  if (window.matchMedia('(pointer: fine)').matches) el.focus();
}

export function confirmDelete(what) {
  return window.confirm(`Delete ${what}? This cannot be undone.`);
}

// openPDF posts the job as a form into a new tab. Blob URLs and window.open
// are unreliable in the HA companion app webview; a plain form post works.
export function openPDF(job) {
  const form = h('form', { method: 'post', action: 'api/print/pdf', target: '_blank', hidden: true },
    h('input', { type: 'hidden', name: 'job', value: JSON.stringify(job) }));
  document.body.append(form);
  form.submit();
  form.remove();
}

// sized adds a size (px) to a picker preview URL.
export const sized = (preview, size) => `${preview}${preview.includes('?') ? '&' : '?'}size=${size}`;

// artPicker offers images for an artwork field: the media's cover, then
// the artists, albums and playlists its page links to, or a pool's item
// art. More load as the grid scrolls into view; an image already shown is
// not shown again. A pick fills input with the image URL and calls onPick
// with a preview URL; "Automatic" clears the input. Nothing saves until
// the form does.
export function artPicker({ path, input, onPick }) {
  const grid = h('div', { class: 'picks' });
  const status = h('p', { class: 'muted small', hidden: true });
  const sentinel = h('div', { 'aria-hidden': 'true' });
  const box = h('div', { class: 'picks-box', hidden: true }, grid, sentinel);
  const shown = new Set();
  let next = 0; // offset of the next page; -1 = all loaded
  let loading = false;
  const mark = () => {
    for (const b of grid.querySelectorAll('button')) {
      b.setAttribute('aria-pressed', String(b.dataset.url === input.value.trim()));
    }
  };
  const tile = (url, preview, label) => h('button', {
    type: 'button', class: 'pick', 'data-url': url, title: label,
    onclick: () => { input.value = url; onPick(preview); mark(); },
  }, preview ? h('img', { src: sized(preview, 192), alt: '', loading: 'lazy', width: 96, height: 96 }) : h('span', { class: 'pick-auto', text: 'Auto' }),
  h('span', { class: 'small', text: label }));
  const say = (text) => { status.textContent = text; status.hidden = !text; };

  async function more() {
    if (loading || next < 0) return;
    loading = true;
    say(shown.size ? 'Loading more…' : 'Looking for images…');
    try {
      const sep = path.includes('?') ? '&' : '?';
      const page = await get(`${path}${sep}offset=${next}`);
      for (const c of page.items) {
        if (shown.has(c.url)) continue;
        shown.add(c.url);
        grid.append(tile(c.url, c.preview, c.kind === 'cover' ? 'Cover' : c.label));
      }
      next = page.next;
      mark();
      say(next < 0 && !shown.size ? 'No images found.' : '');
    } catch (e) {
      say(e.message);
      next = -1;
    } finally {
      loading = false;
    }
    // A short page leaves the sentinel in view, which fires no new event.
    if (next >= 0 && !box.hidden && sentinel.getBoundingClientRect().top < box.getBoundingClientRect().bottom + 200) more();
  }
  const io = new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting)) more(); }, { root: box, rootMargin: '200px' });

  const toggle = h('button', { type: 'button', text: 'Choose image…', onclick: () => {
    box.hidden = !box.hidden;
    if (box.hidden) { io.disconnect(); return; }
    if (!grid.childElementCount) grid.append(tile('', null, 'Automatic'));
    io.observe(sentinel);
  } });
  input.addEventListener('input', mark);
  return h('div', { class: 'stack' }, toggle, box, status);
}
