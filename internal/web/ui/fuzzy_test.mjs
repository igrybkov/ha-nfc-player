// Run with: node --test internal/web/ui/fuzzy_test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { match, filter } from './fuzzy.js';

const names = [
  'Disturbed — Divisible',
  'Metallica — Essential',
  'Creedence Clearwater Revival — Essential',
  'Volodymyr Ivasiuk',
  'Володимир Івасюк',
];

test('subsequence across words', () => {
  assert.ok(match('mess', 'Metallica — Essential'));
  assert.equal(match('mess', 'Disturbed — Divisible'), null);
});

test('mess ranks Metallica — Essential first', () => {
  const got = filter(names, 'mess', (s) => s);
  assert.equal(got[0].item, 'Metallica — Essential');
});

test('case-insensitive, Cyrillic', () => {
  assert.equal(match('юв', 'Володимир Івасюк'), null);
  assert.ok(match('віс', 'Володимир Івасюк'));
  assert.ok(match('ВОЛ', 'Володимир Івасюк'));
});

test('positions are in order and point at matching characters', () => {
  const text = 'Metallica — Essential';
  const m = match('mess', text);
  assert.equal(m.positions.length, 4);
  for (let i = 1; i < m.positions.length; i++) assert.ok(m.positions[i] > m.positions[i - 1]);
  assert.equal(m.positions.map((p) => text[p].toLowerCase()).join(''), 'mess');
  // Picks the word start "E" of Essential, not the "e" in Metallica.
  assert.equal(text[m.positions[1]], 'E');
});

test('empty query keeps order', () => {
  assert.deepEqual(filter(names, '  ', (s) => s).map((r) => r.item), names);
});

test('word starts beat scattered hits', () => {
  const got = filter(['Some Random Album', 'Creedence Clearwater Revival — Essential'], 'ccr', (s) => s);
  assert.equal(got[0].item, 'Creedence Clearwater Revival — Essential');
});
