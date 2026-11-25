'use client'

import { useEffect, useRef, useState } from 'react'
import { eventsUrl, type GroupEvent } from '@/lib/api/groups'
import { getDesktopRuntime } from '@/lib/desktop'
import { subscribeSSE, type SSESubscription } from '@/lib/stream'

export type StreamStatus = 'connecting' | 'open' | 'closed' | 'error'

/**
 * Subscribes to the group event stream.
 *
 * Two transports, one resume contract (Last-Event-ID / after_seq, server
 * replays from the durable group_events table):
 *  - browser: native EventSource (cookie session)
 *  - desktop: fetch-based subscribeSSE with the Bearer session token
 *    (EventSource cannot set Authorization headers)
 *
 * onEvent must be stable-ish; the latest callback is always used.
 */
export function useGroupStream(
  groupId: string | null,
  afterSeq: number,
  onEvent: (ev: GroupEvent) => void,
): { status: StreamStatus; lastSeq: number } {
  const [status, setStatus] = useState<StreamStatus>('closed')
  const [lastSeq, setLastSeq] = useState(afterSeq)
  const cbRef = useRef(onEvent)
  cbRef.current = onEvent
  const afterRef = useRef(afterSeq)
  afterRef.current = Math.max(afterRef.current, afterSeq)

  useEffect(() => {
    if (!groupId) {
      setStatus('closed')
      return
    }
    setStatus('connecting')
    const handle = (ev: GroupEvent) => {
      setLastSeq(ev.seq)
      cbRef.current(ev)
    }

    const desktop = getDesktopRuntime()
    if (desktop) {
      const sub: SSESubscription = subscribeSSE<GroupEvent>(
        eventsUrl(groupId, afterRef.current),
        { token: desktop.sessionToken, initialAfter: afterRef.current },
        {
          onEvent: handle,
          onError: () => setStatus('error'),
        },
      )
      setStatus('open')
      return () => {
        sub.close()
        setStatus('closed')
      }
    }

    const es = new EventSource(eventsUrl(groupId, afterRef.current), { withCredentials: true })
    es.onopen = () => setStatus('open')
    es.onerror = () => setStatus(es.readyState === EventSource.CLOSED ? 'closed' : 'error')
    es.onmessage = (e: MessageEvent<string>) => {
      try {
        handle(JSON.parse(e.data) as GroupEvent)
      } catch {
        /* ignore malformed frames */
      }
    }
    return () => {
      es.close()
      setStatus('closed')
    }
  }, [groupId])

  return { status, lastSeq }
}
