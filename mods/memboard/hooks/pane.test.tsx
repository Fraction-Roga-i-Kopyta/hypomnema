import { expect, mock, test } from 'claude-code/testing'
import type { On, ProcessRunResult } from 'claude-code'
import type { Engine, FoundElement, MockClock, Plugin } from 'claude-code/testing'

import { STATS_SESSION } from './fixtures.gen'
import type { StatsReport } from './memoryctl'

const BIN = '/home/u/.claude/bin/memoryctl'
const HINT = 'memoryctl not found or too old — run ./install.sh in the hypomnema repo'
const PROJECT = STATS_SESSION.project
const SURFACES = ['terminal', 'desktop'] as const

function pane(columns: number) {
  return {
    plugin: 'memboard',
    component: 'Pane',
    requestId: 'memboard',
    viewport: { columns, rows: 40 },
    props: {
      title: 'Memory',
      isFocused: true,
      bodyColumns: columns,
      placement: 'dock',
      scroll: { offset: 0, bodyRows: 40 },
      view: {},
    },
  } as const
}

const CONTEXT = [
  '# Memory Context',
  'When a fact below changes what you say, do, or decide, wrap that sentence in <cc-memory filenames="FILE">…</cc-memory> (the tag is hidden from the user).',
  '## alpha-rule — alpha.md (feedback, 2026-11-01)',
  'Alpha body.',
  '## beta-bug — beta.md (mistake, 2026-10-20)',
  'Beta body.',
].join('\n')

function ran(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

function report(change: (r: StatsReport) => StatsReport = r => r): ProcessRunResult {
  const copy: StatsReport = JSON.parse(JSON.stringify(STATS_SESSION))
  return ran(JSON.stringify(change(copy)))
}

type Script = {
  located?: boolean
  stats?: () => ProcessRunResult
  context?: string
}

type World = {
  clock: MockClock
  statuses: (string | undefined)[]
  runs: (readonly string[])[]
  opened: string[]
  closed: string[]
  script: Script
}

function world(on: On, script: Script = {}): World {
  const w: World = {
    clock: mock.clock(on),
    statuses: [],
    runs: [],
    opened: [],
    closed: [],
    script,
  }
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('command.register', () => ({ value: { command: 'memboard' } }))
  on('ui.status', (_, e) => {
    w.statuses.push(e.text)
    return { value: undefined }
  })
  on('ui.open', (_, e) => {
    w.opened.push(e.id)
    return { value: { isPlaced: true } }
  })
  on('ui.close', (_, e) => {
    w.closed.push(e.id)
    return { value: undefined }
  })
  on('process.run', (_, e) => {
    w.runs.push(e.argv)
    if (e.argv[0] === '/bin/sh') {
      return { value: w.script.located === false ? ran('', 1) : ran(`${BIN}\n`) }
    }
    return { value: (w.script.stats ?? (() => report()))() }
  })
  on('classic.SessionStart', () => ({}))
  on('classic.UserPromptSubmit', () => ({ additionalContext: [w.script.context ?? CONTEXT] }))
  on('classic.Stop', () => ({}))
  return w
}

async function turn($: Engine, w: World) {
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 's1' })
  await $.classic.Stop({ stop_hook_active: false, session_id: 's1' })
  await w.clock.settle()
}

async function start($: Engine, w: World) {
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await $.classic.SessionStart({ source: 'startup', session_id: 's1' })
  await w.clock.settle()
  await turn($, w)
}

function toggle($: Engine) {
  return $.command.run({
    command: 'memboard',
    args: '',
    origin: { kind: 'composer' },
    presentation: { isFullscreen: true, columns: 160 },
  })
}

const statsRuns = (w: World) => w.runs.filter(argv => argv[1] === 'stats').length

const width = (el: FoundElement) => [...el.text].length + (el.type === 'Button' && !el.props.plain ? 4 : 0)

test('missing memoryctl renders hint', async ($, on) => {
  const w = world(on, { located: false })
  await start($, w)

  for (const surface of SURFACES) {
    const ui = await $.ui.mount({ ...pane(52), surface })
    expect((await ui.find({ type: 'Text', text: HINT }))?.text).toBe(HINT)
    expect(await ui.findAll({ type: 'Button' })).toHaveLength(0)
    await ui.unmount()
  }
  expect(statsRuns(w)).toBe(0)
  expect(w.statuses.length).toBeGreaterThan(0)
  expect(w.statuses.every(text => text === undefined)).toBe(true)
})

test('old memoryctl renders hint', async ($, on) => {
  const w = world(on, { stats: () => ran('', 2, 'memoryctl: unknown command "stats"\n') })
  await start($, w)

  for (const surface of SURFACES) {
    const ui = await $.ui.mount({ ...pane(52), surface })
    expect((await ui.find({ type: 'Text', text: HINT }))?.text).toBe(HINT)
    expect(await ui.find({ key: 'tab-session' })).toBeUndefined()
    await ui.unmount()
  }
  expect(statsRuns(w)).toBeGreaterThan(0)
  expect(w.statuses.at(-1)).toBeUndefined()
})

