// Print screen: pick entries, set up the sheet, preview slots, open the PDF.
import {
  h, get, post, put, toast, run, highlight, thumb, searchList, field, num, readNum, select, openPDF, poolEntry,
} from './lib.js';
import { ALL } from './pools.js';

// Kept across visits so a half-done print setup survives a detour.
// background '' = the default background from settings.
const state = { selected: [], template: '', background: '', copies: 1, row: 1, col: 1, skip: 0 };

// editLink opens the editor of a picked entry or pool ("pool:<id>").
const editLink = (id) => (id.startsWith('pool:')
  ? `#/pools/edit/${encodeURIComponent(id.slice(5))}`
  : `#/tags/edit/${encodeURIComponent(id)}`);

export async function render(view) {
  const [tagEntries, { pools }, templates, backgrounds, settings] = await Promise.all([
    get('api/tags'), get('api/pools'), get('api/templates'), get('api/backgrounds'), get('api/settings'),
  ]);
  // Pools print too, under their own name: a tag with that name plays them.
  const entries = [...tagEntries, ...[...pools, { ...ALL, items: [] }].map(poolEntry)];
  const byID = new Map(entries.map((e) => [e.id, e]));
  state.selected = state.selected.filter((id) => byID.has(id));
  if (!templates.some((t) => t.id === state.template)) state.template = templates[0]?.id ?? '';
  if (!backgrounds.some((b) => b.id === state.background)) {
    const def = settings.print.default_background;
    state.background = (backgrounds.find((b) => b.id === def) ?? backgrounds.find((b) => b.id === 'white') ?? backgrounds[0])?.id ?? '';
  }
  const tpl = () => templates.find((t) => t.id === state.template);

  // Entry picker.
  const toggle = (e) => {
    const i = state.selected.indexOf(e.id);
    if (i < 0) state.selected.push(e.id); else state.selected.splice(i, 1);
    sl.refresh();
    drawSelected();
    schedulePreview();
  };
  const sl = searchList({
    items: entries,
    text: (e) => e.name,
    placeholder: 'Search entries (Enter toggles)',
    row: (e, pos) => h('div', { class: 'row' },
      h('input', { type: 'checkbox', checked: state.selected.includes(e.id), tabindex: -1, 'aria-hidden': 'true',
        onclick: (ev) => ev.preventDefault() }),
      thumb(e, 32),
      h('div', { class: 'grow title' }, highlight(e.name, pos)),
      e.isPool && h('span', { class: 'badge', text: 'pool' })),
    onEnter: toggle,
  });

  const selectedBox = h('div', { class: 'chips' });
  function drawSelected() {
    selectedBox.replaceChildren(
      h('span', { class: 'muted small', text: `${state.selected.length} selected` }),
      ...state.selected.map((id) => h('span', { class: 'chip' }, byID.get(id).name,
        h('button', { type: 'button', class: 'icon', text: '×', 'aria-label': `Remove ${byID.get(id).name}`,
          onclick: () => toggle(byID.get(id)) }))),
    );
  }
  const bulk = h('div', { class: 'actions' },
    h('button', { type: 'button', text: 'Select shown', onclick: () => {
      for (const e of sl.shown()) if (!state.selected.includes(e.id)) state.selected.push(e.id);
      sl.refresh(); drawSelected(); schedulePreview();
    } }),
    h('button', { type: 'button', text: 'Clear', onclick: () => {
      state.selected = []; sl.refresh(); drawSelected(); schedulePreview();
    } }));

  // Sheet options.
  const templateSel = select(templates.map((t) => [t.id, t.name]), state.template);
  const bgSel = select(backgrounds.map((b) => [b.id, b.name]), state.background);
  const copies = num(state.copies, { min: 1, max: 100, step: 1 });
  const skip = num(state.skip, { min: 0, step: 1 });
  const offX = num(0, { step: 0.05 });
  const offY = num(0, { step: 0.05 });
  const startLabel = h('span', { class: 'muted small' });
  const loadOffset = () => {
    const o = settings.print.offsets[state.template] || { x: 0, y: 0 };
    offX.value = o.x; offY.value = o.y;
  };
  loadOffset();

  templateSel.addEventListener('change', () => {
    state.template = templateSel.value;
    const g = tpl().grid;
    state.row = Math.min(state.row, g.rows); state.col = Math.min(state.col, g.cols);
    loadOffset(); schedulePreview();
  });
  bgSel.addEventListener('change', () => { state.background = bgSel.value; });
  for (const [input, key] of [[copies, 'copies'], [skip, 'skip']]) {
    input.addEventListener('input', () => { state[key] = Math.max(0, readNum(input) ?? 0); schedulePreview(); });
  }

  const job = (extra = {}) => ({
    entry_ids: state.selected,
    template_id: state.template,
    background_id: state.background,
    copies: Math.max(1, state.copies),
    start_row: state.row,
    start_col: state.col,
    skip: state.skip,
    offset: { x: readNum(offX) ?? 0, y: readNum(offY) ?? 0 },
    ...extra,
  });

  // Sheet map.
  const sheet = h('div', { class: 'sheets' });
  const summary = h('p', { class: 'muted small' });
  // The PDF opens in a new tab, so the page can't read its X-Missing-Art
  // header; this warning from the preview is what the user sees.
  const missingBox = h('div', { class: 'warning small', role: 'status', hidden: true });
  const showMissing = (missing) => {
    missingBox.hidden = !missing.length;
    if (!missing.length) { missingBox.replaceChildren(); return; }
    const n = missing.length;
    // Built through h(), which drops the false separator before the first link.
    missingBox.replaceChildren(h('span', {},
      h('strong', { text: `${n} entr${n === 1 ? 'y has' : 'ies have'} no art and will print without it: ` }),
      missing.map((m, i) => [i > 0 && ', ',
        h('a', { href: editLink(m.id), text: m.name })])));
  };
  let timer = 0;
  let seq = 0;
  // The preview looks up art, which can be slow when a lookup fails. Abort
  // the previous request so stale previews don't hold the connections that
  // thumbnails need; the server stops on the closed request.
  let inflight = null;
  function schedulePreview() {
    clearTimeout(timer);
    timer = setTimeout(preview, 150);
  }
  async function preview() {
    const t = tpl();
    if (!t) return;
    startLabel.textContent = `Start: row ${state.row}, column ${state.col}`;
    const my = ++seq;
    inflight?.abort();
    inflight = null;
    let pages;
    if (state.selected.length === 0) {
      // Nothing to print yet: show where printing would start.
      const start = (state.row - 1) * t.grid.cols + (state.col - 1);
      pages = [Array.from({ length: t.grid.cols * t.grid.rows }, (_, i) => (i < start ? 'used' : 'free'))];
      summary.textContent = 'Pick entries to see where labels land.';
      showMissing([]);
    } else {
      try {
        inflight = new AbortController();
        const r = await post('api/print/preview', job(), inflight.signal);
        if (my !== seq) return;
        pages = r.pages;
        summary.textContent = `${r.labels} label${r.labels === 1 ? '' : 's'} on ${pages.length} sheet${pages.length === 1 ? '' : 's'}.`;
        showMissing(r.missing_art || []);
      } catch (e) {
        if (my === seq && e.name !== 'AbortError') summary.textContent = e.message;
        return;
      }
    }
    // Copies of an entry print next to each other, so the n-th printed
    // slot holds selected[n / copies].
    const perEntry = Math.max(1, state.copies);
    let printed = 0;
    // Detach the old slots first so their thumbnails can be reused.
    sheet.replaceChildren();
    sheet.replaceChildren(...pages.map((slots, p) => h('div', { class: 'sheet-wrap' },
      pages.length > 1 && h('div', { class: 'muted small', text: `Sheet ${p + 1}` }),
      h('div', { class: 'sheet', style: `grid-template-columns: repeat(${t.grid.cols}, 1fr)` },
        slots.map((st, i) => {
          const row = Math.floor(i / t.grid.cols) + 1;
          const col = (i % t.grid.cols) + 1;
          const isStart = p === 0 && row === state.row && col === state.col;
          const e = st === 'printed' ? byID.get(state.selected[Math.floor(printed++ / perEntry)]) : null;
          const where = `Row ${row}, column ${col}`;
          return h('button', {
            type: 'button',
            class: `slot ${st}${isStart ? ' start' : ''}`,
            title: e ? `${where}: ${e.name}` : `${where}: ${st}`,
            'aria-label': e ? `${where}, ${e.name}` : `${where}, ${st}`,
            disabled: p > 0,
            onclick: () => { state.row = row; state.col = col; preview(); },
          }, e && thumb(e, 32), e && h('span', { class: 'slot-name', text: e.name }));
        })))));
  }

  const generate = h('button', { type: 'button', class: 'primary', text: 'Generate PDF', onclick: () => {
    if (!state.selected.length) { toast('Pick at least one entry.', 'error'); return; }
    openPDF(job());
  } });
  const saveOffsets = h('button', { type: 'button', text: 'Save offsets', onclick: (ev) => run(ev.currentTarget, async () => {
    const o = { x: readNum(offX) ?? 0, y: readNum(offY) ?? 0 };
    await put(`api/print/offsets/${encodeURIComponent(state.template)}`, o);
    settings.print.offsets[state.template] = o;
    toast(`Offsets saved for ${tpl().name}`);
  }) });

  view.replaceChildren(
    h('div', { class: 'toolbar' }, h('h1', { text: 'Print labels' })),
    h('div', { class: 'cols' },
      h('section', { class: 'stack' }, sl.el, bulk, selectedBox),
      h('section', { class: 'stack' },
        field('Template', templateSel),
        field('Background', bgSel),
        h('div', { class: 'grid2' }, field('Copies', copies), field('Skip slots', skip)),
        h('fieldset', {}, h('legend', { text: 'Offset (mm)' }),
          h('div', { class: 'grid2' }, field('X (right +)', offX), field('Y (up +)', offY)),
          saveOffsets),
        h('div', {}, h('strong', { text: 'Sheet' }), ' ', startLabel,
          h('p', { class: 'muted small', text: 'Click a slot to start there.' }),
          h('div', { class: 'legend small' },
            h('span', { class: 'slot used' }), 'used ', h('span', { class: 'slot printed' }), 'to print ',
            h('span', { class: 'slot free' }), 'free')),
        sheet, summary, missingBox,
        h('div', { class: 'actions' }, generate))),
  );
  drawSelected();
  preview();
  return () => { clearTimeout(timer); inflight?.abort(); sl.destroy(); };
}
