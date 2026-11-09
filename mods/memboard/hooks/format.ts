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

/** `text` cut to `width` columns from the left, the cut marked with `…`: a path keeps its file name. */
export function fitLeft(text: string, width: number): string {
  const chars = Array.from(text)
  if (chars.length <= width) return text
  if (width <= 0) return ''
  return `…${chars.slice(chars.length - (width - 1)).join('')}`
}

/**
 * The home prefix memoryctl strips from a project tag for its display form,
 * read off one tag and its display (`-Users-me-` from `-Users-me-work` and
 * `work`); undefined when the pair shows none.
 */
export function homePrefix(tag: string, display: string): string | undefined {
  if (display === '' || display === tag || !tag.endsWith(display)) return undefined
  const prefix = tag.slice(0, tag.length - display.length)
  return prefix.endsWith('-') ? prefix : undefined
}

/** A project tag in display form, as memoryctl's: the home prefix stripped. */
export function displayTag(tag: string, prefix: string | undefined): string {
  return prefix !== undefined && tag.startsWith(prefix) && tag.length > prefix.length ? tag.slice(prefix.length) : tag
}

/** Marks as the card shows them: `hide:<tag>` with the tag in display form. */
export function displayMarks(marks: string[], prefix: string | undefined): string[] {
  return marks.map(m => (m.startsWith('hide:') ? `hide:${displayTag(m.slice('hide:'.length), prefix)}` : m))
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

/** `items` joined with ` · ` into lines of at most `width` columns; a longer item is cut. */
export function joinFit(items: string[], width: number): string[] {
  const lines: string[] = []
  let line = ''
  for (const item of items) {
    const next = line === '' ? item : `${line} · ${item}`
    if (Array.from(next).length <= width) {
      line = next
      continue
    }
    if (line !== '') lines.push(line)
    line = fit(item, width)
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
