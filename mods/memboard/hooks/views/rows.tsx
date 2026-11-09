import type { FactKey, FactRef } from '../../types'
import { fit, padEnd, rowKey, shortType } from '../format'
import type { Els } from './session'

/** Rows a triage list shows before its `… N more` Button. */
export const DEFAULT_LIMIT = 5
/** What one press of `… N more` adds. */
export const MORE_STEP = 10

/** The task directive a fact carries (`task:rewrite`), if any. */
export function taskOf(fact: FactRef): string | undefined {
  const mark = fact.marks.find(m => m.startsWith('task:'))
  return mark === undefined ? undefined : mark.slice('task:'.length)
}

type RowProps = {
  ui: Els
  fact: FactRef
  width: number
  /** The list the row stands in: Button keys are unique per list. */
  list: string
  /** Prefix the name with its store (`display/name`), for lists across stores. */
  qualify?: boolean
  onOpen: (fact: FactKey) => void
}

/** `name  type  injected/useful` (30 days) as one Button. */
export function FactRow({ ui, fact, width, list, qualify, onOpen }: RowProps) {
  const { Text, Button } = ui
  const name = fact.name === '' ? fact.slug : fact.name
  const label = qualify === true ? `${fact.display}/${name}` : name
  const tail = `${shortType(fact.type)} ${fact.injected_30d}/${fact.useful_30d}`
  const nameWidth = Math.max(1, width - Array.from(tail).length - 1)
  return (
    <Button
      key={rowKey(list, fact)}
      plain
      dimColor={fact.path === ''}
      onPress={() => onOpen({ project: fact.project, slug: fact.slug })}
    >
      <Text color="text">{`${padEnd(label, nameWidth)} `}</Text>
      <Text dimColor>{tail}</Text>
    </Button>
  )
}

type BlockProps = {
  ui: Els
  title: string
  facts: FactRef[]
  limit: number
  width: number
  list: string
  qualify?: boolean
  onOpen: (fact: FactKey) => void
  /** Raise this list's limit. */
  onMore: (list: string) => void
}

/** A titled list of fact rows, cut to `limit` with a `… N more` Button. */
export function TriageBlock({ ui, title, facts, limit, width, list, qualify, onOpen, onMore }: BlockProps) {
  const { Box, Text, Button } = ui
  const hidden = facts.length - limit
  return (
    <Box flexDirection="column" marginTop={1}>
      <Text bold color="text">{fit(`${title} (${facts.length})`, width)}</Text>
      {facts.length === 0 && <Text dimColor>none</Text>}
      {facts.slice(0, limit).map(fact => (
        <FactRow ui={ui} fact={fact} width={width} list={list} qualify={qualify} onOpen={onOpen} />
      ))}
      {hidden > 0 && (
        <Button key={`more:${list}`} plain onPress={() => onMore(list)}>
          <Text color="text">{fit(`… ${hidden} more`, width)}</Text>
        </Button>
      )}
    </Box>
  )
}
