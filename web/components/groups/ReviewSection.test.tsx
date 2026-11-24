// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MemberView, RequirementView, ReviewStateView } from '@/lib/api/groups'
import ReviewSection from './ReviewSection'

const getReview = vi.fn()
const startReview = vi.fn()
const submitReviewDecision = vi.fn()
const forceCloseReview = vi.fn()
const reviseRequirement = vi.fn()

vi.mock('@/lib/api/groups', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/lib/api/groups')>()
  return {
    ...orig,
    getReview: (...args: unknown[]) => getReview(...args),
    startReview: (...args: unknown[]) => startReview(...args),
    submitReviewDecision: (...args: unknown[]) => submitReviewDecision(...args),
    forceCloseReview: (...args: unknown[]) => forceCloseReview(...args),
    reviseRequirement: (...args: unknown[]) => reviseRequirement(...args),
  }
})

const members: MemberView[] = [
  { id: 'gm_alice', group_id: 'grp_1', member_type: 'user', user_id: 'u_alice', handle: 'alice', display_name: 'Alice', group_role: 'owner', mentionable: true, added_by: 'u_alice', created_at: '2026-11-24T12:00:00Z' },
  { id: 'gm_bob', group_id: 'grp_1', member_type: 'user', user_id: 'u_bob', handle: 'bob', display_name: 'Bob', group_role: 'operator', mentionable: true, added_by: 'u_alice', created_at: '2026-11-24T12:00:00Z' },
  { id: 'gm_ai', group_id: 'grp_1', member_type: 'ai_model', ai_model_id: 'aim_1', handle: 'claude-x', display_name: 'Claude X', group_role: 'ai', ai_status: 'active', mentionable: true, added_by: 'u_alice', created_at: '2026-11-24T12:00:00Z' },
  { id: 'gm_ai2', group_id: 'grp_1', member_type: 'ai_model', ai_model_id: 'aim_2', handle: 'gpt-y', display_name: 'GPT Y', group_role: 'ai', ai_status: 'active', mentionable: true, added_by: 'u_alice', created_at: '2026-11-24T12:00:00Z' },
]

const baseReq: RequirementView = {
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
  cycle: 1,
  max_cycles: 2,
  allowed_actions: ['start_review', 'cancel'],
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

const emptyReview: ReviewStateView = {
  requirement_id: 'req_1',
  state: 'proposed',
  round: 1,
  can_decide: false,
  can_force_close: false,
  can_revise: false,
  rounds: [],
}

describe('ReviewSection', () => {
  afterEach(cleanup)
  beforeEach(() => {
    getReview.mockReset()
    startReview.mockReset()
    submitReviewDecision.mockReset()
    forceCloseReview.mockReset()
    reviseRequirement.mockReset()
  })

  it('offers eligible reviewers (never the assignee) and starts a round', async () => {
    getReview.mockResolvedValue(emptyReview)
    startReview.mockResolvedValue({ ...emptyReview, state: 'in_review' })
    render(<ReviewSection groupId="grp_1" requirement={baseReq} members={members} bump={0} onChanged={() => {}} />)
    expect(await screen.findByText('发起评审')).toBeTruthy()
    const boxes = screen.getAllByRole('checkbox')
    // The assignee is not offered as its own reviewer.
    expect(boxes.some((b) => b.closest('label')?.textContent?.includes('claude-x'))).toBe(false)
    const target = boxes.find((b) => b.closest('label')?.textContent?.includes('gpt-y'))
    expect(target).toBeTruthy()
    fireEvent.click(target!)
    fireEvent.click(screen.getByText('发起评审'))
    await waitFor(() => expect(startReview).toHaveBeenCalledWith('grp_1', 'req_1', ['gm_ai2']))
  })

  it('keeps verdicts blind while the round is open and submits a decision', async () => {
    const open: ReviewStateView = {
      requirement_id: 'req_1',
      state: 'in_review',
      round: 1,
      can_decide: true,
      can_force_close: false,
      can_revise: false,
      rounds: [
        {
          id: 'rnd_1',
          proposal_round: 1,
          status: 'open',
          rule: 'unanimous',
          stage: 'proposal',
          started_by_member_id: 'gm_alice',
          created_at: '2026-11-24T12:00:00Z',
          decisions: [
            { id: 'rdc_1', reviewer_member_id: 'gm_ai2', reviewer_type: 'ai', reviewer_handle: 'gpt-y', status: 'decided' },
            { id: 'rdc_2', reviewer_member_id: 'gm_bob', reviewer_type: 'user', reviewer_handle: 'bob', status: 'pending' },
          ],
        },
      ],
    }
    getReview.mockResolvedValue(open)
    submitReviewDecision.mockResolvedValue({ ...open, state: 'changes_requested' })
    render(
      <ReviewSection
        groupId="grp_1"
        requirement={{ ...baseReq, state: 'in_review', allowed_actions: ['submit_decision', 'cancel'] }}
        members={members}
        bump={0}
        onChanged={() => {}}
      />,
    )
    expect(await screen.findByText('已提交')).toBeTruthy()
    // The decided AI verdict is not in the payload, so nothing renders it.
    expect(screen.queryByText('missing tests')).toBeNull()
    fireEvent.click(screen.getByText('否决'))
    fireEvent.click(screen.getByText('提交评审'))
    await waitFor(() => expect(submitReviewDecision).toHaveBeenCalledWith('grp_1', 'req_1', 'reject', ''))
  })

  it('reveals a closed round and offers revise on changes_requested', async () => {
    const closed: ReviewStateView = {
      requirement_id: 'req_1',
      state: 'changes_requested',
      round: 1,
      can_decide: false,
      can_force_close: false,
      can_revise: true,
      rounds: [
        {
          id: 'rnd_1',
          proposal_round: 1,
          status: 'closed',
          outcome: 'changes_requested',
          rule: 'unanimous',
          stage: 'proposal',
          started_by_member_id: 'gm_alice',
          created_at: '2026-11-24T12:00:00Z',
          closed_at: '2026-11-24T12:05:00Z',
          decisions: [
            {
              id: 'rdc_1',
              reviewer_member_id: 'gm_ai2',
              reviewer_type: 'ai',
              reviewer_handle: 'gpt-y',
              status: 'decided',
              verdict: 'request_changes',
              rationale: 'missing tests',
            },
          ],
        },
      ],
    }
    getReview.mockResolvedValue(closed)
    reviseRequirement.mockResolvedValue({ ...baseReq, state: 'dispatched' })
    render(
      <ReviewSection
        groupId="grp_1"
        requirement={{ ...baseReq, state: 'changes_requested', allowed_actions: ['revise', 'cancel'] }}
        members={members}
        bump={0}
        onChanged={() => {}}
      />,
    )
    expect(await screen.findByText(/missing tests/)).toBeTruthy()
    fireEvent.click(screen.getByText('修订并重新提案'))
    await waitFor(() => expect(reviseRequirement).toHaveBeenCalledWith('grp_1', 'req_1', ''))
  })
})
