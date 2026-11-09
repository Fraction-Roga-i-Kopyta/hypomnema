import { expect, mock, test } from 'claude-code/testing'
import type { On, ProcessRunResult } from 'claude-code'
import type { Engine, FoundElement, MockClock } from 'claude-code/testing'

import { STATS_FACT, STATS_PROJECT, STATS_SESSION } from './fixtures.gen'
import type { StatsReport } from './memoryctl'

const BIN = '/home/u/.claude/bin/memoryctl'
const P = STATS_PROJECT.project
const KEY = `project-queue:${P}/alpha.md`

function pane(columns: number) {
  return {
    plugin: 'memboard',
    component: 'Pane',
    requestId: 'memboard',
    viewport: { columns, rows: 60 },
    surface: 'terminal',
    props: {
      title: 'Memory',
      isFocused: true,
      bodyColumns: columns,
      placement: 'dock',
      scroll: { offset: 0, bodyRows: 60 },
      view: {},
    },
  } as const
}

function ran(stdout: string, exitCode = 0, stderr = ''): ProcessRunResult {
  return { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false }
}

const copy = (r: StatsReport): StatsReport => JSON.parse(JSON.stringify(r))

type World = {
  clock: MockClock
  runs: (readonly string[])[]
  envs: (string | undefined)[]
  toasts: string[]
  fact: () => StatsReport
  /** stderr of a failing mutation, if set. */
  failure: string | undefined
}

const MUTATIONS = ['retire', 'revive', 'pin', 'unpin', 'mark']

function world(on: On): World {
  const w: World = {
    clock: mock.clock(on),
    runs: [],
    envs: [],
    toasts: [],
    fact: () => copy(STATS_FACT),
    failure: undefined,
  }
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('command.register', () => ({ value: { command: 'memboard' } }))
  on('ui.status', () => ({ value: undefined }))
  on('ui.toast', (_, e) => {
    w.toasts.push(e.text)
    return { value: undefined }
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.close', () => ({ value: undefined }))
  on('process.run', (_, e) => {
    w.runs.push(e.argv)
    if (e.argv[0] === '/bin/sh') return { value: ran(`${BIN}\n`) }
    const verb = e.argv[1] ?? ''
    if (MUTATIONS.includes(verb)) {
      w.envs.push(e.init?.env?.HYPOMNEMA_SESSION_ID)
      return { value: w.failure === undefined ? ran('') : ran('', 1, `${w.failure}\nusage...\n`) }
    }
    const scope = e.argv[e.argv.indexOf('--scope') + 1]
    const r = scope === 'fact' ? w.fact() : scope === 'project' ? copy(STATS_PROJECT) : copy(STATS_SESSION)
    return { value: ran(JSON.stringify(r)) }
  })
  on('classic.SessionStart', () => ({}))
  on('classic.UserPromptSubmit', () => ({}))
  on('classic.Stop', () => ({}))
  return w
}

async function start($: Engine, w: World) {
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await $.classic.SessionStart({ source: 'startup', session_id: 's1' })
  await w.clock.settle()
}

/** The Project tab, a queue row pressed: the card is up. */
async function openCard($: Engine, w: World, columns = 52) {
  await start($, w)
  const ui = await $.ui.mount(pane(columns))
  await ui.press({ key: 'tab-project' })
  await w.clock.settle()
  await ui.press({ key: KEY })
  await w.clock.settle()
  return ui
}

const mutations = (w: World) => w.runs.filter(argv => MUTATIONS.includes(argv[1] ?? ''))
const factRuns = (w: World) => w.runs.filter(argv => argv[1] === 'stats' && argv.includes('fact'))
const projectRuns = (w: World) =>
  w.runs.filter(argv => argv[1] === 'stats' && argv[argv.indexOf('--scope') + 1] === 'project')
const width = (el: FoundElement) => [...el.text].length + (el.type === 'Button' && !el.props.plain ? 4 : 0)

test('opening a row shows the fact card', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  expect(factRuns(w).at(-1)).toEqual([BIN, 'stats', '--json', '--scope', 'fact', '--project', P, 'alpha.md'])
  expect((await ui.find({ type: 'Text', text: /^alpha-rule/ }))?.text).toBe('alpha-rule')
  expect((await ui.find({ type: 'Text', text: /^effective/ }))?.text).toMatch(/0\.67$/)
  expect((await ui.find({ type: 'Text', text: /^marks/ }))?.text).toContain('task:rewrite')
  await ui.unmount()
})

test('archive needs confirmation', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  expect((await ui.find({ key: 'ask-archive' }))?.props.hotkey).toBe('a')
  await ui.press({ key: 'ask-archive' })
  await w.clock.settle()
  expect(mutations(w)).toHaveLength(0)
  expect((await ui.find({ key: 'confirm-yes' }))?.props.hotkey).toBe('y')
  expect((await ui.find({ key: 'confirm-no' }))?.props.hotkey).toBe('n')

  await ui.press({ key: 'confirm-no' })
  await w.clock.settle()
  expect(mutations(w)).toHaveLength(0)
  expect(await ui.find({ key: 'ask-archive' })).toBeDefined()
  await ui.unmount()
})

