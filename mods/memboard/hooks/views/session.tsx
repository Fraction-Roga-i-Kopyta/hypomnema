import type { BoxProps, ButtonProps, ElementConstructor, TextProps } from 'claude-code'

import type { FactKey, SessionStats, TurnFact } from '../../types'
import { bar, count, fit, padEnd, padStart, rowKey, shortType, wrapWords } from '../format'
import { toKey } from '../turn'

/** The elements a view draws with, from `$.ui.resolve(e)`. */
export type Els = {
  Box: ElementConstructor<BoxProps>
  Text: ElementConstructor<TextProps>
  Button: ElementConstructor<ButtonProps>
}

type Props = {
  ui: Els
  width: number
  session: SessionStats | null
  turn: TurnFact[]
  sessionProject: string | null
  onOpen: (fact: FactKey) => void
}

const TOP = 5
const PULL_ONLY = 'pull-only mode: prompts inject nothing; facts read with Read are not counted'

const isKey = (a: FactKey) => (b: FactKey) => a.project === b.project && a.slug === b.slug

/** One run of a styled line. */
export type Seg = [text: string, props: TextProps]

/** The runs of a styled line; one plain run when the whole line must be cut to `width`. */
export function runs(Text: Els['Text'], width: number, segs: Seg[]) {
  const full = segs.map(s => s[0]).join('')
  if (Array.from(full).length > width) {
    return [
      <Text key="cut" color="text">
        {fit(full, width)}
      </Text>,
    ]
  }
  return segs.map(([text, props], i) => (
    <Text key={`run-${i}`} color="text" {...props}>
      {text}
    </Text>
  ))
}

/** A line of styled runs; one plain run when the whole line must be cut to `width`. */
function Line({ ui, width, segs }: { ui: Els; width: number; segs: Seg[] }) {
  const { Text } = ui
  const full = segs.map(s => s[0]).join('')
  if (Array.from(full).length > width) return <Text color="text">{fit(full, width)}</Text>
  return (
    <Text color="text">
      {segs.map(([text, props], i) => (
        <Text key={`seg-${i}`} {...props}>
          {text}
        </Text>
      ))}
    </Text>
  )
}

/** The Session tab: counters, useful share, this turn's facts, the most injected. */
export function SessionView({ ui, width, session, turn, sessionProject, onOpen }: Props) {
  const { Box, Text, Button } = ui
  if (session === null) {
    return <Text dimColor>{fit('No session stats yet.', width)}</Text>
  }

  const pct = `${Math.round(session.useful_share * 100)}%`
  const barWidth = width - 12
  const share = barWidth > 0 ? `Useful ${bar(session.useful_share, barWidth)} ${padStart(pct, 4)}` : `Useful ${pct}`

  // This turn: mark (2) + name + space + type (4).
  const nameWidth = Math.max(1, width - 7)
  const turnRows = turn.map(fact => {
    const key = sessionProject === null ? undefined : toKey(fact, session, sessionProject)
    const isUseful = key !== undefined && session.useful_facts.some(isKey(key))
    const left = `${isUseful ? '✓' : '·'} ${padEnd(fact.name, nameWidth)} `
    const type = shortType(fact.type)
    if (key === undefined) {
      return (
        <Text key={`turn:${fact.name}`} color="text">
          {fit(left, width - 4)}
          <Text dimColor>{type}</Text>
        </Text>
      )
    }
    return (
      <Button key={rowKey('turn', key)} plain onPress={() => onOpen(key)}>
        <Text color={isUseful ? 'success' : 'text'}>{fit(left, width - 4)}</Text>
        <Text dimColor>{type}</Text>
      </Button>
    )
  })

  // Most injected: name + ` inj` (5) + ` ✓` (4).
  const topWidth = Math.max(1, width - 9)
  const topRows = session.top.slice(0, TOP).map(fact => (
    <Button
      key={rowKey('top', fact)}
      plain
      dimColor={fact.path === ''}
      onPress={() => onOpen({ project: fact.project, slug: fact.slug })}
    >
      {runs(Text, width, [
        [`${padEnd(fact.name || fact.slug, topWidth)} `, {}],
        [padStart(String(fact.injected), 4), { bold: true }],
        [' ', {}],
        [padStart(fact.useful === 0 ? '–' : String(fact.useful), 3), fact.useful === 0 ? {} : { color: 'success' }],
      ])}
    </Button>
  ))

  return (
    <Box flexDirection="column">
      <Line
        ui={ui}
        width={width}
        segs={[
          ['Injected ', {}],
          [String(session.injected), { bold: true }],
          ['  ', {}],
          [`✓ useful ${session.useful}`, { color: 'success', bold: true }],
          ['  · silent ', {}],
          [String(session.silent), { bold: true }],
        ]}
      />
      <Text color="text">{fit(share, width)}</Text>
      {session.undelivered > 0 && (
        <Text color="warning">{fit(`⚠ ${session.undelivered} citation(s) for facts not delivered`, width)}</Text>
      )}
      {!session.prompt_inject &&
        wrapWords(PULL_ONLY, width).map((line, i) => (
          <Text key={`pull-${i}`} dimColor>
            {line}
          </Text>
        ))}
      <Box flexDirection="column" marginTop={1}>
        <Text bold color="text">{fit(`This turn (${count(turn.length, 'fact')})`, width)}</Text>
        {turnRows.length === 0 ? <Text dimColor>{fit('nothing injected this turn', width)}</Text> : turnRows}
      </Box>
      <Box flexDirection="column" marginTop={1}>
        <Text bold color="text">{fit(`${padEnd('Most injected', topWidth)}  inj   ✓`, width)}</Text>
        {topRows.length === 0 ? <Text dimColor>{fit('nothing injected yet', width)}</Text> : topRows}
      </Box>
    </Box>
  )
}
