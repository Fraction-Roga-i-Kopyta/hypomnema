import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { Tab } from '../types'
import { fit, statusLine } from './format'
import { locate, stats } from './memoryctl'
import { parseInjected } from './turn'
import { SessionView } from './views/session'

const PANE = 'memboard'
const HINT = 'memoryctl not found or too old — run ./install.sh in the hypomnema repo'
// While the pane is open it refreshes on its own too (a subagent's turn ends
// with SubagentStop, which raises no refresh).
const PANE_REFRESH_MS = 180_000
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
    refresh($).catch(() => $.ui.status(undefined))
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
    const body =
      active === 'session' ? (
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
              onPress={() => update($, tab, () => t)}
            />
          ))}
        </Box>
        <Text dimColor>{rule(name, width)}</Text>
        {body}
      </Box>
    )
  })
}
