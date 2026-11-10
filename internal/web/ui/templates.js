// Templates screen: built-in and user label templates, editor with a live
// outline preview.
import { h, get, post, put, del, toast, run, field, num, readNum, select, confirmDelete, openPDF } from './lib.js';

export async function render(view, rest, params) {
  const templates = await get('api/templates');
  if (rest[0] === 'edit' && rest[1]) return editor(view, templates.find((t) => t.id === rest[1]), false);
  if (rest[0] === 'new') {
    const from = templates.find((t) => t.id === params.get('from')) || templates[0];
    const copy = structuredClone(from);
    copy.id = '';
    copy.name = `${from.name} (copy)`;
    copy.builtin = false;
    return editor(view, copy, true);
  }
  view.replaceChildren(
    h('div', { class: 'toolbar' }, h('h1', { text: 'Templates' }),
      h('a', { class: 'button primary', href: '#/templates/new', text: 'New template' })),
    h('ul', { class: 'list plain' }, templates.map((t) => h('li', {},
      h('div', { class: 'row' },
        h('div', { class: 'grow' },
          h('a', { class: 'title', href: `#/templates/edit/${encodeURIComponent(t.id)}`, text: t.name }),
          h('div', { class: 'muted small' },
            `${t.id} · ${t.grid.cols}×${t.grid.rows} · ${t.label.width_in}×${t.label.height_in} in · ${t.layout}`,
            t.builtin && h('span', { class: 'badge', text: 'built-in' }))),
        h('a', { class: 'button', href: `#/templates/new?from=${encodeURIComponent(t.id)}`, text: 'Duplicate' }))))),
  );
}

// The editable numbers: [label, path, step].
const NUMBERS = [
  ['Page width (in)', 'page.width_in', 0.0625], ['Page height (in)', 'page.height_in', 0.0625],
  ['Label width (in)', 'label.width_in', 0.0625], ['Label height (in)', 'label.height_in', 0.0625],
  ['Columns', 'grid.cols', 1], ['Rows', 'grid.rows', 1],
  ['Left margin (in)', 'margin.left_in', 0.03125], ['Top margin (in)', 'margin.top_in', 0.03125],
  ['Column gap (in)', 'gap.col_in', 0.03125], ['Row gap (in)', 'gap.row_in', 0.03125],
  ['Title size (pt)', 'font.title_pt', 0.5], ['Subtitle size (pt)', 'font.subtitle_pt', 0.5],
];

const getPath = (o, p) => p.split('.').reduce((v, k) => v[k], o);
const setPath = (o, p, v) => { const ks = p.split('.'); ks.slice(0, -1).reduce((x, k) => x[k], o)[ks.at(-1)] = v; };

// problems mirrors the server checks so the user sees them while typing.
function problems(t) {
  const out = [];
  for (const [label, path] of NUMBERS) {
    const v = getPath(t, path);
    if (!(v >= 0) || (v === 0 && !path.startsWith('margin') && !path.startsWith('gap'))) out.push(`${label} must be positive.`);
  }
  const w = t.margin.left_in + t.grid.cols * t.label.width_in + (t.grid.cols - 1) * t.gap.col_in;
  const hgt = t.margin.top_in + t.grid.rows * t.label.height_in + (t.grid.rows - 1) * t.gap.row_in;
  if (w > t.page.width_in + 1e-9) out.push(`Labels are ${w.toFixed(3)} in wide; the page is ${t.page.width_in} in.`);
  if (hgt > t.page.height_in + 1e-9) out.push(`Labels are ${hgt.toFixed(3)} in tall; the page is ${t.page.height_in} in.`);
  return out;
}

const SVG = 'http://www.w3.org/2000/svg';
function svg(tag, attrs) {
  const el = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  return el;
}

