import type { FactKey, SessionStats, TurnFact } from '../types'

// A fact header as hypomnema injects it: `## <name> — <file.md> (<type>, <created>)`,
// the `, <created>` part absent when the fact has none.
const HEADER = /^## (.+) — (\S+\.md) \(([^,)]+)(?:, [^)]*)?\)$/

/** The fact headers in the hooks' additionalContext, in order, one per file. */
export function parseInjected(contexts: readonly string[]): TurnFact[] {
  const facts: TurnFact[] = []
  const seen = new Set<string>()
  for (const context of contexts) {
    for (const line of context.split(/\r?\n/)) {
      const m = HEADER.exec(line)
      const [, name, file, type] = m ?? []
      if (name === undefined || file === undefined || type === undefined || seen.has(file)) {
        continue
      }
      seen.add(file)
      facts.push({ name, file, type })
    }
  }
  return facts
}

/**
 * The store a turn fact came from: the first `delivered` entry with its file,
 * the session's own store before the global one; undefined until a refresh
 * has seen the fact delivered.
 */
export function toKey(fact: TurnFact, s: SessionStats, sessionProject: string): FactKey | undefined {
  const hits = s.delivered.filter(k => k.slug === fact.file)
  return hits.find(k => k.project === sessionProject) ?? hits.find(k => k.project === 'global')
}
