import { afterEach, describe, expect, it, vi } from 'vitest'
import { createGroup, GroupApiError, listMessages, listUsers, postMessage } from './groups'
import { parseMentions, splitSegments } from '../mentions'

function stubFetch(impl: (url: string, init?: RequestInit) => Promise<unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => impl(url, init)),
  )
}

function ok(data: unknown) {
  return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve(JSON.stringify({ data })) })
}

function fail(status: number, code: string, message: string) {
  return Promise.resolve({ ok: false, status, text: () => Promise.resolve(JSON.stringify({ error: { code, message } })) })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('groups api', () => {
  it('creates a group via POST /groups with the envelope unwrapped', async () => {
    stubFetch((url, init) => {
      expect(url).toContain('/groups')
      expect(init?.method).toBe('POST')
      return ok({ id: 'grp_1', name: 'T' })
    })
    const g = await createGroup({ name: 'T' })
    expect(g.id).toBe('grp_1')
  })

  it('maps error envelopes to GroupApiError with status and code', async () => {
    stubFetch(() => fail(422, 'intent_not_available', 'not yet'))
    await expect(postMessage('grp_1', { body_md: 'x', client_msg_id: 'c', intent: 'dispatch' })).rejects.toMatchObject({
      status: 422,
      code: 'intent_not_available',
    })
    await expect(postMessage('grp_1', { body_md: 'x', client_msg_id: 'c', intent: 'dispatch' })).rejects.toBeInstanceOf(GroupApiError)
  })

  it('builds message list query params', async () => {
    stubFetch((url) => {
      expect(url).toContain('before_seq=42')
      expect(url).toContain('limit=10')
      return ok({ items: [], has_more: false })
    })
    const page = await listMessages('grp_1', { before_seq: 42, limit: 10 })
    expect(page.has_more).toBe(false)
  })

  it('listUsers maps admin rows (subject is the identifier) and returns null on failure', async () => {
    stubFetch((url) => {
      expect(url).toContain('/admin/users')
      return ok([{ subject: 'alice@example.com', disabled: false, roles: [] }])
    })
    await expect(listUsers()).resolves.toEqual([{ id: 'alice@example.com', username: 'alice@example.com', display_name: 'alice@example.com' }])

    stubFetch(() => fail(403, 'forbidden', 'no'))
    await expect(listUsers()).resolves.toBeNull()
  })
})

describe('mentions parser parity with the Go implementation', () => {
  it('handles code-point offsets and exclusions', () => {
    expect(parseMentions('😀 @ab')).toEqual([{ raw: '@ab', handle: 'ab', start: 3, end: 6 }])
    expect(parseMentions('mail a@bc.com')).toEqual([])
    expect(parseMentions('`@ab` @cd').map((t) => t.handle)).toEqual(['cd'])
  })

  it('splits segments for rendering', () => {
    const segs = splitSegments('hi @claude-x !', new Set(['claude-x']))
    expect(segs).toHaveLength(3)
    expect(segs[1].mention?.resolved).toBe(true)
  })
})
