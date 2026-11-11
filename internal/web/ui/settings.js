// Settings screen: areas, player chain, readers, print values.
import { h, get, put, toast, run, field, num, readNum } from './lib.js';

const KNOWN_PLAYERS = ['sonos', 'music_assistant'];

const list = (s) => s.split(',').map((x) => x.trim()).filter(Boolean);

export async function render(view) {
  const [settings, areas] = await Promise.all([
    get('api/settings'),
    get('api/ha/areas').catch((e) => { toast(`Areas: ${e.message}`, 'error'); return []; }),
  ]);
  const s = structuredClone(settings);

  // Areas: every HA area plus any configured area HA no longer reports.
  const known = new Map(areas.map((a) => [a.id, a]));
  for (const id of Object.keys(s.rooms.areas)) {
    if (!known.has(id)) known.set(id, { id, name: `${id} (not in HA)`, sonos_entity: '', ma_entity: '' });
  }
  const areaRows = [...known.values()].map((a) => {
    const cur = s.rooms.areas[a.id] || {};
    const vol = num(cur.volume, { min: 0, max: 100, step: 1, placeholder: 'default', 'aria-label': `${a.name} volume` });
    const sonos = h('input', { value: cur.sonos_entity ?? '', placeholder: a.sonos_entity || 'auto', 'aria-label': `${a.name} Sonos entity` });
    const ma = h('input', { value: cur.ma_entity ?? '', placeholder: a.ma_entity || 'auto', 'aria-label': `${a.name} Music Assistant entity` });
    return { id: a.id, vol, sonos, ma, tr: h('tr', {}, h('th', { scope: 'row', text: a.name }), h('td', {}, vol), h('td', {}, sonos), h('td', {}, ma)) };
  });

  // Player chain with reorder.
  const chain = [...s.players.default];
  const chainList = h('ol', { class: 'chain' });
  const addPlayer = h('select', { 'aria-label': 'Add player' });
  const drawChain = () => {
    chainList.replaceChildren(...chain.map((p, i) => h('li', {},
      h('span', { class: 'grow', text: p }),
      h('button', { type: 'button', class: 'icon', text: '↑', 'aria-label': `Move ${p} up`, disabled: i === 0,
        onclick: () => { [chain[i - 1], chain[i]] = [chain[i], chain[i - 1]]; drawChain(); } }),
      h('button', { type: 'button', class: 'icon', text: '↓', 'aria-label': `Move ${p} down`, disabled: i === chain.length - 1,
        onclick: () => { [chain[i + 1], chain[i]] = [chain[i], chain[i + 1]]; drawChain(); } }),
      h('button', { type: 'button', class: 'icon', text: '×', 'aria-label': `Remove ${p}`, disabled: chain.length === 1,
        onclick: () => { chain.splice(i, 1); drawChain(); } }))));
    const missing = KNOWN_PLAYERS.filter((p) => !chain.includes(p));
    addPlayer.replaceChildren(h('option', { value: '', text: missing.length ? 'Add player…' : 'All players added' }),
      ...missing.map((p) => h('option', { value: p, text: p })));
    addPlayer.disabled = !missing.length;
  };
  addPlayer.addEventListener('change', () => { if (addPlayer.value) chain.push(addPlayer.value); drawChain(); });
  drawChain();

  const fallback = h('input', { type: 'checkbox', checked: s.players.fallback });
  // 0 in settings.yaml reads as unset and becomes 15, so the minimum is 1.
  const defaultVolume = num(s.rooms.default_volume, { min: 1, max: 100, step: 1 });
  const sonosPref = h('input', { value: s.rooms.sonos_model_preference.join(', ') });
  const integrations = h('input', { value: s.readers.integrations.join(', ') });
  const maEntry = h('input', { value: s.music_assistant.config_entry_id ?? '', placeholder: 'auto-detect' });
  const inset = num(s.print.inset_mm, { min: 0, max: 10, step: 0.1 });
  const bleed = num(s.print.bleed_mm, { min: 0, max: 10, step: 0.1 });
  const edge = num(s.print.edge_bleed_mm, { min: 0, max: 10, step: 0.1 });
  const outer = num(s.print.outer_bleed_mm ?? 1, { min: 0, max: 10, step: 0.1 });

  const offsetRows = Object.entries(s.print.offsets).map(([tid, o]) => {
    const x = num(o.x, { step: 0.05, 'aria-label': `${tid} X offset` });
    const y = num(o.y, { step: 0.05, 'aria-label': `${tid} Y offset` });
    const row = { tid, x, y, removed: false };
    row.tr = h('tr', {}, h('th', { scope: 'row', text: tid }), h('td', {}, x), h('td', {}, y),
      h('td', {}, h('button', { type: 'button', class: 'icon', text: '×', 'aria-label': `Remove ${tid} offset`,
        onclick: () => { row.removed = true; row.tr.remove(); } })));
    return row;
  });

  const collect = () => {
    const areasOut = {};
    for (const r of areaRows) {
      const a = { volume: readNum(r.vol), sonos_entity: r.sonos.value.trim() || null, ma_entity: r.ma.value.trim() || null };
      if (a.volume != null || a.sonos_entity || a.ma_entity) areasOut[r.id] = a;
    }
    const offsets = {};
    for (const r of offsetRows) if (!r.removed) offsets[r.tid] = { x: readNum(r.x) ?? 0, y: readNum(r.y) ?? 0 };
    return {
      ...s,
      readers: { integrations: list(integrations.value) },
      players: { default: chain, fallback: fallback.checked },
      rooms: { default_volume: readNum(defaultVolume) ?? 0, areas: areasOut, sonos_model_preference: list(sonosPref.value) },
      music_assistant: { config_entry_id: maEntry.value.trim() || null },
      // default_background is set on the Backgrounds screen; keep it.
      print: { ...s.print, offsets, inset_mm: readNum(inset) ?? 0, bleed_mm: readNum(bleed) ?? 0, edge_bleed_mm: readNum(edge) ?? 0, outer_bleed_mm: readNum(outer) ?? 0 },
    };
  };

  const save = h('button', { type: 'submit', class: 'primary', text: 'Save settings' });
  view.replaceChildren(h('form', { class: 'editor', onsubmit: (ev) => {
    ev.preventDefault();
    run(save, async () => {
      await put('api/settings', collect());
      toast('Settings saved');
    });
  } },
  h('div', { class: 'toolbar' }, h('h1', { text: 'Settings' })),
  h('fieldset', {}, h('legend', { text: 'Areas' }),
    field('Default volume %', defaultVolume, '1 to 100. Used in areas with no volume of their own.'),
    h('div', { class: 'table-wrap' }, h('table', {},
      h('thead', {}, h('tr', {}, ['Area', 'Volume %', 'Sonos entity', 'Music Assistant entity'].map((c) => h('th', { text: c })))),
      h('tbody', {}, areaRows.length ? areaRows.map((r) => r.tr) : h('tr', {}, h('td', { colspan: 4, class: 'muted', text: 'No areas.' }))))),
    h('p', { class: 'muted small', text: 'Leave a field empty to use the default. Placeholders show what is found in HA.' }),
    field('Sonos model preference', sonosPref, 'Comma-separated, best first.')),
  h('fieldset', {}, h('legend', { text: 'Players' }),
    chainList, addPlayer,
    h('label', { class: 'check' }, fallback, ' On error, try the next player'),
    field('Music Assistant config entry', maEntry)),
  h('fieldset', {}, h('legend', { text: 'Readers' }),
    field('Integrations', integrations, 'Only scans from devices of these integrations play. Comma-separated.')),
  h('fieldset', {}, h('legend', { text: 'Print' }),
    h('div', { class: 'grid3' }, field('Inset mm', inset), field('Bleed mm', bleed), field('Edge bleed mm', edge)),
    h('div', { class: 'grid3' }, field('Outer bleed mm', outer)),
    h('p', { class: 'muted small', text: 'Outer bleed: extra fill on the paper-edge side of the first and last columns.' }),
    h('div', { class: 'table-wrap' }, h('table', {},
      h('thead', {}, h('tr', {}, ['Template', 'X offset mm', 'Y offset mm', ''].map((c) => h('th', { text: c })))),
      h('tbody', {}, offsetRows.map((r) => r.tr)))),
    h('p', { class: 'muted small', text: 'Offsets are saved from the Print screen.' })),
  h('div', { class: 'actions sticky' }, save)));
}
