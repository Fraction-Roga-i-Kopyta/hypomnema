import { expect, mock, test } from 'claude-code/testing'
import type { On, ProcessRunResult } from 'claude-code'
import type { Engine, FoundElement, MockClock } from 'claude-code/testing'

import { STATS_ALL, STATS_PROJECT, STATS_SESSION } from './fixtures.gen'
import type { StatsReport } from './memoryctl'

const BIN = '/home/u/.claude/bin/memoryctl'
const P = STATS_PROJECT.project
const SURFACES = ['terminal', 'desktop'] as const

function pane(columns: number) {
  return {
    plugin: 'memboard',
    component: 'Pane',
    requestId: 'memboard',
    viewport: { columns, rows: 60 },
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

function ran(stdout: string): ProcessRunResult {
  return { exitCode: 0, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
}

const copy = (r: StatsReport): StatsReport => JSON.parse(JSON.stringify(r))

type World = {
  clock: MockClock
  runs: (readonly string[])[]
  project: () => StatsReport
  all: () => StatsReport
  /** Scopes whose stats run exits non-zero. */
  failing: Set<string>
}

function world(on: On): World {
  const w: World = {
    clock: mock.clock(on),
    runs: [],
    project: () => copy(STATS_PROJECT),
    all: () => copy(STATS_ALL),
    failing: new Set(),
  }
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('command.register', () => ({ value: { command: 'memboard' } }))
  on('ui.status', () => ({ value: undefined }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.close', () => ({ value: undefined }))
  on('process.run', (_, e) => {
    w.runs.push(e.argv)
    if (e.argv[0] === '/bin/sh') return { value: ran(`${BIN}\n`) }
    const scope = e.argv[e.argv.indexOf('--scope') + 1]
    if (scope !== undefined && w.failing.has(scope)) {
      return { value: { ...ran(''), exitCode: 1, stderr: `boom ${scope}\n` } }
    }
    const r = scope === 'project' ? w.project() : scope === 'all' ? w.all() : copy(STATS_SESSION)
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

const scoped = (w: World, scope: string) =>
  w.runs.filter(argv => argv[1] === 'stats' && argv[argv.indexOf('--scope') + 1] === scope)

const width = (el: FoundElement) => [...el.text].length + (el.type === 'Button' && !el.props.plain ? 4 : 0)

const LONG = 'an-extremely-long-fact-name-that-will-never-fit-into-a-narrow-pane'

test('project tab shows noisy facts', async ($, on) => {
  const w = world(on)
  w.project = () => {
    const r = copy(STATS_PROJECT)
    const ps = r.project_stats
    if (ps !== undefined && ps.queue[0] !== undefined) ps.triage.noisy = [{ ...ps.queue[0], marks: [] }]
    return r
  }
  await start($, w)

  for (const surface of SURFACES) {
    const ui = await $.ui.mount({ ...pane(52), surface })
    await ui.press({ key: 'tab-project' })
    await w.clock.settle()
    expect(scoped(w, 'project').at(-1)).toEqual([BIN, 'stats', '--json', '--scope', 'project'])
    const noisy = await ui.find({ key: `project-noisy:${P}/alpha.md` })
    expect(noisy?.text).toMatch(/^alpha-rule +feed +3\/1$/)
    expect((await ui.find({ key: `project-cand:${P}/beta.md` }))?.text).toMatch(/^beta-bug +mist +1\/0$/)
    expect((await ui.find({ type: 'Text', text: /^work-proj/ }))?.text).toContain('4 visible')
    expect(await ui.find({ type: 'Text', text: /3 own \+ 1 global/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /candidates 1 · stale 0 · pinned 1/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /ok 12/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /fail 2/ })).toBeDefined()
    expect((await ui.findAll({ type: 'Text', text: /^\d\d-\d\d.*\S$/ })).length).toBe(7)
    // The queue row carries its task directive.
    expect(await ui.find({ key: `project-queue:${P}/alpha.md` })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /rewrite/ })).toBeDefined()
    await ui.unmount()
  }
})

test('all tab funnel numbers', async ($, on) => {
  const w = world(on)
  await start($, w)

  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await ui.press({ key: 'tab-all' })
  await w.clock.settle()
  expect(scoped(w, 'all').at(-1)).toEqual([BIN, 'stats', '--json', '--scope', 'all'])
  expect((await ui.find({ type: 'Text', text: /^Stored/ }))?.text).toMatch(/^Stored +4 /)
  expect((await ui.find({ type: 'Text', text: /^Injected 7d/ }))?.text).toMatch(/^Injected 7d +4 /)
  expect((await ui.find({ type: 'Text', text: /^Useful 7d/ }))?.text).toMatch(/^Useful 7d +2 /)
  expect((await ui.find({ type: 'Text', text: /^not injected 30d/ }))?.text).toBe('not injected 30d: 1')
  expect((await ui.find({ key: `proj:${P}/` }))?.text).toMatch(/^work-proj +3 +1$/)
  // Emphasis: a funnel number is bold; a useful count is success.
  expect((await ui.find({ type: 'Text', text: /^ {6}4$/ }))?.props.bold).toBe(true)
  expect((await ui.find({ type: 'Text', text: /^ {6}2$/ }))?.props.color).toBe('success')
  expect((await ui.find({ type: 'Text', text: /^ {4}1$/ }))?.props.color).toBe('success')
  expect((await ui.find({ key: `all-cand:${P}/beta.md` }))?.text).toMatch(/^work-proj\/beta-bug/)
  await ui.unmount()
})

test('drill into project and back', async ($, on) => {
  const w = world(on)
  await start($, w)

  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await ui.press({ key: 'tab-all' })
  await w.clock.settle()
  await ui.press({ key: `proj:${P}/` })
  await w.clock.settle()
  expect(scoped(w, 'project').at(-1)).toEqual([BIN, 'stats', '--json', '--scope', 'project', '--project', P])
  expect(await ui.find({ key: `drill-cand:${P}/beta.md` })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /^Stored/ })).toBeUndefined()
  expect((await ui.find({ key: 'back' }))?.props.hotkey).toBe('b')

  await ui.press({ key: 'back' })
  await w.clock.settle()
  expect(await ui.find({ type: 'Text', text: /^Stored/ })).toBeDefined()
  expect(await ui.find({ key: 'back' })).toBeUndefined()
  await ui.unmount()
})

test('the global drill-down says it has no days instead of zero rows', async ($, on) => {
  const w = world(on)
  w.project = () => {
    const r = copy(STATS_PROJECT)
    if (r.project_stats !== undefined) {
      r.project_stats.project = 'global'
      r.project_stats.display = 'global'
    }
    return r
  }
  await start($, w)

  const ui = await $.ui.mount({ ...pane(40), surface: 'terminal' })
  await ui.press({ key: 'tab-all' })
  await w.clock.settle()
  await ui.press({ key: 'proj:global/' })
  await w.clock.settle()
  expect(scoped(w, 'project').at(-1)).toEqual([BIN, 'stats', '--json', '--scope', 'project', '--project', 'global'])
  expect((await ui.findAll({ type: 'Text', text: /^\d\d-\d\d.*\S$/ })).length).toBe(0)
  expect(await ui.find({ type: 'Text', text: /^Days +inj/ })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /^Days: none/ }))?.text).toBe('Days: none (global has no sessions)')
  // A project's own drill-down keeps its days.
  await ui.press({ key: 'back' })
  await w.clock.settle()
  w.project = () => copy(STATS_PROJECT)
  await ui.press({ key: `proj:${P}/` })
  await w.clock.settle()
  expect((await ui.findAll({ type: 'Text', text: /^\d\d-\d\d.*\S$/ })).length).toBe(7)
  await ui.unmount()
})

test('rows fit 40 columns', async ($, on) => {
  const w = world(on)
  const longFact = (r: StatsReport): StatsReport => {
    const dead = (f: NonNullable<StatsReport['project_stats']>['queue'][number]) => ({
      ...f,
      name: LONG,
      display: LONG,
      injected_30d: 123456,
      useful_30d: 98765,
      marks: [`task:${LONG}`],
    })
    const ps = r.project_stats
    if (ps !== undefined) {
      ps.display = LONG
      ps.queue = ps.queue.map(dead)
      ps.triage.noisy = ps.queue.map(dead)
      ps.triage.candidates = ps.triage.candidates.map(dead)
      ps.health = { ok: 123456, warn: 45, fail: 6, problems: [LONG, LONG, LONG, LONG] }
    }
    const a = r.all
    if (a !== undefined) {
      a.projects = a.projects.map(p => ({ ...p, display: LONG, facts: 123456, useful_7d: 98765 }))
      a.triage.candidates = a.triage.candidates.map(dead)
      a.funnel = { stored: 1234567, injected_7d: 765432, useful_7d: 1234, not_injected_30d: 987654 }
    }
    return r
  }
  w.project = () => longFact(copy(STATS_PROJECT))
  w.all = () => longFact(copy(STATS_ALL))
  await start($, w)

  for (const surface of SURFACES) {
    const ui = await $.ui.mount({ ...pane(40), surface })
    for (const key of ['tab-project', 'tab-all', 'proj:' + P + '/']) {
      await ui.press({ key })
      await w.clock.settle()
      const elements = [...(await ui.findAll({ type: 'Text' })), ...(await ui.findAll({ type: 'Button' }))]
      expect(elements.length).toBeGreaterThan(8)
      for (const el of elements) {
        expect(width(el) <= 40, `${key}: ${el.type} ${width(el)} > 40: ${el.text}`).toBe(true)
      }
      if (key === 'tab-project') {
        // The Health row is one line of three Texts and gaps.
        const cells = await ui.findAll({ type: 'Text', text: /^[●▲✕]/ })
        expect(cells).toHaveLength(3)
        const row = cells.reduce((n, c) => n + width(c), 0) + cells.length - 1
        expect(row <= 40, `health row ${row} > 40`).toBe(true)
      }
    }
    await ui.unmount()
  }
})

test('cached tab does not refetch within 120s', async ($, on) => {
  const w = world(on)
  await start($, w)

  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await ui.press({ key: 'tab-project' })
  await w.clock.settle()
  await ui.press({ key: 'tab-session' })
  await ui.press({ key: 'tab-project' })
  await w.clock.settle()
  expect(scoped(w, 'project')).toHaveLength(1)

  await w.clock.advance(119_000)
  await ui.press({ key: 'tab-session' })
  await ui.press({ key: 'tab-project' })
  await w.clock.settle()
  expect(scoped(w, 'project')).toHaveLength(1)

  await w.clock.advance(2_000)
  await ui.press({ key: 'tab-session' })
  await ui.press({ key: 'tab-project' })
  await w.clock.settle()
  expect(scoped(w, 'project')).toHaveLength(2)
  expect(scoped(w, 'all')).toHaveLength(0)
  await ui.unmount()
})

test('more button raises the triage limit by 10', async ($, on) => {
  const w = world(on)
  w.project = () => {
    const r = copy(STATS_PROJECT)
    const ps = r.project_stats
    const base = ps?.queue[0]
    if (ps !== undefined && base !== undefined) {
      ps.triage.stale = Array.from({ length: 20 }, (_, i) => ({ ...base, slug: `s${i}.md`, name: `s${i}`, marks: [] }))
    }
    return r
  }
  await start($, w)

  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await ui.press({ key: 'tab-project' })
  await w.clock.settle()
  const rows = async () => (await ui.findAll({ type: 'Button' })).filter(b => b.key?.startsWith('project-stale:'))
  expect(await rows()).toHaveLength(5)
  expect((await ui.find({ key: 'more:project-stale' }))?.text).toBe('… 15 more')

  await ui.press({ key: 'more:project-stale' })
  expect(await rows()).toHaveLength(15)
  expect((await ui.find({ key: 'more:project-stale' }))?.text).toBe('… 5 more')
  await ui.unmount()
})

test('the pane timer refreshes only the visible tab', async ($, on) => {
  const w = world(on)
  await start($, w)
  await $.command.run({
    command: 'memboard',
    args: '',
    origin: { kind: 'composer' },
    presentation: { isFullscreen: true, columns: 160 },
  })
  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await ui.press({ key: 'tab-all' })
  await w.clock.settle()
  expect(scoped(w, 'all')).toHaveLength(1)

  await w.clock.advance(180_000)
  expect(scoped(w, 'all')).toHaveLength(2)
  expect(scoped(w, 'project')).toHaveLength(0)
  await ui.unmount()
})

const goTo = async (ui: { press: (t: { key: string }) => Promise<unknown> }, w: World, key: string) => {
  await ui.press({ key })
  await w.clock.settle()
}

test('a failed fetch shows its error above the cached rows, a good one clears it', async ($, on) => {
  const w = world(on)
  await start($, w)
  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await goTo(ui, w, 'tab-project')
  expect(await ui.find({ type: 'Text', text: /boom/ })).toBeUndefined()

  w.failing.add('project')
  await w.clock.advance(121_000)
  await goTo(ui, w, 'tab-session')
  await goTo(ui, w, 'tab-project')
  expect((await ui.find({ type: 'Text', text: /boom project/ }))?.text).toBe('boom project')
  expect(await ui.find({ key: `project-cand:${P}/beta.md` })).toBeDefined()

  w.failing.delete('project')
  await w.clock.advance(121_000)
  await goTo(ui, w, 'tab-session')
  await goTo(ui, w, 'tab-project')
  expect(await ui.find({ type: 'Text', text: /boom/ })).toBeUndefined()
  await ui.unmount()
})

test('an error stays with the view that raised it', async ($, on) => {
  const w = world(on)
  await start($, w)
  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await goTo(ui, w, 'tab-all')
  await goTo(ui, w, 'tab-session')

  w.failing.add('project')
  await goTo(ui, w, 'tab-project')
  expect(await ui.find({ type: 'Text', text: /boom project/ })).toBeDefined()

  // All is cached and fresh; the project error is not its own.
  await goTo(ui, w, 'tab-all')
  expect(await ui.find({ type: 'Text', text: /boom/ })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /^Stored/ })).toBeDefined()
  await ui.unmount()
})

test('a drill-down does not show the All error', async ($, on) => {
  const w = world(on)
  await start($, w)
  const ui = await $.ui.mount({ ...pane(52), surface: 'terminal' })
  await goTo(ui, w, 'tab-all')
  w.failing.add('all')
  await w.clock.advance(121_000)
  await goTo(ui, w, 'tab-session')
  await goTo(ui, w, 'tab-all')
  expect(await ui.find({ type: 'Text', text: /boom all/ })).toBeDefined()

  // The drill fetch stalls: its own view has no error, only Loading.
  w.failing.delete('all')
  w.failing.add('project')
  await ui.press({ key: `proj:${P}/` })
  await w.clock.settle()
  expect(await ui.find({ type: 'Text', text: /boom all/ })).toBeUndefined()
  expect(await ui.find({ type: 'Text', text: /boom project/ })).toBeDefined()
  w.failing.delete('project')
  await ui.press({ key: 'back' })
  await ui.press({ key: `proj:${P}/` })
  await w.clock.settle()
  expect(await ui.find({ type: 'Text', text: /boom/ })).toBeUndefined()
  await ui.unmount()
})
