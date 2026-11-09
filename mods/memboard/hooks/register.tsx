import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { FactKey, Tab } from '../types'
import { fit, statusLine } from './format'
import { locate, stats } from './memoryctl'
import { parseInjected } from './turn'
import { AllView } from './views/all'
import { ProjectView } from './views/project'
import { DEFAULT_LIMIT, MORE_STEP } from './views/rows'
import { SessionView } from './views/session'

const PANE = 'memboard'
const HINT = 'memoryctl not found or too old — run ./install.sh in the hypomnema repo'
// While the pane is open it refreshes on its own too (a subagent's turn ends
// with SubagentStop, which raises no refresh).
const PANE_REFRESH_MS = 180_000
// A tab opened within this long of its last fetch shows what it has.
const CACHE_MS = 120_000
const TOGGLE = /^\s*\/memboard(\s|$)/

const TABS: readonly (readonly [Tab, string, string])[] = [
  ['session', '1', 'Session'],
  ['project', '2', 'Project'],
  ['all', '3', 'All'],
]

// Bump a shape tag when that value's fields change: a reload keeps $.state,
// and the tag is how new code declines what old code stored.
const tab = atom({ plugin: 'memboard', key: 'tab' } as const, 'session', { shape: 'tab-1' })
const isOpen = atom({ plugin: 'memboard', key: 'isOpen' } as const, false, { shape: 'open-1' })
const turn = atom({ plugin: 'memboard', key: 'turn' } as const, [], { shape: 'turn-1' })
const session = atom({ plugin: 'memboard', key: 'session' } as const, null, { shape: 'stats-1' })
const bin = atom({ plugin: 'memboard', key: 'bin' } as const, null, { shape: 'bin-1' })
const error = atom({ plugin: 'memboard', key: 'error' } as const, null, { shape: 'error-1' })
const sessionId = atom({ plugin: 'memboard', key: 'sessionId' } as const, null, { shape: 'sid-1' })
const sessionProject = atom({ plugin: 'memboard', key: 'sessionProject' } as const, null, {
  shape: 'project-1',
})
const project = atom({ plugin: 'memboard', key: 'project' } as const, null, { shape: 'cache-1' })
const all = atom({ plugin: 'memboard', key: 'all' } as const, null, { shape: 'cache-1' })
const drill = atom({ plugin: 'memboard', key: 'drill' } as const, null, { shape: 'drill-1' })
const drillCache = atom({ plugin: 'memboard', key: 'drillCache' } as const, null, { shape: 'dcache-1' })
const tabError = atom({ plugin: 'memboard', key: 'tabError' } as const, null, { shape: 'tabError-2' })
const limits = atom({ plugin: 'memboard', key: 'limits' } as const, {}, { shape: 'limits-1' })
const selected = atom({ plugin: 'memboard', key: 'selected' } as const, null, { shape: 'selected-1' })

/** `stats --scope session` into state and the status line; any failure clears the line. */
async function refresh($: EngineInterface): Promise<void> {
  let path = await read($, bin)
  if (path === null) {
    path = (await locate($)) ?? null
    if (path === null) {
      $.ui.status(undefined)
      return
    }
    const found = path
    await update($, bin, () => found)
  }
  const sid = await read($, sessionId)
  if (sid === null) return

  const r = await stats($, path, ['--scope', 'session'], sid)
  // A /clear while stats ran: this answer is the old session's; the new id's
  // own refresh writes.
  if ((await read($, sessionId)) !== sid) return
  const s = r.ok ? r.report.session : undefined
  if (!r.ok || s === undefined) {
    const why = r.ok ? 'memoryctl stats sent no session' : r.error
    await update($, error, () => why)
    // Locate again next time: ./install.sh may have replaced or moved it.
    await update($, bin, () => null)
    $.ui.status(undefined)
    return
  }
  await update($, session, () => s)
  await update($, sessionProject, () => r.report.project)
  await update($, error, () => null)
  $.ui.status(statusLine(s))
}

/**
 * The visible Project / All tab (or the drill-down over All) from memoryctl.
 * Without `force` a cache younger than CACHE_MS stands. Nothing runs for the
 * Session tab: its numbers are `refresh`'s.
 */
/** The view a fetch serves: `project`, `all`, or `drill:<tag>`. */
function viewOf(active: Tab, tag: string | null): string {
  return active === 'all' && tag !== null ? `drill:${tag}` : active
}