function outline(t) {
  const root = svg('svg', { viewBox: `0 0 ${t.page.width_in} ${t.page.height_in}`, class: 'outline', role: 'img', 'aria-label': 'Sheet outline' });
  root.append(svg('rect', { x: 0, y: 0, width: t.page.width_in, height: t.page.height_in, class: 'page' }));
  const ok = t.grid.cols > 0 && t.grid.rows > 0 && t.grid.cols * t.grid.rows <= 1000;
  for (let r = 0; ok && r < t.grid.rows; r++) {
    for (let c = 0; c < t.grid.cols; c++) {
      root.append(svg('rect', {
        x: t.margin.left_in + c * (t.label.width_in + t.gap.col_in),
        y: t.margin.top_in + r * (t.label.height_in + t.gap.row_in),
        width: t.label.width_in, height: t.label.height_in, rx: 0.04, class: 'label',
      }));
    }
  }
  return root;
}

async function editor(view, tpl, isNew) {
  if (!tpl) {
    view.replaceChildren(h('p', { class: 'error', text: 'No such template.' }), h('a', { href: '#/templates', text: 'Back' }));
    return;
  }
  const t = structuredClone(tpl);
  const ro = t.builtin;
  const layouts = await get('api/layouts').catch(() => [{ id: t.layout, name: t.layout }]);

  const preview = h('div', { class: 'outline-box' });
  const issues = h('ul', { class: 'error small' });
  const redraw = () => {
    preview.replaceChildren(outline(t));
    issues.replaceChildren(...problems(t).map((p) => h('li', { text: p })));
  };

  const name = h('input', { value: t.name, disabled: ro, required: true });
  name.addEventListener('input', () => { t.name = name.value; });
  const id = h('input', { value: t.id, disabled: !isNew, placeholder: 'From name', pattern: '[a-z0-9\\-]*' });
  const layout = select(layouts.map((l) => [l.id, l.name]), t.layout, { disabled: ro });
  layout.addEventListener('change', () => { t.layout = layout.value; });
  const numbers = NUMBERS.map(([label, path, step]) => {
    const input = num(getPath(t, path), { step, min: 0, disabled: ro });
    input.addEventListener('input', () => { setPath(t, path, readNum(input) ?? 0); redraw(); });
    return field(label, input);
  });

  const save = h('button', { type: 'submit', class: 'primary', text: isNew ? 'Create' : 'Save', disabled: ro });
  const form = h('form', { class: 'editor', onsubmit: (ev) => {
    ev.preventDefault();
    run(save, async () => {
      if (isNew) t.id = id.value.trim();
      const saved = isNew ? await post('api/templates', t) : await put(`api/templates/${encodeURIComponent(t.id)}`, t);
      toast(`Saved ${saved.name}`);
      location.hash = `#/templates/edit/${encodeURIComponent(saved.id)}`;
    });
  } },
  h('div', { class: 'toolbar' },
    h('a', { href: '#/templates', class: 'button', text: '← Templates' }),
    h('h1', { text: isNew ? 'New template' : t.name })),
  ro && h('p', { class: 'note' }, 'Built-in templates are read-only. ',
    h('a', { href: `#/templates/new?from=${encodeURIComponent(t.id)}`, text: 'Duplicate it' }), ' to make changes.'),
  h('div', { class: 'cols' },
    h('div', { class: 'stack' },
      field('Name', name), field('ID', id, isNew ? 'Optional. Lower case, digits and dashes.' : null),
      field('Layout', layout),
      h('div', { class: 'grid2' }, numbers),
      issues),
    h('div', { class: 'stack' }, preview,
      h('button', { type: 'button', text: 'PDF outline preview', onclick: () => {
        openPDF({ template: t, sample: t.grid.cols * t.grid.rows, background_id: 'white', outline: true });
      } }))),
  h('div', { class: 'actions' },
    save,
    !ro && !isNew && h('button', { type: 'button', class: 'danger', text: 'Delete', onclick: (ev) => {
      if (!confirmDelete(t.name)) return;
      run(ev.currentTarget, async () => {
        await del(`api/templates/${encodeURIComponent(t.id)}`);
        toast(`Deleted ${t.name}`);
        location.hash = '#/templates';
      });
    } })));
  view.replaceChildren(form);
  redraw();
}
