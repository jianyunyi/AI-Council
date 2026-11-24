/**
 * Minimal SSE client over fetch with Bearer support (desktop mode) and
 * Last-Event-ID resume. Used only when native EventSource can't be used
 * (no cookie session). Mirrors the resume semantics of EventSource.
 */
export interface SSEHandlers<T> {
  onEvent: (ev: T & { seq: number }) => void
  onError?: (err: unknown) => void
}

export interface SSESubscription {
  close: () => void
  lastSeq: () => number
}

export function subscribeSSE<T>(
  url: string,
  opts: { token?: string; initialAfter?: number },
  handlers: SSEHandlers<T>,
): SSESubscription {
  let closed = false
  let last = opts.initialAfter ?? 0
  let attempt = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined

  async function connect() {
    if (closed) return
    controller = new AbortController()
    try {
      const headers: Record<string, string> = { Accept: 'text/event-stream' }
      if (opts.token) headers.Authorization = `Bearer ${opts.token}`
      if (last > 0) headers['Last-Event-ID'] = String(last)
      const res = await fetch(url, { headers, signal: controller.signal, credentials: 'include' })
      if (!res.ok || !res.body) throw new Error(`sse status ${res.status}`)
      attempt = 0
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done || closed) break
        buf += decoder.decode(value, { stream: true })
        let idx: number
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const frame = buf.slice(0, idx)
          buf = buf.slice(idx + 2)
          let data = ''
          let id: number | undefined
          for (const line of frame.split('\n')) {
            if (line.startsWith('data:')) data += line.slice(5).trim()
            else if (line.startsWith('id:')) id = Number(line.slice(3).trim())
          }
          if (!data) continue
          try {
            const ev = JSON.parse(data) as T & { seq: number }
            if (typeof ev.seq === 'number') last = ev.seq
            else if (id !== undefined) last = id
            handlers.onEvent(ev)
          } catch {
            /* ignore malformed frames */
          }
        }
      }
    } catch (err) {
      if (closed) return
      handlers.onError?.(err)
    }
    if (!closed) {
      attempt++
      timer = setTimeout(connect, Math.min(1000 * 2 ** attempt, 15000))
    }
  }

  void connect()
  return {
    close: () => {
      closed = true
      if (timer) clearTimeout(timer)
      controller?.abort()
    },
    lastSeq: () => last,
  }
}
