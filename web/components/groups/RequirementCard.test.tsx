// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RequirementView } from '@/lib/api/groups'
import RequirementCard from './RequirementCard'

const getRequirement = vi.fn()
const retryRequirement = vi.fn()
const cancelRequirement = vi.fn()

vi.mock('@/lib/api/groups', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/lib/api/groups')>()
  return {
    ...orig,
    getRequirement: (...args: unknown[]) => getRequirement(...args),
    retryRequirement: (...args: unknown[]) => retryRequirement(...args),
    cancelRequirement: (...args: unknown[]) => cancelRequirement(...args),
  }
})

const base: RequirementView = {
  id: 'req_1',
  group_id: 'grp_1',
  anchor_message_id: 'msg_1',
  requested_by: 'u_alice',
  requested_by_member_id: 'gm_alice',
  assignee_member_id: 'gm_ai',
  assignee_ai_model_id: 'aim_1',
  assignee_handle: 'claude-x',
  title: 'plan the migration',
  body_md: '@claude-x plan the migration',
  state: 'proposed',
  version: 3,
  proposal: {
    id: 'prop_1',
    attempt: 1,
    summary: '1 command(s)',
    created_at: '2026-11-24T12:00:00Z',
    plan: { version: 1, patches: [], commands: [{ executable: 'echo', args: ['hi'] }], acceptance: [], recovery: [] },
  },
  created_at: '2026-11-24T12:00:00Z',
  updated_at: '2026-11-24T12:00:00Z',
  state_changed_at: '2026-11-24T12:00:00Z',
}

describe('RequirementCard', () => {
  afterEach(cleanup)
  beforeEach(() => {
    getRequirement.mockReset()
    retryRequirement.mockReset()
    cancelRequirement.mockReset()
  })

  it('renders the proposal summary and expands the plan', async () => {
    getRequirement.mockResolvedValue(base)
    render(<RequirementCard groupId="grp_1" requirementId="req_1" bump={0} myMemberId="gm_alice" />)
    expect(await screen.findByText('已提案 · 待审核')).toBeTruthy()
    expect(screen.getByText('1 command(s)')).toBeTruthy()
    fireEvent.click(screen.getByText('查看方案'))
    expect(screen.getByText(/echo hi/)).toBeTruthy()
    expect(screen.getByText(/尚未执行/)).toBeTruthy()
  })

  it('shows retry for the requester on failure and calls the API', async () => {
    const failed: RequirementView = { ...base, state: 'proposal_failed', failure_code: 'invalid_plan', failure_detail: 'empty_plan', proposal: undefined }
    getRequirement.mockResolvedValue(failed)
    retryRequirement.mockResolvedValue({ ...failed, state: 'dispatched', failure_code: undefined, failure_detail: undefined })
    render(<RequirementCard groupId="grp_1" requirementId="req_1" bump={0} myMemberId="gm_alice" />)
    expect(await screen.findByText(/invalid_plan/)).toBeTruthy()
    fireEvent.click(screen.getByText('重试'))
    await waitFor(() => expect(retryRequirement).toHaveBeenCalledWith('grp_1', 'req_1'))
    expect(await screen.findByText('已派发')).toBeTruthy()
  })

  it('hides actions from members who are neither requester nor manager', async () => {
    getRequirement.mockResolvedValue({ ...base, state: 'failed', failure_code: 'budget_exhausted', proposal: undefined })
    render(<RequirementCard groupId="grp_1" requirementId="req_1" bump={0} myMemberId="gm_someone_else" />)
    expect(await screen.findByText(/budget_exhausted/)).toBeTruthy()
    expect(screen.queryByText('重试')).toBeNull()
    expect(screen.queryByText('取消需求')).toBeNull()
  })

  it('refetches when bump changes', async () => {
    getRequirement.mockResolvedValue(base)
    const { rerender } = render(<RequirementCard groupId="grp_1" requirementId="req_1" bump={0} />)
    await screen.findByText('已提案 · 待审核')
    getRequirement.mockResolvedValue({ ...base, state: 'cancelled' })
    rerender(<RequirementCard groupId="grp_1" requirementId="req_1" bump={1} />)
    expect(await screen.findByText('已取消')).toBeTruthy()
    expect(getRequirement).toHaveBeenCalledTimes(2)
  })
})
