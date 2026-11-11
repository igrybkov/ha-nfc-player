// Entry point: hash router. Each screen module exports
// render(view, params) and may return a cleanup function.
import * as records from './records.js';
import * as tags from './tags.js';
import * as print from './print.js';
import * as templates from './templates.js';
import * as backgrounds from './backgrounds.js';
import * as settings from './settings.js';
import { h } from './lib.js';

const screens = { records, tags, print, templates, backgrounds, settings };
let cleanup = null;
let seq = 0;

// parseHash turns "#/records/edit/x?name=y" into {screen, rest, params}.
function parseHash() {
  const [path, query = ''] = location.hash.replace(/^#\/?/, '').split('?');
  const [screen, ...rest] = path.split('/').map(decodeURIComponent);
  return { screen: screen || 'records', rest, params: new URLSearchParams(query) };
}

// route renders the screen for the hash. Each route gets its own container,
// attached at once, so a slower earlier route that resolves late writes into
// a detached node and never replaces the newer screen. Its cleanup runs as
// soon as it resolves, so its listeners don't leak.
async function route() {
  const my = ++seq;
  const { screen, rest, params } = parseHash();
  const mod = screens[screen] || records;
  if (cleanup) cleanup();
  cleanup = null;
  for (const a of document.querySelectorAll('#nav a')) {
    a.toggleAttribute('aria-current', a.dataset.screen === screen);
  }
  const view = h('div', { class: 'screen' }, h('p', { class: 'muted', text: 'Loading…' }));
  document.getElementById('view').replaceChildren(view);
  let done = null;
  try {
    done = (await mod.render(view, rest, params)) || null;
  } catch (e) {
    if (my === seq) view.replaceChildren(h('p', { class: 'error', text: `Could not load: ${e.message}` }));
    return;
  }
  if (my === seq) cleanup = done;
  else if (done) done();
}

window.addEventListener('hashchange', route);
// Sticky toolbars sit under the sticky nav, whose height wraps on a phone.
const setNavHeight = () => document.documentElement.style.setProperty('--nav-h', `${document.querySelector('header.top').offsetHeight}px`);
window.addEventListener('resize', setNavHeight);
setNavHeight();

route();
