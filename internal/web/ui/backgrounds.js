// Backgrounds screen: label color schemes with color pickers.
import { h, get, post, put, del, toast, run, field, confirmDelete } from './lib.js';

export async function render(view, rest, params) {
  const all = await get('api/backgrounds');
  if (rest[0] === 'edit' && rest[1]) return editor(view, all.find((b) => b.id === rest[1]), false);
  if (rest[0] === 'new') {
    const from = all.find((b) => b.id === params.get('from'));
    const b = from
      ? { ...structuredClone(from), id: '', name: `${from.name} (copy)`, builtin: false }
      : { id: '', name: '', builtin: false, background: '#1E3A5F', text: '#FFFFFF', fade: null };
    return editor(view, b, true);
  }
  view.replaceChildren(
    h('div', { class: 'toolbar' }, h('h1', { text: 'Backgrounds' }),
      h('a', { class: 'button primary', href: '#/backgrounds/new', text: 'New background' })),
    h('ul', { class: 'list plain' }, all.map((b) => h('li', {},
      h('div', { class: 'row' },
        sample(b, true),
        h('div', { class: 'grow' },
          h('a', { class: 'title', href: `#/backgrounds/edit/${encodeURIComponent(b.id)}`, text: b.name }),
          h('div', { class: 'muted small' }, b.id, b.builtin && h('span', { class: 'badge', text: 'built-in' }))),
        h('a', { class: 'button', href: `#/backgrounds/new?from=${encodeURIComponent(b.id)}`, text: 'Duplicate' }))))),
  );
}

// sample draws a label mock-up: art area, then the fade into the background
// under the text, like the "full" layout.
function sample(b, small) {
  const fade = b.fade || b.background;
  return h('div', {
    class: `bg-sample${small ? ' small' : ''}`,
    style: `--s-bg:${b.background};--s-fg:${b.text};--s-fade:${fade}`,
  }, h('div', { class: 'bg-art' }), h('div', { class: 'bg-text' }, h('b', { text: 'Artist' }), h('span', { text: 'Album title' })));
}

function editor(view, b, isNew) {
  if (!b) {
    view.replaceChildren(h('p', { class: 'error', text: 'No such background.' }), h('a', { href: '#/backgrounds', text: 'Back' }));
    return;
  }
  const ro = b.builtin;
  const preview = h('div', { class: 'stack' });
  const redraw = () => preview.replaceChildren(sample(b, false));

  const name = h('input', { value: b.name, required: true, disabled: ro });
  name.addEventListener('input', () => { b.name = name.value; });
  const color = (key) => {
    const input = h('input', { type: 'color', value: (b[key] || b.background).toLowerCase(), disabled: ro });
    input.addEventListener('input', () => { b[key] = input.value.toUpperCase(); redraw(); });
    return input;
  };
  const bg = color('background');
  const fg = color('text');
  const fade = color('fade');
  const sameFade = h('input', { type: 'checkbox', checked: b.fade == null, disabled: ro });
  const syncFade = () => {
    fade.disabled = ro || sameFade.checked;
    b.fade = sameFade.checked ? null : fade.value.toUpperCase();
    redraw();
  };
  sameFade.addEventListener('change', syncFade);
  bg.addEventListener('input', () => { if (sameFade.checked) fade.value = bg.value; });
  fade.disabled = ro || sameFade.checked;

  const save = h('button', { type: 'submit', class: 'primary', text: isNew ? 'Create' : 'Save', disabled: ro });
  view.replaceChildren(h('form', { class: 'editor', onsubmit: (ev) => {
    ev.preventDefault();
    run(save, async () => {
      const saved = isNew ? await post('api/backgrounds', b) : await put(`api/backgrounds/${encodeURIComponent(b.id)}`, b);
      toast(`Saved ${saved.name}`);
      location.hash = `#/backgrounds/edit/${encodeURIComponent(saved.id)}`;
    });
  } },
  h('div', { class: 'toolbar' },
    h('a', { href: '#/backgrounds', class: 'button', text: '← Backgrounds' }),
    h('h1', { text: isNew ? 'New background' : b.name })),
  ro && h('p', { class: 'note' }, 'Built-in backgrounds are read-only. ',
    h('a', { href: `#/backgrounds/new?from=${encodeURIComponent(b.id)}`, text: 'Duplicate it' }), ' to make changes.'),
  h('div', { class: 'cols' },
    h('div', { class: 'stack' },
      field('Name', name),
      h('div', { class: 'grid3' }, field('Background', bg), field('Text', fg), field('Fade', fade)),
      h('label', { class: 'check' }, sameFade, ' Fade uses the background color'),
      h('p', { class: 'muted small', text: 'Non-white backgrounds bleed past the label edge when printed.' })),
    preview),
  h('div', { class: 'actions' },
    save,
    !ro && !isNew && h('button', { type: 'button', class: 'danger', text: 'Delete', onclick: (ev) => {
      if (!confirmDelete(b.name)) return;
      run(ev.currentTarget, async () => {
        await del(`api/backgrounds/${encodeURIComponent(b.id)}`);
        toast(`Deleted ${b.name}`);
        location.hash = '#/backgrounds';
      });
    } }))));
  redraw();
}
