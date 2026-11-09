// The memboard contract: the JSON of `memoryctl stats --json`
// (schema_version 1, json tags of internal/stats/types.go verbatim) and the
// mod's $.state values.
//
// Placeholders the Go side may send: a fact whose file is gone has
// `path: ""` and empty `type` / `status` / `created`.

export type FactKey = { project: string; slug: string }

export type FactRef = {
  project: string
  display: string
  slug: string
  name: string
  type: string
  status: string
  description: string
  path: string
  created: string
  ref_count: number
  effectiveness: number
  last_injected: string
  last_useful: string
  injected_30d: number
  useful_30d: number
  /** Display form: `hide:<project>`, `less`, `task:<directive>`. */
  marks: string[]
}

export type FactCount = FactRef & { injected: number; useful: number }

export type Day = { date: string; injected: number; useful: number; silent: number }

export type Triage = { noisy: FactRef[]; candidates: FactRef[]; stale: FactRef[] }

export type Health = { ok: number; warn: number; fail: number; problems: string[] }

export type SessionStats = {
  session_id: string
  injected: number
  injected_facts: number
  useful: number
  silent: number
  undelivered: number
  recalled: number
  useful_share: number
  prompt_inject: boolean
  delivered: FactKey[]
  useful_facts: FactKey[]
  top: FactCount[]
}

export type ProjectStats = {
  project: string
  display: string
  visible: number
  local: number
  global: number
  by_type: Record<string, number>
  candidates: number
  stale: number
  pinned: number
  days: Day[]
  health: Health
  triage: Triage
  queue: FactRef[]
}

export type ProjectRow = { project: string; display: string; facts: number; useful_7d: number }

export type Funnel = {
  stored: number
  injected_7d: number
  useful_7d: number
  not_injected_30d: number
}

export type AllStats = {
  stores: number
  facts: number
  funnel: Funnel
  projects: ProjectRow[]
  by_type: Record<string, number>
  triage: Triage
}

export type StatsReport = {
  schema_version: number
  scope: 'session' | 'project' | 'all' | 'fact'
  today: string
  /** Store tag of the calling session's project. */
  project: string
  session?: SessionStats
  project_stats?: ProjectStats
  all?: AllStats
  fact?: FactCount
}

/** A fact header hypomnema injected on the current turn. */
export type TurnFact = { name: string; file: string; type: string }

export type Tab = 'session' | 'project' | 'all'

declare module 'claude-code' {
  interface PluginState {
    memboard: {
      tab: Shaped<Tab>
      isOpen: Shaped<boolean>
      turn: Shaped<TurnFact[]>
      /** The last `stats --scope session` result. */
      session: Shaped<SessionStats | null>
      /** The located memoryctl binary; null when none was found. */
      bin: Shaped<string | null>
      /** The last stats failure, one line; null after a good refresh. */
      error: Shaped<string | null>
      /** From the classic hooks' input: it changes after /clear. */
      sessionId: Shaped<string | null>
      /** The report's top-level `project` (the session's store tag). */
      sessionProject: Shaped<string | null>
    }
  }
}
