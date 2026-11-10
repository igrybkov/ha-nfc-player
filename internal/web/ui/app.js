// Entry point: hash router. Each screen module exports
// render(view, params) and may return a cleanup function.
import * as tags from './tags.js';
import * as scans from './scans.js';
import * as print from './print.js';
import * as templates from './templates.js';
import * as backgrounds from './backgrounds.js';
import * as settings from './settings.js';
import { h } from './lib.js';

const screens = { tags, scans, print, templates, backgrounds, settings };
let cleanup = null;

// parseHash turns "#/tags/edit?name=x" into {screen, rest, params}.
function parseHash() {
  const [path, query = ''] = location.hash.replace(/^#\/?/, '').split('?');
  const [screen, ...rest] = path.split('/').map(decodeURIComponent);
  return { screen: screen || 'tags', rest, params: new URLSearchParams(query) };
}

async function route() {
  const { screen, rest, params } = parseHash();
  const mod = screens[screen] || tags;
  if (cleanup) cleanup();
  cleanup = null;
  for (const a of document.querySelectorAll('#nav a')) {
    a.toggleAttribute('aria-current', a.dataset.screen === screen);
  }
  const view = document.getElementById('view');
  view.replaceChildren(h('p', { class: 'muted', text: 'Loading…' }));
  try {
    cleanup = (await mod.render(view, rest, params)) || null;
  } catch (e) {
    view.replaceChildren(h('p', { class: 'error', text: `Could not load: ${e.message}` }));
  }
}

window.addEventListener('hashchange', route);
route();
