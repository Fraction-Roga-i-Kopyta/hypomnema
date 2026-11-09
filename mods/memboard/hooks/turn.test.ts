import { expect, test } from 'claude-code/testing'

import { STATS_SESSION } from './fixtures.gen'
import { parseInjected, toKey } from './turn'

const CONTEXT = [
  '# Memory Context',
  'When a fact below changes what you say, do, or decide, wrap that sentence in <cc-memory filenames="FILE">…</cc-memory> (the tag is hidden from the user).',
  '',
  '## alpha-rule — alpha.md (feedback, 2026-11-01)',
  'Alpha body, with a line that mentions ## gamma — gamma.md (strategy).',
  '## Context',
  '## gamma-way — gamma.md (strategy, 2026-08-15)',
  'Gamma body.',
].join('\n')

test('parses fact headers', () => {
  const facts = parseInjected([CONTEXT, '## alpha-rule — alpha.md (feedback, 2026-11-01)'])

  expect(facts).toEqual([
    { name: 'alpha-rule', file: 'alpha.md', type: 'feedback' },
    { name: 'gamma-way', file: 'gamma.md', type: 'strategy' },
  ])
})

test('parses header without date', () => {
  expect(parseInjected(['## beta-bug — beta.md (mistake)\r\nbody'])).toEqual([
    { name: 'beta-bug', file: 'beta.md', type: 'mistake' },
  ])
})

test('ignores non-header lines', () => {
  const noise = [
    '## Context',
    '### alpha-rule — alpha.md (feedback, 2026-11-01)',
    '## notes — notes.txt (note)',
    '## two words — my file.md (note)',
    'see ## alpha-rule — alpha.md (feedback)',
    '## alpha-rule — alpha.md (feedback, 2026-11-01) trailing',
    '## alpha-rule - alpha.md (feedback)',
  ].join('\n')

  expect(parseInjected([noise])).toEqual([])
  expect(parseInjected([])).toEqual([])
})

test('toKey prefers session project', () => {
  const s = STATS_SESSION.session
  if (s === undefined) throw new Error('fixture has no session')
  const project = STATS_SESSION.project
  const both = {
    ...s,
    delivered: [
      { project: 'global', slug: 'alpha.md' },
      { project: '-HOME-other', slug: 'alpha.md' },
      { project, slug: 'alpha.md' },
    ],
  }
  const alpha = { name: 'alpha-rule', file: 'alpha.md', type: 'feedback' }

  expect(toKey(alpha, both, project)).toEqual({ project, slug: 'alpha.md' })
  expect(toKey(alpha, both, '-HOME-elsewhere')).toEqual({ project: 'global', slug: 'alpha.md' })
  expect(toKey({ ...alpha, file: 'gamma.md' }, s, project)).toEqual({
    project: 'global',
    slug: 'gamma.md',
  })
  expect(toKey({ ...alpha, file: 'nowhere.md' }, s, project)).toBeUndefined()
})
