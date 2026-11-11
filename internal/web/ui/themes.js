// Themes screen: label colors and, per label format, where the art goes.
// The editor previews the theme on every template, rendered by the server
// with the same code that prints.
import { h, get, post, put, del, toast, run, field, select, confirmDelete, byName } from './lib.js';

const FORMAT_NAMES = { small: 'Small labels', large: 'Large labels', dot: 'Dots (round)' };
const ART_NAMES = { left: 'Art left', right: 'Art right', top: 'Art on top', bottom: 'Art at the bottom', background: 'Art behind the text', none: 'No art' };
const COVER_NAMES = { '': 'no art', pale: 'pale cover', dark: 'dark cover', busy: 'busy cover' };

// Preview text, kept while the app is open.
const sampleText = { title: 'Artist Name', subtitle: 'Album Title' };

export async function render(view, rest, params) {
  const [all, settings, info] = await Promise.all([get('api/themes'), get('api/settings'), get('api/label-formats')]);
  if (rest[0] === 'edit' && rest[1]) return editor(view, all.find((b) => b.id === rest[1]), false, info);
  if (rest[0] === 'new') {
    const from = all.find((b) => b.id === params.get('from'));
    const b = from
      ? { ...structuredClone(from), id: '', name: `${from.name} (copy)`, builtin: false }
      : { id: '', name: '', builtin: false, background: '#1E3A5F', text: '#FFFFFF', fade: null, fade_text: null, formats: {} };
    return editor(view, b, true, info);
  }
  // The theme prints start with; a deleted one falls back to white.
  const saved = settings.print.default_theme;
  const def = all.some((b) => b.id === saved) ? saved : 'white';
  const makeDefault = (b) => (ev) => run(ev.currentTarget, async () => {
    await put(`api/print/default-theme/${encodeURIComponent(b.id)}`, {});
    toast(`${b.name} is the default theme`);
    await render(view, rest, params);
  });
  const previews = new Previews();
  const signal = previews.round();
  view.replaceChildren(
    h('div', { class: 'toolbar' }, h('h1', { text: 'Themes' }),
      h('a', { class: 'button primary', href: '#/themes/new', text: 'New theme' })),
    h('ul', { class: 'list plain' }, all.map((b) => {
      const img = h('img', { class: 'label-preview', width: 96, alt: `${b.name} on a small label` });
      previews.load(img, { theme: b, format: 'small', cover: 'dark', ...sampleText, px_per_in: 128 }, signal);
      return h('li', {},
        h('div', { class: 'row' },
          img,
          h('div', { class: 'grow' },
            h('a', { class: 'title', href: `#/themes/edit/${encodeURIComponent(b.id)}`, text: b.name }),
            h('div', { class: 'muted small' }, b.id, b.builtin && h('span', { class: 'badge', text: 'built-in' }),
              b.id === def && h('span', { class: 'badge', text: 'default' }))),
          b.id !== def && h('button', { type: 'button', text: 'Make default', onclick: makeDefault(b) }),
          h('a', { class: 'button', href: `#/themes/new?from=${encodeURIComponent(b.id)}`, text: 'Duplicate' })));
    })),
  );
  return () => previews.close();
}

// Previews loads label PNGs from the server. Each round of requests aborts
// the last one, so fast color changes don't pile up.
class Previews {
  constructor() { this.ctl = null; this.urls = new Set(); }

  // round aborts the requests in flight and returns a signal for new ones.
  round() {
    this.ctl?.abort();
    this.ctl = new AbortController();
    return this.ctl.signal;
  }

  async load(img, body, signal) {
    try {
      const res = await fetch('api/themes/preview', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal,
      });
      if (!res.ok) {
        const data = await res.json().catch(() => null);
        throw new Error(data?.error || `${res.status} ${res.statusText}`);
      }
      const url = URL.createObjectURL(await res.blob());
      this.urls.add(url);
      const old = img.dataset.url;
      img.src = url;
      img.dataset.url = url;
      img.title = '';
      img.classList.remove('failed');
      if (old) {
        URL.revokeObjectURL(old);
        this.urls.delete(old);
      }
    } catch (err) {
      if (err.name === 'AbortError') return;
      img.classList.add('failed');
      img.title = err.message;
    }
  }

  close() {
    this.ctl?.abort();
    for (const u of this.urls) URL.revokeObjectURL(u);
    this.urls.clear();
  }
}