test('schema mismatch clears status', async ($, on) => {
  const w = world(on)
  await start($, w)
  expect(w.statuses.at(-1)).toBe('mem 5↓ ✓1 · 33%')

  w.script.stats = () => report(r => ({ ...r, schema_version: 2 }))
  await turn($, w)

  expect(w.statuses.at(-1)).toBeUndefined()
  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: HINT }))?.text).toBe(HINT)
  await ui.unmount()

  w.script.stats = () => report()
  await turn($, w)
  expect(w.statuses.at(-1)).toBe('mem 5↓ ✓1 · 33%')
})

test("session tab lists this turn's facts", async ($, on) => {
  const w = world(on)
  await start($, w)
  const r = await $.classic.UserPromptSubmit({ prompt: 'again', session_id: 's1' })
  expect(r.additionalContext).toEqual([CONTEXT])

  for (const surface of SURFACES) {
    const ui = await $.ui.mount({ ...pane(52), surface })
    expect((await ui.find({ type: 'Text', text: /^This turn/ }))?.text).toBe('This turn (2 facts)')
    const rows = (await ui.findAll({ type: 'Button' })).filter(b => b.key?.startsWith('turn:'))
    expect(rows.map(b => b.key)).toEqual([`turn:${PROJECT}/alpha.md`, `turn:${PROJECT}/beta.md`])
    expect(rows[0]?.text).toMatch(/alpha-rule +feed$/)
    expect(rows[1]?.text).toMatch(/beta-bug +mist$/)
    expect((await ui.find({ type: 'Text', text: /^Injected/ }))?.text).toBe(
      'Injected 5  ✓ useful 1  · silent 2',
    )
    const top = (await ui.findAll({ type: 'Button' })).filter(b => b.key?.startsWith('top:'))
    expect(top.map(b => b.key)).toEqual([
      `top:${PROJECT}/alpha.md`,
      `top:${PROJECT}/beta.md`,
      'top:global/gamma.md',
      `top:${PROJECT}/gone.md`,
    ])
    expect(top[0]?.text).toMatch(/^alpha-rule +2 +1$/)
    expect(top[1]?.text).toMatch(/^beta-bug +1 +–$/)
    expect(await ui.find({ type: 'Text', text: /citation\(s\) for facts not delivered/ })).toBeDefined()
    await ui.unmount()
  }
})

test('the /memboard prompt keeps the turn', async ($, on) => {
  const w = world(on)
  await start($, w)

  w.script.context = '## gamma-way — gamma.md (strategy, 2026-08-15)'
  await $.classic.UserPromptSubmit({ prompt: '/memboard', session_id: 's1' })

  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  const rows = (await ui.findAll({ type: 'Button' })).filter(b => b.key?.startsWith('turn:'))
  expect(rows.map(b => b.key)).toEqual([`turn:${PROJECT}/alpha.md`, `turn:${PROJECT}/beta.md`])
  await ui.unmount()
})

test('a failed refresh locates memoryctl again', async ($, on) => {
  const w = world(on, { stats: () => ran('', 1, 'exec failed\n') })
  await start($, w)
  const locates = w.runs.filter(argv => argv[0] === '/bin/sh').length

  w.script.stats = () => report()
  await turn($, w)

  expect(w.runs.filter(argv => argv[0] === '/bin/sh').length).toBe(locates + 1)
  expect(w.statuses.at(-1)).toBe('mem 5↓ ✓1 · 33%')
})

test('this-turn tick from useful_facts', async ($, on) => {
  const context = [
    '## alpha-rule — alpha.md (feedback, 2026-11-01)',
    '## gamma-way — gamma.md (strategy, 2026-08-15)',
    '## beta-bug — beta.md (mistake)',
    '## fresh-fact — fresh.md (knowledge, 2026-11-09)',
  ].join('\n')
  // beta.md is useful in the global store only: the session's own beta.md is not.
  const w = world(on, {
    context,
    stats: () =>
      report(r => {
        const s = r.session
        if (s === undefined) return r
        return { ...r, session: { ...s, useful_facts: [...s.useful_facts, { project: 'global', slug: 'beta.md' }] } }
      }),
  })
  await start($, w)

  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  const rows = (await ui.findAll({ type: 'Button' })).filter(b => b.key?.startsWith('turn:'))
  expect(rows.map(b => [b.key, b.text.slice(0, 2)])).toEqual([
    [`turn:${PROJECT}/alpha.md`, '✓ '],
    ['turn:global/gamma.md', '· '],
    [`turn:${PROJECT}/beta.md`, '· '],
  ])
  // A fact not delivered by the last refresh is listed, but not as a button.
  expect((await ui.find({ type: 'Text', text: /fresh-fact/ }))?.text).toMatch(/^· fresh-fact +know$/)
  await ui.unmount()
})

