import type { FactKey, FactRef, ProjectStats } from '../../types'
import { bar, count, fit, joinFit, padEnd, padStart } from '../format'
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
}

const PROBLEMS = 3
const DAY_PREFIX = 5 + 1 + 4 + 1 + 3 + 1 + 6 + 1

/** One project: size, the last 7 days, composition, health, triage, queue. */
export function ProjectView({ ui, width, stats, scope, limits, onOpen, onMore }: Props) {
  const { Box, Text } = ui
  const limit = (list: string) => limits[list] ?? DEFAULT_LIMIT
  const peak = Math.max(1, ...stats.days.map(d => d.injected))
  const barWidth = Math.min(12, width - DAY_PREFIX)
  const types = Object.entries(stats.by_type)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([type, n]) => `${type} ${n}`)
  const mix = joinFit(types, Math.max(1, width - 5))
  const { health } = stats

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
      <Text bold>{fit(`${stats.display}  ${stats.visible} visible`, width)}</Text>
      <Text dimColor>{fit(`${stats.local} own + ${stats.global} global`, width)}</Text>
      <Box flexDirection="column" marginTop={1}>
        <Text dimColor>
          {fit(`${padEnd('Days', 5)} ${padStart('inj', 4)} ${padStart('✓', 3)} ${padStart('silent', 6)}`, width)}
        </Text>
        {stats.days.map(d => (
          <Text>
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
      <Box flexDirection="column" marginTop={1}>
        {mix.map((line, i) => (
          <Text>{fit(`${i === 0 ? 'Mix  ' : '     '}${line}`, width)}</Text>
        ))}
        <Text>{fit(`candidates ${stats.candidates} · stale ${stats.stale} · pinned ${stats.pinned}`, width)}</Text>
      </Box>
      <Box flexDirection="column" marginTop={1}>
        <Box flexDirection="row" gap={1}>
          <Text bold>Health</Text>
          <Text color="success">{`● ok ${health.ok}`}</Text>
          <Text color="warning">{`▲ warn ${health.warn}`}</Text>
          <Text color="error">{`✕ fail ${health.fail}`}</Text>
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
        <Text bold>{fit(`Queue (${count(stats.queue.length, 'fact')})`, width)}</Text>
        {stats.queue.length === 0 && <Text dimColor>nothing queued</Text>}
        {stats.queue.slice(0, limit(queueList)).map(fact => {
          const task = taskOf(fact)
          return (
            <Box flexDirection="column">
              <FactRow ui={ui} fact={fact} width={width} list={queueList} onOpen={onOpen} />
              {task !== undefined && <Text dimColor>{fit(`  ↳ ${task}`, width)}</Text>}
            </Box>
          )
        })}
      </Box>
    </Box>
  )
}