async function editor(view, b, isNew, info) {
  if (!b) {
    view.replaceChildren(h('p', { class: 'error', text: 'No such theme.' }), h('a', { href: '#/themes', text: 'Back' }));
    return;
  }
  const templates = await get('api/templates');
  const ro = b.builtin;
  // Every format gets an explicit style, so what the editor shows is saved.
  b.formats ??= {};
  for (const f of info.formats) {
    b.formats[f.id] = { art: b.formats[f.id]?.art || f.default_art, mono: !!b.formats[f.id]?.mono };
  }

  // Unsaved changes: the theme compared with what was loaded.
  let saved = null;
  const dirtyBadge = h('span', { class: 'badge dirty', text: 'Unsaved changes', hidden: true });
  const checkDirty = () => {
    const dirty = saved !== null && JSON.stringify(b) !== saved;
    dirtyBadge.hidden = !dirty;
    return dirty;
  };
  const onLeave = (ev) => { if (checkDirty()) ev.preventDefault(); };

  // Previews: one row per template, with no art and each sample cover. A
  // format with no template shows its reference size.
  const previews = new Previews();
  const rows = [];
  for (const f of info.formats) {
    const own = byName(templates.filter((t) => t.format === f.id));
    for (const t of own) rows.push({ format: f.id, template: t.id, label: t.label, title: t.name });
    if (!own.length) {
      rows.push({ format: f.id, template: '', label: f.label, title: `No template yet, ${f.label.width_in}×${f.label.height_in} in` });
    }
  }
  const covers = ['', ...info.covers];
  const PX_PER_IN = 72; // on screen; the PNG has twice the pixels, for sharpness
  const previewBox = h('div', { class: 'stack' }, rows.map((row) => {
    row.imgs = covers.map((cover) => h('img', {
      class: 'label-preview',
      width: Math.round(row.label.width_in * PX_PER_IN),
      alt: `${row.title}, ${COVER_NAMES[cover]}`,
    }));
    return h('section', {},
      h('h3', { class: 'preview-title' }, row.title, h('span', { class: 'muted', text: ` · ${FORMAT_NAMES[row.format]}` })),
      h('div', { class: 'preview-row' }, row.imgs));
  }));
  let timer = 0;
  const redraw = (now = false) => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      const signal = previews.round();
      for (const row of rows) {
        row.imgs.forEach((img, i) => previews.load(img, {
          theme: b, template_id: row.template, format: row.format, cover: covers[i], ...sampleText, px_per_in: 2 * PX_PER_IN,
        }, signal));
      }
    }, now ? 0 : 200);
  };

  const name = h('input', { value: b.name, required: true, disabled: ro });
  name.addEventListener('input', () => { b.name = name.value; });
  const color = (key, fallback) => {
    const input = h('input', { type: 'color', value: (b[key] || fallback).toLowerCase(), disabled: ro });
    input.addEventListener('input', () => { b[key] = input.value.toUpperCase(); redraw(); });
    return input;
  };
  const bg = color('background', '#FFFFFF');
  const fg = color('text', '#000000');

  // The fade and its text only matter where art sits behind the text.
  const fade = color('fade', '#000000');
  const blackFade = h('input', { type: 'checkbox', checked: b.fade == null, disabled: ro });
  blackFade.addEventListener('change', () => {
    fade.disabled = ro || blackFade.checked;
    b.fade = blackFade.checked ? null : fade.value.toUpperCase();
    redraw();
  });
  fade.disabled = ro || blackFade.checked;
  const fadeText = color('fade_text', '#FFFFFF');
  const whiteFadeText = h('input', { type: 'checkbox', checked: b.fade_text == null, disabled: ro });
  whiteFadeText.addEventListener('change', () => {
    fadeText.disabled = ro || whiteFadeText.checked;
    b.fade_text = whiteFadeText.checked ? null : fadeText.value.toUpperCase();
    redraw();
  });
  fadeText.disabled = ro || whiteFadeText.checked;
  const fadeBox = h('fieldset', {}, h('legend', { text: 'Text over art' }),
    h('p', { class: 'muted small', text: 'Where the art sits behind the text, a fade under the text keeps it readable.' }),
    h('div', { class: 'grid2' }, field('Fade', fade), field('Text', fadeText)),
    h('label', { class: 'check' }, blackFade, ' Black fade'),
    h('label', { class: 'check' }, whiteFadeText, ' White text'));
  const syncFadeBox = () => { fadeBox.hidden = !Object.values(b.formats).some((s) => s.art === 'background'); };

  const formatBoxes = info.formats.map((f) => {
    const style = b.formats[f.id];
    const art = select(info.placements.map((p) => [p, ART_NAMES[p] ?? p]), style.art, { disabled: ro });
    art.addEventListener('change', () => { style.art = art.value; syncFadeBox(); redraw(); });
    const mono = h('input', { type: 'checkbox', checked: style.mono, disabled: ro });
    mono.addEventListener('change', () => { style.mono = mono.checked; redraw(); });
    return h('fieldset', {}, h('legend', { text: FORMAT_NAMES[f.id] ?? f.id }),
      field('Art', art),
      h('label', { class: 'check' }, mono, ' Gray art'));
  });

  // The preview text is not part of the theme.
  const noFill = { autocomplete: 'off', 'data-1p-ignore': '', 'data-lpignore': 'true' };
  const title = h('input', { value: sampleText.title, ...noFill });
  const subtitle = h('input', { value: sampleText.subtitle, ...noFill });
  for (const [input, key] of [[title, 'title'], [subtitle, 'subtitle']]) {
    input.addEventListener('input', () => { sampleText[key] = input.value; redraw(); });
  }

  const save = h('button', { type: 'submit', class: 'primary', text: isNew ? 'Create' : 'Save', disabled: ro });
  const remove = !ro && !isNew && h('button', { type: 'button', class: 'danger', text: 'Delete', onclick: (ev) => {
    if (!confirmDelete(b.name)) return;
    run(ev.currentTarget, async () => {
      await del(`api/themes/${encodeURIComponent(b.id)}`);
      saved = null;
      toast(`Deleted ${b.name}`);
      location.hash = '#/themes';
    });
  } });
  view.replaceChildren(h('form', { class: 'editor', oninput: checkDirty, onchange: checkDirty, onsubmit: (ev) => {
    ev.preventDefault();
    run(save, async () => {
      const done = isNew ? await post('api/themes', b) : await put(`api/themes/${encodeURIComponent(b.id)}`, b);
      saved = null;
      toast(`Saved ${done.name}`);
      const to = `#/themes/edit/${encodeURIComponent(done.id)}`;
      // Same id: the hash stays, so ask the router to reload the screen.
      if (location.hash === to) window.dispatchEvent(new HashChangeEvent('hashchange'));
      else location.hash = to;
    });
  } },
  h('div', { class: 'toolbar sticky-top' },
    h('a', { href: '#/themes', class: 'button', text: '← Themes' }),
    h('h1', { text: isNew ? 'New theme' : b.name }),
    dirtyBadge, save, remove),
  ro && h('p', { class: 'note' }, 'Built-in themes are read-only. ',
    h('a', { href: `#/themes/new?from=${encodeURIComponent(b.id)}`, text: 'Duplicate it' }), ' to make changes.'),
  h('div', { class: 'cols' },
    h('div', { class: 'stack' },
      field('Name', name),
      h('div', { class: 'grid2' }, field('Background', bg), field('Text', fg)),
      formatBoxes,
      fadeBox,
      h('p', { class: 'muted small', text: 'Non-white backgrounds bleed past the label edge when printed.' })),
    h('div', { class: 'stack' },
      h('h2', { text: 'Preview' }),
      h('div', { class: 'grid2' }, field('Title', title), field('Subtitle', subtitle)),
      previewBox))));
  syncFadeBox();
  redraw(true);
  saved = JSON.stringify(b);
  window.addEventListener('beforeunload', onLeave);
  return () => {
    clearTimeout(timer);
    previews.close();
    window.removeEventListener('beforeunload', onLeave);
  };
}