test('hotkey 2 switches tab', async ($, on) => {
  const w = world(on)
  await start($, w)

  for (const surface of SURFACES) {
    const ui = await $.ui.mount({ ...pane(52), surface })
    const tabs = (await ui.findAll({ type: 'Button' })).filter(b => b.key?.startsWith('tab-'))
    expect(tabs.map(b => [b.key, b.props.hotkey, b.text])).toEqual([
      ['tab-session', '1', '1 Session'],
      ['tab-project', '2', '2 Project'],
      ['tab-all', '3', '3 All'],
    ])
    expect((await ui.find({ key: 'tab-session' }))?.props.variant).toBe('primary')

    await ui.press({ key: 'tab-project' })
    expect((await ui.find({ key: 'tab-project' }))?.props.variant).toBe('primary')
    expect((await ui.find({ key: 'tab-session' }))?.props.variant).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /^This turn/ })).toBeUndefined()
    expect(await ui.find({ type: 'Text', text: /Project/ })).toBeDefined()

    await ui.press({ key: 'tab-session' })
    expect(await ui.find({ type: 'Text', text: /^This turn/ })).toBeDefined()
    await ui.unmount()
  }
})

test('session rows fit 40 columns', async ($, on) => {
  const long = 'an-extremely-long-fact-name-that-will-never-fit-into-a-narrow-pane'
  const w = world(on, {
    context: `## ${long} — alpha.md (knowledge, 2026-11-01)\n## beta-bug — beta.md (mistake)`,
    stats: () =>
      report(r => {
        const s = r.session
        if (s === undefined) return r
        const top = s.top.map((f, i) => (i === 0 ? { ...f, name: long, injected: 12345, useful: 678 } : f))
        return {
          ...r,
          session: { ...s, injected: 123456, useful: 7890, silent: 98765, undelivered: 4321, prompt_inject: false, top },
        }
      }),
  })
  await start($, w)

  for (const surface of SURFACES) {
    const ui = await $.ui.mount({ ...pane(40), surface })
    const elements = [...(await ui.findAll({ type: 'Text' })), ...(await ui.findAll({ type: 'Button' }))]
    expect(elements.length).toBeGreaterThan(10)
    for (const el of elements) {
      expect(width(el) <= 40, `${el.type} ${width(el)} > 40: ${el.text}`).toBe(true)
    }
    const tabs = (await ui.findAll({ type: 'Button' })).filter(b => b.key?.startsWith('tab-'))
    const bar = tabs.reduce((n, b) => n + width(b), 0) + tabs.length - 1
    expect(bar <= 40, `tab bar ${bar} > 40`).toBe(true)
    expect((await ui.find({ key: `turn:${PROJECT}/alpha.md` }))?.text).toContain('…')
    expect((await ui.find({ key: `top:${PROJECT}/alpha.md` }))?.text).toContain('…')
    expect(await ui.find({ type: 'Text', text: /^pull-only mode/ })).toBeDefined()
    await ui.unmount()
  }
})

// A close the mod did not ask for: the person's close key reaches the same
// `ui.close` chain; a test raises it through another plugin's `$.ui.close`.
const CLOSER: Plugin = {
  name: 'closer',
  register(on) {
    on('command.run', { command: 'close-memboard' }, async $ => {
      await $.ui.close({ id: 'memboard' })
      return { text: 'closed' }
    })
  },
}

test('closing with Esc resets isOpen', { plugins: [CLOSER] }, async ($, on) => {
  const w = world(on)
  await start($, w)

  await toggle($)
  expect(w.opened).toEqual(['memboard'])
  const open = statsRuns(w)
  await w.clock.advance(180_000)
  expect(statsRuns(w)).toBe(open + 1)

  await $.command.run({
    command: 'close-memboard',
    args: '',
    origin: { kind: 'composer' },
    presentation: { isFullscreen: true, columns: 160 },
  })
  expect(w.closed).toEqual(['memboard'])
  const closed = statsRuns(w)
  await w.clock.advance(10 * 180_000)
  expect(statsRuns(w)).toBe(closed)

  await toggle($)
  expect(w.opened).toEqual(['memboard', 'memboard'])
})

test('memboard toggles the pane', async ($, on) => {
  const w = world(on)
  await start($, w)

  await toggle($)
  await toggle($)
  expect(w.opened).toEqual(['memboard'])
  expect(w.closed).toEqual(['memboard'])
  await toggle($)
  expect(w.opened).toEqual(['memboard', 'memboard'])
})
