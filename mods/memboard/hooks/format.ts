import type { FactKey, SessionStats } from '../types'

/** The status line: injected this session, useful citations, useful share. */
export function statusLine(s: Pick<SessionStats, 'injected' | 'useful' | 'useful_share'>): string {
  return `mem ${s.injected}↓ ✓${s.useful} · ${Math.round(s.useful_share * 100)}%`
}

/** `text` cut to `width` columns, the cut marked with `…`. */
export function fit(text: string, width: number): string {
  const chars = Array.from(text)
  if (chars.length <= width) return text
  if (width <= 0) return ''
  return `${chars.slice(0, width - 1).join('')}…`
}

/** `text` fitted to `width` and padded on the right to exactly `width`. */
export function padEnd(text: string, width: number): string {
  const cut = fit(text, width)
  return cut + ' '.repeat(Math.max(0, width - Array.from(cut).length))
}

/** `text` fitted to `width` and padded on the left to exactly `width`. */
export function padStart(text: string, width: number): string {
  const cut = fit(text, width)
  return ' '.repeat(Math.max(0, width - Array.from(cut).length)) + cut
}

/** A bar `width` cells long, `share` (0..1) of it filled. */
export function bar(share: number, width: number): string {
  const cells = Math.max(0, width)
  const filled = Math.round(Math.min(1, Math.max(0, share)) * cells)
  return '█'.repeat(filled) + '░'.repeat(cells - filled)
}

/** Words laid out in lines of at most `width` columns; a longer word is cut. */
export function wrapWords(text: string, width: number): string[] {
  const lines: string[] = []
  let line = ''
  for (const word of text.split(/\s+/).filter(w => w !== '')) {
    const next = line === '' ? word : `${line} ${word}`
    if (Array.from(next).length <= width) {
      line = next
      continue
    }
    if (line !== '') lines.push(line)
    line = fit(word, width)
  }
  if (line !== '') lines.push(line)
  return lines
}

/** A fact type in four columns (`feed`, `mist`), `-` when the file is gone. */
export function shortType(type: string): string {
  return padEnd(type === '' ? '-' : type.slice(0, 4), 4)
}

/** A row Button's key: unique per list, so one fact may stand in several. */
export function rowKey(list: string, k: FactKey): string {
  return `${list}:${k.project}/${k.slug}`
}

/** `1 fact`, `2 facts`. */
export function count(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}
