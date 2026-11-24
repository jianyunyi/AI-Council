export interface MentionToken {
  raw: string
  handle: string
  start: number
  end: number
}

const HANDLE_CHAR = /[A-Za-z0-9._-]/

function isLineStart(s: string, i: number): boolean {
  return i === 0 || s[i - 1] === '\n'
}

/**
 * Mirrors the Go ParseMentions in internal/app/group/mention.go. Offsets are
 * UTF-16 code-unit indices (native JS string indices), so they can be used
 * directly for slicing and highlighting.
 */
export function parseMentions(body: string): MentionToken[] {
  const out: MentionToken[] = []
  let fence = false
  let i = 0
  while (i < body.length) {
    const ch = body[i]
    if (ch === '`' && body.startsWith('```', i)) {
      fence = !fence
      i += 3
      continue
    }
    if (fence) {
      i++
      continue
    }
    if (ch === '`') {
      const end = body.indexOf('`', i + 1)
      if (end !== -1 && !body.slice(i + 1, end).includes('\n')) {
        i = end + 1
        continue
      }
    }
    if (ch === '>' && isLineStart(body, i)) {
      const nl = body.indexOf('\n', i)
      if (nl === -1) break
      i = nl + 1
      continue
    }
    if (ch !== '@') {
      i++
      continue
    }
    if (i > 0) {
      const prev = body[i - 1]
      if (HANDLE_CHAR.test(prev) || prev === '@') {
        i++
        continue
      }
    }
    let j = i + 1
    while (j < body.length && j - i - 1 < 64 && HANDLE_CHAR.test(body[j])) j++
    const handle = body.slice(i + 1, j)
    if (handle.length >= 2 && !handle.startsWith('.') && !handle.endsWith('.') && !handle.includes('..')) {
      out.push({
        raw: body.slice(i, j),
        handle: handle.toLowerCase(),
        start: i,
        end: j,
      })
    }
    i = j
  }
  return out
}

export interface TextSegment {
  text: string
  mention?: MentionToken & { resolved?: boolean }
}

/** Splits a message body into plain and mention segments for rendering. */
export function splitSegments(body: string, resolvedHandles: Set<string>): TextSegment[] {
  const tokens = parseMentions(body)
  if (tokens.length === 0) return [{ text: body }]
  const segs: TextSegment[] = []
  let last = 0
  for (const t of tokens) {
    if (t.start > last) segs.push({ text: body.slice(last, t.start) })
    segs.push({
      text: body.slice(t.start, t.end),
      mention: { ...t, resolved: resolvedHandles.has(t.handle) },
    })
    last = t.end
  }
  if (last < body.length) segs.push({ text: body.slice(last) })
  return segs
}
