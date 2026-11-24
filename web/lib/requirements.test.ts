import { describe, expect, it } from 'vitest'
import { canCancel, canRetry, previewDispatch, stateMeta } from './requirements'
import type { RequirementView } from './api/groups'

const req = (state: RequirementView['state']): Pick<RequirementView, 'state'> => ({ state })

describe('requirement helpers', () => {
  it('labels every P6 state', () => {
    expect(stateMeta('dispatched').label).toBe('已派发')
    expect(stateMeta('proposing').label).toBe('正在生成方案')
    expect(stateMeta('proposed').label).toBe('已提案 · 待审核')
    expect(stateMeta('proposal_failed').tone).toBe('danger')
    expect(stateMeta('failed').tone).toBe('danger')
    expect(stateMeta('cancelled').tone).toBe('muted')
    expect(stateMeta('something_else').label).toBe('something_else')
  })

  it('gates retry and cancel by state', () => {
    expect(canRetry(req('proposal_failed'))).toBe(true)
    expect(canRetry(req('failed'))).toBe(true)
    expect(canRetry(req('proposed'))).toBe(false)
    expect(canCancel(req('dispatched'))).toBe(true)
    expect(canCancel(req('proposing'))).toBe(true)
    expect(canCancel(req('proposed'))).toBe(true)
    expect(canCancel(req('cancelled'))).toBe(false)
    expect(canCancel(req('failed'))).toBe(false)
  })

  it('previews dispatch fan-out from AI mentions only', () => {
    const ai = new Set(['claude-x', 'gpt-y'])
    expect(previewDispatch('@claude-x plan this', ai)).toEqual({ aiHandles: ['claude-x'], ok: true, problem: '' })
    expect(previewDispatch('@claude-x and @gpt-y both', ai).aiHandles).toEqual(['claude-x', 'gpt-y'])
    expect(previewDispatch('@claude-x @claude-x twice', ai).aiHandles).toEqual(['claude-x'])
    const none = previewDispatch('@bob plan this', ai)
    expect(none.ok).toBe(false)
    expect(none.problem).toContain('AI')
    expect(previewDispatch('no mentions at all', ai).ok).toBe(false)
    // Mentions inside code fences do not count (mirrors the backend parser).
    expect(previewDispatch('```\n@claude-x\n```', ai).ok).toBe(false)
  })
})
