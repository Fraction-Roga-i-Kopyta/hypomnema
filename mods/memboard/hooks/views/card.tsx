import type { FactCount } from '../../types'
import type { Action } from '../actions'
import { displayMarks, fit, fitLeft, homePrefix, joinFit, wrapWords } from '../format'
import type { Els } from './session'

const DESCRIPTION_LINES = 3
const LABEL = 10
const HIDE = 'h Hide in '
// A Button draws 4 columns around its label.
const BUTTON_FRAME = 4

type Props = {
  ui: Els
  width: number
  fact: FactCount
  /** The session's project tag: Hide is offered for its facts and for `global`'s, and hides there. */
  sessionProject: string
  /** That tag in display form: the Hide button names it. */
  sessionDisplay: string
  /** `'archive'` while the archive waits for y / n. */
  confirm: 'archive' | null
  onAct: (action: Action) => void
  onAsk: () => void
  onCancel: () => void
  onBack: () => void
}

/** `description` in at most 3 lines of `width`, the last one cut with `…`. */
function describe(text: string, width: number): string[] {
  const lines = wrapWords(text, width)
  if (lines.length <= DESCRIPTION_LINES) return lines
  const kept = lines.slice(0, DESCRIPTION_LINES)
  kept[DESCRIPTION_LINES - 1] = fit(`${kept[DESCRIPTION_LINES - 1]} …`, width)
  return kept
}

/** One fact: its numbers, marks and the actions on it. */
export function FactCard({
  ui,
  width,
  fact,
  sessionProject,
  sessionDisplay,
  confirm,
  onAct,
  onAsk,
  onCancel,
  onBack,
}: Props) {
  const { Box, Text, Button } = ui
  const name = fact.name === '' ? fact.slug : fact.name
  const field = (label: string, value: string) => (
    <Text color="text">{fit(`${label.padEnd(LABEL)}${value === '' ? '-' : value}`, width)}</Text>
  )
  const hasTask = fact.marks.some(m => m.startsWith('task:'))
  const canHide = fact.project === 'global' || fact.project === sessionProject
  // The hide lands in the session's project, which a drill-down's fact may not be in: the label says where.
  const hideLabel = HIDE + fitLeft(sessionDisplay, Math.max(1, width - BUTTON_FRAME - HIDE.length))
  const act = (key: string, label: string, hotkey: string, action: Action) => (
    <Button key={key} label={label} hotkey={hotkey} onPress={() => onAct(action)} />
  )
  const prefix = homePrefix(fact.project, fact.display) ?? homePrefix(sessionProject, sessionDisplay)
  const markLines = joinFit(displayMarks(fact.marks, prefix), Math.max(1, width - LABEL))

  return (
    <Box flexDirection="column">
      <Text bold color="text">{fit(name, width)}</Text>
      {field('type', fact.type)}
      {field('status', fact.status)}
      {field('store', fact.display)}
      {field('created', fact.created)}
      {describe(fact.description, width).map(line => (
        <Text dimColor>{line}</Text>
      ))}
      <Text dimColor>{fact.path === '' ? fit('file is gone', width) : fitLeft(fact.path, width)}</Text>
      {field('ref_count', String(fact.ref_count))}
      {field('effective', fact.effectiveness.toFixed(2))}
      {field('last inj.', fact.last_injected)}
      {field('last use', fact.last_useful)}
      {markLines.length === 0
        ? field('marks', '')
        : markLines.map((line, i) => (
            <Text color="text">{fit(`${i === 0 ? 'marks'.padEnd(LABEL) : ' '.repeat(LABEL)}${line}`, width)}</Text>
          ))}
      <Box flexDirection="column" marginTop={1}>
        {confirm === 'archive' ? (
          <Box flexDirection="column">
            <Text color="warning">{fit(`Archive ${name}?`, width)}</Text>
            <Box flexDirection="row" gap={1}>
              <Button key="confirm-yes" label="y Yes" hotkey="y" variant="primary" onPress={() => onAct('archive')} />
              <Button key="confirm-no" label="n No" hotkey="n" onPress={onCancel} />
            </Box>
          </Box>
        ) : (
          <Box flexDirection="column">
            <Box flexDirection="row" gap={1}>
              <Button key="ask-archive" label="a Archive" hotkey="a" onPress={onAsk} />
              {fact.status === 'pinned'
                ? act('act-unpin', 'p Unpin', 'p', 'unpin')
                : act('act-pin', 'p Pin', 'p', 'pin')}
            </Box>
            {canHide && <Box flexDirection="row">{act('act-hide', hideLabel, 'h', 'hide')}</Box>}
            <Box flexDirection="row" gap={1}>
              {act('act-less', 'l Show less', 'l', 'less')}
              {act('act-clear', 'c Clear marks', 'c', 'clear')}
            </Box>
            <Box flexDirection="row" gap={1}>
              {act('act-rewrite', 'r Rewrite', 'r', 'rewrite')}
              {act('act-split', 's Split', 's', 'split')}
              {act('act-merge', 'm Merge', 'm', 'merge')}
            </Box>
            <Box flexDirection="row" gap={1}>
              {act('act-review', 'v Review', 'v', 'review')}
              {hasTask && act('act-done', 'd Done', 'd', 'done')}
              <Button key="back" label="b Back" hotkey="b" onPress={onBack} />
            </Box>
          </Box>
        )}
      </Box>
    </Box>
  )
}

type StubProps = {
  ui: Els
  width: number
  name: string
  onUndo: () => void
  onBack: () => void
}

/** What stands where the card was once its fact is archived. */
export function ArchivedStub({ ui, width, name, onUndo, onBack }: StubProps) {
  const { Box, Text, Button } = ui
  return (
    <Box flexDirection="column">
      <Text color="text">{fit(`archived: ${name}`, width)}</Text>
      <Box flexDirection="row" gap={1}>
        <Button key="undo" label="u Undo" hotkey="u" onPress={onUndo} />
        <Button key="back" label="b Back" hotkey="b" onPress={onBack} />
      </Box>
    </Box>
  )
}
