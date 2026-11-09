import type { EngineInterface } from 'claude-code'

export type Action =
  | 'archive'
  | 'revive'
  | 'pin'
  | 'unpin'
  | 'hide'
  | 'less'
  | 'clear'
  | 'rewrite'
  | 'split'
  | 'merge'
  | 'review'
  | 'done'

export type ActResult = { ok: boolean; message: string }

const PAST: Record<Action, string> = {
  archive: 'Archived',
  revive: 'Restored',
  pin: 'Pinned',
  unpin: 'Unpinned',
  hide: 'Hidden here',
  less: 'Showing less',
  clear: 'Marks cleared',
  rewrite: 'Marked: rewrite',
  split: 'Marked: split',
  merge: 'Marked: merge',
  review: 'Marked: review',
  done: 'Task done',
}

/** The memoryctl argv tail for an action (`retire`, `revive`, `pin`, `unpin`, or `mark <directive>`). */
function verb(action: Action, slug: string, project: string): string[] {
  const target = ['--project', project]
  switch (action) {
    case 'archive':
      return ['retire', slug, ...target]
    case 'revive':
    case 'pin':
    case 'unpin':
      return [action, slug, ...target]
    default:
      return ['mark', slug, action, ...target]
  }
}

function firstLine(text: string): string {
  return text.split('\n').find(line => line.trim() !== '')?.trim() ?? ''
}

/**
 * Run one action on a fact. `sessionId` rides in HYPOMNEMA_SESSION_ID so the
 * WAL rows belong to the session. Never throws: a failure is `ok: false` with
 * stderr's first line.
 */
export async function act(
  $: EngineInterface,
  bin: string,
  sessionId: string,
  action: Action,
  fact: { project: string; slug: string },
): Promise<ActResult> {
  try {
    const run = await $.process.run([bin, ...verb(action, fact.slug, fact.project)], {
      env: { HYPOMNEMA_SESSION_ID: sessionId },
      timeoutMs: 15_000,
    })
    if (run.exitCode !== 0) {
      return { ok: false, message: firstLine(run.stderr) || `memoryctl exited ${run.exitCode}` }
    }
    return { ok: true, message: `${PAST[action]}: ${fact.slug.replace(/\.md$/, '')}` }
  } catch (err) {
    return { ok: false, message: firstLine(String(err)) || 'memoryctl did not run' }
  }
}
