import type { EngineInterface, ProcessRunResult } from 'claude-code'

import type { StatsReport } from '../types'

export type { StatsReport }

export type StatsResult = { ok: true; report: StatsReport } | { ok: false; error: string }

const SCHEMA_VERSION = 1

const LOCATE = 'p="$HOME/.claude/bin/memoryctl"; [ -x "$p" ] && echo "$p" || command -v memoryctl'

/** The memoryctl binary: the one install.sh links, else the first on PATH. */
export async function locate($: EngineInterface): Promise<string | undefined> {
  try {
    const { exitCode, stdout } = await $.process.run(['/bin/sh', '-c', LOCATE], { timeoutMs: 5_000 })
    const path = stdout.trim().split('\n')[0]?.trim() ?? ''
    return exitCode === 0 && path !== '' ? path : undefined
  } catch {
    return undefined
  }
}

/**
 * `memoryctl stats --json <args>` for the session `sessionId`. A non-zero exit
 * (an old binary answers `unknown command "stats"`), output that is not a
 * report, or another schema_version is `ok: false` with one line saying why.
 */
export async function stats(
  $: EngineInterface,
  bin: string,
  args: string[],
  sessionId: string,
): Promise<StatsResult> {
  let run: ProcessRunResult
  try {
    run = await $.process.run([bin, 'stats', '--json', ...args], {
      env: { HYPOMNEMA_SESSION_ID: sessionId },
      timeoutMs: 15_000,
    })
  } catch (err) {
    return { ok: false, error: firstLine(String(err)) || 'memoryctl stats did not run' }
  }
  if (run.exitCode !== 0) {
    return { ok: false, error: firstLine(run.stderr) || `memoryctl stats exited ${run.exitCode}` }
  }
  let report: unknown
  try {
    report = JSON.parse(run.stdout)
  } catch {
    return { ok: false, error: 'memoryctl stats printed no JSON' }
  }
  const version = isObject(report) ? report.schema_version : undefined
  if (version !== SCHEMA_VERSION) {
    return { ok: false, error: `memoryctl stats schema_version ${String(version)}, want ${SCHEMA_VERSION}` }
  }
  return { ok: true, report: report as StatsReport }
}

function firstLine(text: string): string {
  return text.split('\n').find(line => line.trim() !== '')?.trim() ?? ''
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null
}