test('archive shows stub with undo', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  const cardFetches = factRuns(w).length
  const listFetches = projectRuns(w).length
  await ui.press({ key: 'ask-archive' })
  await ui.press({ key: 'confirm-yes' })
  await w.clock.settle()

  expect(mutations(w)).toEqual([[BIN, 'retire', 'alpha.md', '--project', P]])
  expect(w.envs).toEqual(['s1'])
  expect(w.toasts.at(-1)).toBe('Archived: alpha')
  expect((await ui.find({ type: 'Text', text: /^archived:/ }))?.text).toBe('archived: alpha-rule')
  expect((await ui.find({ key: 'undo' }))?.props.hotkey).toBe('u')
  expect((await ui.find({ key: 'back' }))?.props.hotkey).toBe('b')
  // The fact is gone: the card is not fetched again, the list is.
  expect(factRuns(w)).toHaveLength(cardFetches)
  expect(projectRuns(w).length).toBeGreaterThan(listFetches)
  await ui.unmount()
})

test('undo archive revives', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  await ui.press({ key: 'ask-archive' })
  await ui.press({ key: 'confirm-yes' })
  await w.clock.settle()
  const cardFetches = factRuns(w).length
  await ui.press({ key: 'undo' })
  await w.clock.settle()

  expect(mutations(w).at(-1)).toEqual([BIN, 'revive', 'alpha.md', '--project', P])
  expect(factRuns(w).length).toBeGreaterThan(cardFetches)
  expect(await ui.find({ key: 'undo' })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /^alpha-rule/ }))?.text).toBe('alpha-rule')
  await ui.unmount()
})

test('pin calls memoryctl with --project', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  const before = factRuns(w).length
  await ui.press({ key: 'act-pin' })
  await w.clock.settle()
  expect(mutations(w)).toEqual([[BIN, 'pin', 'alpha.md', '--project', P]])
  expect(w.envs).toEqual(['s1'])
  expect(w.toasts.at(-1)).toBe('Pinned: alpha')
  expect(factRuns(w).length).toBeGreaterThan(before)

  await ui.press({ key: 'act-less' })
  await w.clock.settle()
  expect(mutations(w).at(-1)).toEqual([BIN, 'mark', 'alpha.md', 'less', '--project', P])
  await ui.unmount()
})

test('pinned fact offers unpin', async ($, on) => {
  const w = world(on)
  w.fact = () => {
    const r = copy(STATS_FACT)
    if (r.fact !== undefined) r.fact.status = 'pinned'
    return r
  }
  const ui = await openCard($, w)
  expect(await ui.find({ key: 'act-pin' })).toBeUndefined()
  await ui.press({ key: 'act-unpin' })
  await w.clock.settle()
  expect(mutations(w)).toEqual([[BIN, 'unpin', 'alpha.md', '--project', P]])
  await ui.unmount()
})

function withFact(w: World, change: (f: NonNullable<StatsReport['fact']>) => void) {
  w.fact = () => {
    const r = copy(STATS_FACT)
    if (r.fact !== undefined) change(r.fact)
    return r
  }
}

