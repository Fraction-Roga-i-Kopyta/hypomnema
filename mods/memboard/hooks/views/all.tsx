import type { AllStats, FactKey, FactRef } from '../../types'
import { bar, fit, joinFit, padEnd, padStart, rowKey } from '../format'
import { DEFAULT_LIMIT, TriageBlock } from './rows'
import type { Els } from './session'

type Props = {
  ui: Els
  width: number
  stats: AllStats
  limits: Record<string, number>
  onOpen: (fact: FactKey) => void
  onMore: (list: string) => void
  onDrill: (project: string) => void
}

const PROJECTS_TOP = 5
const FUNNEL_PREFIX = 11 + 1 + 7

/** All stores: the funnel, the projects, the types, triage across stores. */
export function AllView({ ui, width, stats, limits, onOpen, onMore, onDrill }: Props) {
  const { Box, Text, Button } = ui
  const { funnel } = stats
  const barWidth = Math.min(20, width - FUNNEL_PREFIX - 1)
  const stage = (label: string, n: number) => {
    const share = funnel.stored > 0 ? n / funnel.stored : 0
    return (
      <Text color={label === 'Useful 7d' ? 'success' : 'text'}>
        {fit(`${padEnd(label, 11)} ${padStart(String(n), 7)}${barWidth > 0 ? ` ${bar(share, barWidth)}` : ''}`, width)}
      </Text>
    )
  }

  const shown = limits.projects ?? PROJECTS_TOP
  const hidden = stats.projects.length - shown
  const nameWidth = Math.max(1, width - 13)
  const types = Object.entries(stats.by_type)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([type, n]) => `${type} ${n}`)
  const typeLines = joinFit(types, Math.max(1, width - 6))

  const block = (title: string, key: string, facts: FactRef[]) => {
    const list = `all-${key}`
    return (
      <TriageBlock
        ui={ui}
        title={title}
        facts={facts}
        limit={limits[list] ?? DEFAULT_LIMIT}
        width={width}
        list={list}
        qualify
        onOpen={onOpen}
        onMore={onMore}
      />
    )
  }

  return (
    <Box flexDirection="column">
      <Text bold color="text">{fit(`All stores (${stats.stores}) · ${stats.facts} facts · 7 days`, width)}</Text>
      <Box flexDirection="column" marginTop={1}>
        {stage('Stored', funnel.stored)}
        {stage('Injected 7d', funnel.injected_7d)}
        {stage('Useful 7d', funnel.useful_7d)}
        <Text dimColor>{fit(`not injected 30d: ${funnel.not_injected_30d}`, width)}</Text>
      </Box>
      <Box flexDirection="column" marginTop={1}>
        <Text bold color="text">{fit(`${padEnd('Projects', nameWidth)} ${padStart('facts', 6)} ${padStart('✓7d', 5)}`, width)}</Text>
        {stats.projects.slice(0, shown).map(p => (
          <Button key={rowKey('proj', { project: p.project, slug: '' })} plain onPress={() => onDrill(p.project)}>
            <Text color="text">
              {fit(
                `${padEnd(p.display, nameWidth)} ${padStart(String(p.facts), 6)} ${padStart(String(p.useful_7d), 5)}`,
                width,
              )}
            </Text>
          </Button>
        ))}
        {hidden > 0 && (
          <Button key="more:projects" plain onPress={() => onMore('projects')}>
            <Text color="text">{fit(`… ${hidden} more`, width)}</Text>
          </Button>
        )}
      </Box>
      <Box flexDirection="column" marginTop={1}>
        {typeLines.map((line, i) => (
          <Text color="text">{fit(`${i === 0 ? 'Types ' : '      '}${line}`, width)}</Text>
        ))}
      </Box>
      {block('Noisy', 'noisy', stats.triage.noisy)}
      {block('Candidates', 'cand', stats.triage.candidates)}
      {block('Stale', 'stale', stats.triage.stale)}
    </Box>
  )
}
