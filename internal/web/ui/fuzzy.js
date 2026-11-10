// Fuzzy subsequence matching: every query character must appear in order.
// "mess" matches "Metallica — Essential". Matches at word starts and runs of
// consecutive characters rank higher.

const isWordStart = (s, i) => i === 0 || /[\s\-—–_/|.,()]/.test(s[i - 1]);

const WORD_START = 10;
const CONSECUTIVE = 6;
const MAX_GAP_PENALTY = 10;

// match returns {score, positions} or null. Higher score is better.
// Dynamic programming over (query char, text position) finds the best
// placement, not just the first one: "mess" picks the "E" of "Essential".
export function match(query, text) {
  const q = query.trim().toLowerCase().replace(/\s+/g, ' ');
  if (!q) return { score: 0, positions: [] };
  const t = text.toLowerCase();
  const n = t.length;
  // best[i][j]: best score with q[i] placed at t[j]; from[i][j]: previous j.
  const best = [];
  const from = [];
  for (let i = 0; i < q.length; i++) {
    best.push(new Array(n).fill(-Infinity));
    from.push(new Array(n).fill(-1));
    for (let j = i; j < n; j++) {
      if (t[j] !== q[i]) continue;
      const bonus = isWordStart(t, j) ? WORD_START : 0;
      if (i === 0) {
        best[i][j] = bonus - j * 0.2;
        continue;
      }
      for (let k = i - 1; k < j; k++) {
        if (best[i - 1][k] === -Infinity) continue;
        const gap = j - k - 1;
        const s = best[i - 1][k] + bonus + (gap === 0 ? CONSECUTIVE : -Math.min(gap, MAX_GAP_PENALTY));
        if (s > best[i][j]) { best[i][j] = s; from[i][j] = k; }
      }
    }
  }
  const last = best[q.length - 1];
  let end = -1;
  for (let j = 0; j < n; j++) if (last[j] > -Infinity && (end === -1 || last[j] > last[end])) end = j;
  if (end === -1) return null;
  const positions = new Array(q.length);
  for (let i = q.length - 1, j = end; i >= 0; j = from[i][j], i--) positions[i] = j;
  return { score: last[end] - n * 0.01, positions };
}

// filter returns the items that match, best first. text(item) gives the
// searchable string. Without a query the order is kept.
export function filter(items, query, text) {
  if (!query.trim()) return items.map((item) => ({ item, positions: [] }));
  const out = [];
  for (const item of items) {
    const m = match(query, text(item));
    if (m) out.push({ item, positions: m.positions, score: m.score });
  }
  out.sort((a, b) => b.score - a.score);
  return out;
}