async function loadTab($: EngineInterface, force: boolean): Promise<void> {
  const active = await read($, tab)
  if (active === 'session') return
  const tag = active === 'all' ? await read($, drill) : null
  const projectCache = await read($, project)
  const allCache = await read($, all)
  const dCache = await read($, drillCache)
  const cache = active === 'project' ? projectCache : tag === null ? allCache : dCache?.tag === tag ? dCache : null
  const now = await $.clock.now()
  if (!force && cache !== null && now - cache.fetchedAt < CACHE_MS) return

  const path = await read($, bin)
  const sid = await read($, sessionId)
  if (path === null || sid === null) return
  // The error belongs to the view that asked, not to whichever is showing later.
  const view = viewOf(active, tag)
  const args =
    active === 'project'
      ? ['--scope', 'project']
      : tag === null
        ? ['--scope', 'all']
        : ['--scope', 'project', '--project', tag]
  const r = await stats($, path, args, sid)
  if ((await read($, sessionId)) !== sid) return
  const body = r.ok ? (active === 'all' && tag === null ? r.report.all : r.report.project_stats) : undefined
  if (!r.ok || body === undefined) {
    // The tabs stay: only this view says why it has nothing new.
    const message = r.ok ? `memoryctl stats sent no ${args[1]} report` : r.error
    await update($, tabError, () => ({ view, message }))
    return
  }
  const fetchedAt = await $.clock.now()
  const report = r.report
  if (active === 'project') await update($, project, () => ({ report, fetchedAt }))
  else if (tag === null) await update($, all, () => ({ report, fetchedAt }))
  else await update($, drillCache, () => ({ report, fetchedAt, tag }))
  await update($, tabError, e => (e?.view === view ? null : e))
}

/** The open pane's timer tick: the visible tab only. */
async function tick($: EngineInterface): Promise<void> {
  if ((await read($, tab)) === 'session') await refresh($)
  else await loadTab($, true)
}

/** A refresh on the next tick: the hook that asks for it never waits on it. */
function later($: EngineInterface): void {
  try {
    $.clock.after(0, () => {
      refresh($).catch(() => $.ui.status(undefined))
    })
  } catch {
    // No clock: the next turn's end refreshes.
  }
}

// The open pane's own refresh; a reload drops it with the module.
let paneTimer: Timer | undefined

function stopPaneTimer(): void {
  paneTimer?.cancel()
  paneTimer = undefined
}

function startPaneTimer($: EngineInterface): void {
  stopPaneTimer()
  paneTimer = $.clock.every(PANE_REFRESH_MS, () => {
    tick($).catch(() => $.ui.status(undefined))
  })
}

