import type { FactKey, FactRef, ProjectStats } from '../../types'
import { bar, count, fit, joinFit, padEnd, padStart, rowKey } from '../format'
import { DEFAULT_LIMIT, FactRow, taskOf, TriageBlock } from './rows'
import type { Els } from './session'

type Props = {
  ui: Els
  width: number
  stats: ProjectStats
  /** Names the lists (`project`, `drill`): Button keys stay unique per tab. */
  scope: string
  limits: Record<string, number>
  onOpen: (fact: FactKey) => void
  onMore: (list: string) => void
  /** Queue rows: mark the fact's open task done. */
  onDone?: (fact: FactKey) => void
}

const PROBLEMS = 3
const DONE_WIDTH = 4
const GLOBAL = 'global'
const NO_DAYS = 'Days: none (global has no sessions)'
const DAY_PREFIX = 5 + 1 + 4 + 1 + 3 + 1 + 6 + 1

/** One project: size, the last 7 days, composition, health, triage, queue. */
export function ProjectView({ ui, width, stats, scope, limits, onOpen, onMore, onDone }: Props) {
  const { Box, Text, Button } = ui
  const limit = (list: string) => limits[list] ?? DEFAULT_LIMIT
  const peak = Math.max(1, ...stats.days.map(d => d.injected))
  const barWidth = Math.min(12, width - DAY_PREFIX)
  const types = Object.entries(stats.by_type)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([type, n]) => `${type} ${n}`)
  const mix = joinFit(types, Math.max(1, width - 5))
  const { health } = stats
  // `Health ● ok 1 ▲ warn 2 ✕ fail 3` when it fits; else the label goes, then each part is cut.
  const parts = [`● ok ${health.ok}`, `▲ warn ${health.warn}`, `✕ fail ${health.fail}`]
  const joined = parts.reduce((n, p) => n + Array.from(p).length, 0) + 2
  const showLabel = joined + 7 <= width
  const cell = joined <= width ? width : Math.max(1, Math.floor((width - 2) / 3))

  const block = (title: string, key: string, facts: FactRef[]) => {
    const list = `${scope}-${key}`
    return (
      <TriageBlock
        ui={ui}
        title={title}
        facts={facts}
        limit={limit(list)}
        width={width}
        list={list}
        onOpen={onOpen}
        onMore={onMore}
      />
    )
  }

  const queueList = `${scope}-queue`
  return (
    <Box flexDirection="column">
      <Text bold color="text">{fit(`${stats.display}  ${stats.visible} visible`, width)}</Text>
      <Text dimColor>{fit(`${stats.local} own + ${stats.global} global`, width)}</Text>
      {stats.project === GLOBAL ? (
        // Sessions belong to projects: the global store's days would only ever read zero.
        <Box flexDirection="column" marginTop={1}>
          <Text dimColor>{fit(NO_DAYS, width)}</Text>
        </Box>
      ) : (
        <Box flexDirection="column" marginTop={1}>
          <Text bold color="text">
            {fit(`${padEnd('Days', 5)} ${padStart('inj', 4)} ${padStart('✓', 3)} ${padStart('silent', 6)}`, width)}
          </Text>
          {stats.days.map(d => (
            <Text color="text">
              {fit(
                `${d.date.slice(5)} ${padStart(String(d.injected), 4)} ${padStart(String(d.useful), 3)} ${padStart(
                  String(d.silent),
                  6,
                )}${barWidth > 0 ? ` ${bar(d.injected / peak, barWidth)}` : ''}`,
                width,
              )}
            </Text>
          ))}
        </Box>
      )}
      <Box flexDirection="column" marginTop={1}>
        {mix.map((line, i) => (
          <Text color="text">{fit(`${i === 0 ? 'Mix  ' : '     '}${line}`, width)}</Text>
        ))}
        <Text color="text">{fit(`candidates ${stats.candidates} · stale ${stats.stale} · pinned ${stats.pinned}`, width)}</Text>
      </Box>
      <Box flexDirection="column" marginTop={1}>
        <Box flexDirection="row" gap={1}>
          {showLabel && (
            <Text bold color="text">
              Health
            </Text>
          )}
          <Text color="success">{fit(parts[0] ?? '', cell)}</Text>
          <Text color="warning">{fit(parts[1] ?? '', cell)}</Text>
          <Text color="error">{fit(parts[2] ?? '', cell)}</Text>
        </Box>
        {health.problems.slice(0, PROBLEMS).map(p => (
          <Text dimColor>{fit(`· ${p}`, width)}</Text>
        ))}
        {health.problems.length > PROBLEMS && (
          <Text dimColor>{fit(`· … ${health.problems.length - PROBLEMS} more`, width)}</Text>
        )}
      </Box>
      {block('Noisy', 'noisy', stats.triage.noisy)}
      {block('Candidates', 'cand', stats.triage.candidates)}
      {block('Stale', 'stale', stats.triage.stale)}
      <Box flexDirection="column" marginTop={1}>
        <Text bold color="text">{fit(`Queue (${count(stats.queue.length, 'fact')})`, width)}</Text>
        {stats.queue.length === 0 && <Text dimColor>nothing queued</Text>}
        {stats.queue.slice(0, limit(queueList)).map(fact => {
          const task = taskOf(fact)
          return (
            <Box flexDirection="column">
              <FactRow ui={ui} fact={fact} width={width} list={queueList} onOpen={onOpen} />
              {task !== undefined && (
                <Box flexDirection="row" gap={1}>
                  <Text dimColor>{fit(`  ↳ ${task}`, Math.max(1, width - DONE_WIDTH - 1))}</Text>
                  <Button key={`done:${rowKey(queueList, fact)}`} plain onPress={() => onDone?.(fact)}>
                    <Text color="text">Done</Text>
                  </Button>
                </Box>
              )}
            </Box>
          )
        })}
      </Box>
    </Box>
  )
}