test('hide hidden for foreign project fact', async ($, on) => {
  const w = world(on)
  withFact(w, f => {
    f.project = '-HOME-other'
  })
  const ui = await openCard($, w)
  expect(await ui.find({ key: 'act-hide' })).toBeUndefined()
  expect(await ui.find({ key: 'act-less' })).toBeDefined()
  await ui.unmount()
})

test('hide offered for own project fact', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  expect((await ui.find({ key: 'act-hide' }))?.props.hotkey).toBe('h')
  await ui.press({ key: 'act-hide' })
  await w.clock.settle()
  expect(mutations(w)).toEqual([[BIN, 'mark', 'alpha.md', 'hide', '--project', P]])
  await ui.unmount()
})

test('hide offered for global fact', async ($, on) => {
  const w = world(on)
  withFact(w, f => {
    f.project = 'global'
  })
  const ui = await openCard($, w)
  expect(await ui.find({ key: 'act-hide' })).toBeDefined()
  await ui.unmount()
})

test('done offered with an open task', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  expect((await ui.find({ key: 'act-done' }))?.props.hotkey).toBe('d')
  await ui.unmount()
})

test('done only with open task', async ($, on) => {
  const w = world(on)
  withFact(w, f => {
    f.marks = ['less']
  })
  const ui = await openCard($, w)
  expect(await ui.find({ key: 'act-done' })).toBeUndefined()
  await ui.unmount()
})

test('queue row Done runs mark done', async ($, on) => {
  const w = world(on)
  await start($, w)
  const ui = await $.ui.mount(pane(52))
  await ui.press({ key: 'tab-project' })
  await w.clock.settle()
  const before = projectRuns(w).length
  await ui.press({ key: `done:${KEY}` })
  await w.clock.settle()
  expect(mutations(w)).toEqual([[BIN, 'mark', 'alpha.md', 'done', '--project', P]])
  expect(w.envs).toEqual(['s1'])
  expect(projectRuns(w).length).toBeGreaterThan(before)
  expect(await ui.find({ key: 'back' })).toBeUndefined()
  await ui.unmount()
})

test('failed action toasts and refreshes', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  const cards = factRuns(w).length
  const lists = projectRuns(w).length
  w.failure = 'no fact named alpha.md'
  await ui.press({ key: 'act-pin' })
  await w.clock.settle()
  expect(w.toasts.at(-1)).toBe('no fact named alpha.md')
  expect(factRuns(w).length).toBeGreaterThan(cards)
  expect(projectRuns(w).length).toBeGreaterThan(lists)
  await ui.unmount()
})

test('back leaves the card', async ($, on) => {
  const w = world(on)
  const ui = await openCard($, w)
  await ui.press({ key: 'back' })
  await w.clock.settle()
  expect(await ui.find({ key: 'act-pin' })).toBeUndefined()
  expect(await ui.find({ key: KEY })).toBeDefined()
  await ui.unmount()
})

test('card fits 40 columns', async ($, on) => {
  const w = world(on)
  const LONG = 'an-extremely-long-fact-name-that-will-never-fit-into-a-narrow-pane'
  w.fact = () => {
    const r = copy(STATS_FACT)
    if (r.fact !== undefined) {
      r.fact.name = LONG
      r.fact.display = LONG
      r.fact.description = `${LONG} ${LONG} ${LONG} ${LONG} and then some more words that go on`
      r.fact.path = `/Users/someone/.claude/projects/${LONG}/memory/${LONG}.md`
      r.fact.marks = [`hide:${LONG}`, 'less', `task:${LONG}`]
    }
    return r
  }
  const ui = await openCard($, w, 40)
  for (const confirming of [false, true]) {
    if (confirming) await ui.press({ key: 'ask-archive' })
    const elements = [...(await ui.findAll({ type: 'Text' })), ...(await ui.findAll({ type: 'Button' }))]
    expect(elements.length).toBeGreaterThan(10)
    for (const el of elements) expect(width(el) <= 40, `${width(el)} > 40: ${el.text}`).toBe(true)
  }
  await ui.unmount()
})
