import { expect, test } from 'claude-code/testing'

import { bar, displayMarks, displayTag, fit, fitLeft, homePrefix, statusLine, wrapWords } from './format'

test('status line format', () => {
  expect(statusLine({ injected: 37, useful: 2, useful_share: 0.0625 })).toBe('mem 37↓ ✓2 · 6%')
  expect(statusLine({ injected: 0, useful: 0, useful_share: 0 })).toBe('mem 0↓ ✓0 · 0%')
})

test('fit cuts with ellipsis', () => {
  expect(fit('abcdef', 4)).toBe('abc…')
  expect(fit('abcd', 4)).toBe('abcd')
  expect(fit('ab', 4)).toBe('ab')
  expect(fit('abc', 1)).toBe('…')
  expect(fit('abc', 0)).toBe('')
  expect(fit('Память', 4)).toBe('Пам…')
})

test('share bar fills by share', () => {
  expect(bar(0.25, 8)).toBe('██░░░░░░')
  expect(bar(0, 4)).toBe('░░░░')
  expect(bar(1, 4)).toBe('████')
  expect(bar(0.5, 0)).toBe('')
})

test('wrapWords keeps every line within width', () => {
  const lines = wrapWords('pull-only mode: prompts inject nothing; facts read with Read are not counted', 20)

  expect(lines).toEqual(['pull-only mode:', 'prompts inject', 'nothing; facts read', 'with Read are not', 'counted'])
  expect(wrapWords('a-very-long-unbroken-word', 10)).toEqual(['a-very-lo…'])
})

test('fitLeft keeps the end', () => {
  expect(fitLeft('/a/b/memory/alpha.md', 12)).toBe('…ry/alpha.md')
  expect(fitLeft('abcd', 4)).toBe('abcd')
  expect(fitLeft('abc', 1)).toBe('…')
  expect(fitLeft('abc', 0)).toBe('')
  expect(fitLeft('Память.md', 5)).toBe('…ь.md')
})

test('tags take display form from a known pair', () => {
  const prefix = homePrefix('-Users-me-work-proj', 'work-proj')
  expect(prefix).toBe('-Users-me-')
  expect(displayTag('-Users-me-other', prefix)).toBe('other')
  expect(displayTag('-opt-elsewhere', prefix)).toBe('-opt-elsewhere')
  expect(displayTag('-Users-me-', prefix)).toBe('-Users-me-')
  expect(displayTag('-Users-me-other', undefined)).toBe('-Users-me-other')
  expect(homePrefix('global', 'global')).toBeUndefined()
  expect(homePrefix('-opt-x', '-opt-x')).toBeUndefined()
  expect(homePrefix('-a-bwork', 'work')).toBeUndefined()
  expect(displayMarks(['hide:-Users-me-other', 'less', 'task:rewrite'], prefix)).toEqual([
    'hide:other',
    'less',
    'task:rewrite',
  ])
})