/** `─ Session ─────`, the active tab named in words, not by color alone. */
function rule(name: string, width: number): string {
  return fit(`─ ${name} ${'─'.repeat(Math.max(0, width - name.length - 3))}`, width)
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const r = await next(e)
    try {
      await $.command.register({ name: 'memboard', description: 'Toggle the hypomnema memory dashboard' })
    } catch {
      // No /memboard this session; the status line still works.
    }
    try {
      const found = (await locate($)) ?? null
      await update($, bin, () => found)
      // A reload runs this again mid-session: the id is known, the pane may be up.
      if ((await read($, sessionId)) !== null) later($)
      if (await read($, isOpen)) {
        const isUp = (await $.ui.panes()).some(p => p.id === PANE)
        if (isUp) startPaneTimer($)
        else await update($, isOpen, () => false)
      }
    } catch {
      // The next turn's end refreshes again.
    }
    return r
  })

  // The chain hooks below read what next settled and never change it; their
  // .catch replays that settlement (or runs the rest when next was not yet
  // called), so a failure here never skips or re-runs a settings hook.
  on('classic.SessionStart', async ($, e, next) => {
    const r = await next(e)
    try {
      const facts = parseInjected(r.additionalContext ?? [])
      const isNew = (await read($, sessionId)) !== e.session_id
      await update($, sessionId, () => e.session_id)
      await update($, turn, () => facts)
      if (isNew) later($)
    } catch {
      // Leave the last turn as it was.
    }
    return r
  }).catch(($, e, next) => next(e))

  on('classic.UserPromptSubmit', async ($, e, next) => {
    const r = await next(e)
    try {
      // The toggle never reaches the model: what it injected is no turn's.
      if (TOGGLE.test(e.prompt)) return r
      const facts = parseInjected(r.additionalContext ?? [])
      await update($, sessionId, () => e.session_id)
      await update($, turn, () => facts)
    } catch {
      // Leave the last turn as it was.
    }
    return r
  }).catch(($, e, next) => next(e))

  // The settings Stop hook (`memoryctl close`) has written the WAL once next
  // resolves; the refresh runs after the hook returns, so the turn's end never
  // waits on it.
  on('classic.Stop', async ($, e, next) => {
    const r = await next(e)
    later($)
    return r
  }).catch(($, e, next) => next(e))

  // The pane is the answer: a plugin command's text would ride in the model's
  // requests, so the toggle returns none, and a failure is a toast.
  on('command.run', { command: 'memboard' }, async $ => {
    if (await read($, isOpen)) {
      stopPaneTimer()
      await update($, isOpen, () => false)
      await $.ui.close({ id: PANE })
      return {}
    }
    // The person's own command: the pane is placed at any width.
    await $.ui.open({ id: PANE, title: 'Memory' })
    await update($, isOpen, () => true)
    startPaneTimer($)
    // The command returns at once; the tab fills when the fetch lands.
    loadTab($, false).catch(() => undefined)
    return {}
  }).catch($ => {
    $.ui.toast('memboard: the dashboard could not toggle')
    return {}
  })

  // Any close (the person's close key or mark, our own toggle) ends the pane's
  // refreshes; answering without next would keep the pane open.
  on('ui.close', { id: PANE }, async ($, e, next) => {
    const r = await next(e)
    try {
      stopPaneTimer()
      await update($, isOpen, () => false)
    } catch {
      // The next /memboard sets it again.
    }
    return r
  }).catch(($, e, next) => next(e))

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Text, Button } = $.ui.resolve(e)
    const width = e.props.bodyColumns > 0 ? e.props.bodyColumns : 52
    const path = await read($, bin)
    const failed = await read($, error)
    if (path === null || failed !== null) {
      return <Text>{HINT}</Text>
    }

    const active = await read($, tab)
    const name = TABS.find(([t]) => t === active)?.[2] ?? 'Session'
    const open = async (t: Tab) => {
      await update($, tab, () => t)
      await update($, drill, () => null)
      try {
        await loadTab($, false)
      } catch {
        // The tab shows what it has.
      }
    }
    const els = { Box, Text, Button }
    const lim = await read($, limits)
    const onOpen = (fact: FactKey) => void update($, selected, () => fact)
    const onMore = (list: string) => void update($, limits, l => ({ ...l, [list]: (l[list] ?? DEFAULT_LIMIT) + MORE_STEP }))
    const tag = await read($, drill)
    const failure = await read($, tabError)
    const why = failure !== null && failure.view === viewOf(active, tag) ? failure.message : null
    // With an error and nothing cached, the error line alone says why.
    const loading = why === null ? <Text dimColor>{fit('Loading…', width)}</Text> : null
    const projectCache = await read($, project)
    const allCache = await read($, all)
    const dCache = await read($, drillCache)
    const goDrill = async (t: string | null) => {
      await update($, drill, () => t)
      try {
        await loadTab($, false)
      } catch {
        // The view shows what it has.
      }
    }
    const body =
      active === 'project' ? (
        projectCache?.report.project_stats === undefined ? (
          loading
        ) : (
          <ProjectView
            ui={els}
            width={width}
            stats={projectCache.report.project_stats}
            scope="project"
            limits={lim}
            onOpen={onOpen}
            onMore={onMore}
          />
        )
      ) : active === 'all' && tag !== null ? (
        <Box flexDirection="column">
          <Button key="back" label="b Back" hotkey="b" onPress={() => goDrill(null)} />
          {dCache === null || dCache.tag !== tag || dCache.report.project_stats === undefined ? (
            loading
          ) : (
            <ProjectView
              ui={els}
              width={width}
              stats={dCache.report.project_stats}
              scope="drill"
              limits={lim}
              onOpen={onOpen}
              onMore={onMore}
            />
          )}
        </Box>
      ) : active === 'all' ? (
        allCache?.report.all === undefined ? (
          loading
        ) : (
          <AllView
            ui={els}
            width={width}
            stats={allCache.report.all}
            limits={lim}
            onOpen={onOpen}
            onMore={onMore}
            onDrill={t => void goDrill(t)}
          />
        )
      ) : active === 'session' ? (
        <SessionView
          ui={{ Box, Text, Button }}
          width={width}
          session={await read($, session)}
          turn={await read($, turn)}
          sessionProject={await read($, sessionProject)}
          // Rows hold the focus and keys already; the fact card opens from here.
          onOpen={() => undefined}
        />
      ) : (
        <Text dimColor>{fit(`${name}: not available yet.`, width)}</Text>
      )

    return (
      <Box flexDirection="column">
        <Box flexDirection="row" gap={1}>
          {TABS.map(([t, hotkey, label]) => (
            <Button
              key={`tab-${t}`}
              label={`${hotkey} ${label}`}
              hotkey={hotkey}
              {...(t === active ? { variant: 'primary' as const } : {})}
              onPress={() => open(t)}
            />
          ))}
        </Box>
        <Text dimColor>{rule(name, width)}</Text>
        {why !== null && <Text color="warning">{fit(why, width)}</Text>}
        {body}
      </Box>
    )
  })
}
