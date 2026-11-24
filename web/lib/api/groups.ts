// Group chat API (Part B P5). Transport rides lib/api's request() — same
// envelope, apiBase and desktop Authorization handling as every other call
// (WIRING F1/F2 verified against web/lib/api.ts). Quick's original draft
// assumed a fetch-based transport and a GET /users endpoint; both are
// adapted here (see listUsers).
import { ApiError, listUsers as listAdminUsers, request } from '../api'
import { apiBase } from '../desktop'
import type { ExecutionPlan } from '../types'

// ---------- types (mirror internal/app/group views) ----------

export type GroupStatus = 'active' | 'archived'
export type MemberType = 'user' | 'ai_model'
export type GroupRole = 'owner' | 'admin' | 'operator' | 'ai'
export type MentionResolution = 'resolved' | 'not_member' | 'unknown' | 'ignored_ai_author'

export interface GroupPolicy {
  proposal_review_rule: 'all' | 'quorum' | 'any'
  proposal_quorum?: number
  min_reviewers: number
  max_reviewers: number
  allow_ai_judges: boolean
  review_deadline_minutes: number
  context_token_budget: number
  require_human_approval: true // D1: always true in v1
}

export interface MemberView {
  id: string
  group_id: string
  member_type: MemberType
  user_id?: string
  ai_model_id?: string
  handle: string
  display_name: string
  group_role: GroupRole
  params_override?: Record<string, unknown>
  ai_status?: string
  mentionable: boolean
  added_by: string
  created_at: string
  removed_at?: string
}

export interface GroupSummary {
  id: string
  name: string
  description?: string
  status: GroupStatus
  shared_workspace_id: string
  created_by: string
  version: number
  last_event_seq: number
  last_message_seq: number
  created_at: string
  updated_at: string
  my_role?: GroupRole
  can_manage: boolean
  member_count: number
  ai_member_count: number
}

export interface GroupDetail extends GroupSummary {
  policy: GroupPolicy
  members: MemberView[]
}

export interface MentionView {
  raw_token: string
  handle: string
  target_member_id?: string
  resolution: MentionResolution
  intent: 'reference' | 'call'
  start_offset: number
  end_offset: number
}

export interface MessageView {
  id: string
  group_id: string
  seq: number
  author_type: 'user' | 'ai' | 'system'
  author?: MemberView
  kind: 'text' | 'requirement' | 'proposal' | 'dispatch_card' | 'result_card' | 'system'
  body_md: string
  reply_to_message_id?: string
  requirement_id?: string
  artifact_id?: string
  status: 'streaming' | 'final' | 'failed'
  edited_at?: string
  created_at: string
  mentions: MentionView[]
}

export interface MentionCandidate {
  handle: string
  display_name: string
  member_type: MemberType
  member_id?: string
  ai_model_id?: string
  is_member: boolean
  status?: string
}

export interface GroupEvent {
  seq: number
  type: string
  ts: string
  data: unknown
}

export interface UserSummary {
  id: string
  username: string
  display_name: string
}

export class GroupApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

function toGroupError(err: unknown): never {
  if (err instanceof ApiError) {
    throw new GroupApiError(err.status, err.code ?? 'http_error', err.message)
  }
  throw err
}

async function call<T>(p: Promise<T>): Promise<T> {
  try {
    return await p
  } catch (err) {
    toGroupError(err)
  }
}

// ---------- groups ----------

export interface ListGroupsParams {
  status?: GroupStatus
  q?: string
  cursor?: string
  limit?: number
}

export function listGroups(params: ListGroupsParams = {}): Promise<{ items: GroupSummary[]; next_cursor: string | null }> {
  const qs = new URLSearchParams()
  if (params.status) qs.set('status', params.status)
  if (params.q) qs.set('q', params.q)
  if (params.cursor) qs.set('cursor', params.cursor)
  if (params.limit) qs.set('limit', String(params.limit))
  const suffix = qs.toString()
  return call(request(`/groups${suffix ? `?${suffix}` : ''}`))
}

export interface CreateGroupInput {
  name: string
  description?: string
  policy?: Partial<GroupPolicy>
  members?: AddMemberInput[]
}

export function createGroup(input: CreateGroupInput): Promise<GroupDetail> {
  return call(request('/groups', { method: 'POST', body: JSON.stringify(input) }))
}

export function getGroup(id: string): Promise<GroupDetail> {
  return call(request(`/groups/${encodeURIComponent(id)}`))
}

export interface UpdateGroupInput {
  name?: string
  description?: string
  policy?: Partial<GroupPolicy>
  expected_version: number
}

export function updateGroup(id: string, input: UpdateGroupInput): Promise<GroupDetail> {
  return call(request(`/groups/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) }))
}

export function archiveGroup(id: string): Promise<GroupDetail> {
  return call(request(`/groups/${encodeURIComponent(id)}/archive`, { method: 'POST', body: '{}' }))
}

// ---------- members ----------

export interface AddMemberInput {
  member_type: MemberType
  user_id?: string
  ai_model_id?: string
  group_role?: GroupRole
  params_override?: Record<string, unknown>
}

export function addMember(groupId: string, input: AddMemberInput): Promise<MemberView> {
  return call(request(`/groups/${encodeURIComponent(groupId)}/members`, { method: 'POST', body: JSON.stringify(input) }))
}

export interface UpdateMemberInput {
  group_role?: GroupRole
  params_override?: Record<string, unknown>
}

export function updateMember(groupId: string, memberId: string, input: UpdateMemberInput): Promise<MemberView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(memberId)}`, {
      method: 'PATCH',
      body: JSON.stringify(input),
    }),
  )
}

export function removeMember(groupId: string, memberId: string): Promise<void> {
  return call(request(`/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(memberId)}`, { method: 'DELETE' }))
}

// ---------- messages ----------

export interface ListMessagesParams {
  after_seq?: number
  before_seq?: number
  requirement_id?: string
  limit?: number
}

export function listMessages(groupId: string, params: ListMessagesParams = {}): Promise<{ items: MessageView[]; has_more: boolean }> {
  const qs = new URLSearchParams()
  if (params.after_seq) qs.set('after_seq', String(params.after_seq))
  if (params.before_seq) qs.set('before_seq', String(params.before_seq))
  if (params.requirement_id) qs.set('requirement_id', params.requirement_id)
  if (params.limit) qs.set('limit', String(params.limit))
  const suffix = qs.toString()
  return call(request(`/groups/${encodeURIComponent(groupId)}/messages${suffix ? `?${suffix}` : ''}`))
}

export interface PostMessageInput {
  body_md: string
  client_msg_id: string
  reply_to_message_id?: string
  requirement_id?: string
  intent?: 'chat' | 'dispatch' | 'review' | 'result_review'
}

export function postMessage(groupId: string, input: PostMessageInput): Promise<{ message: MessageView; duplicate: boolean }> {
  return call(request(`/groups/${encodeURIComponent(groupId)}/messages`, { method: 'POST', body: JSON.stringify(input) }))
}

export function editMessage(groupId: string, messageId: string, bodyMd: string): Promise<MessageView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/messages/${encodeURIComponent(messageId)}`, {
      method: 'PATCH',
      body: JSON.stringify({ body_md: bodyMd }),
    }),
  )
}

export function resolveMentions(groupId: string, q: string): Promise<{ items: MentionCandidate[] }> {
  return call(request(`/groups/${encodeURIComponent(groupId)}/mentions/resolve?q=${encodeURIComponent(q)}`))
}

// ---------- requirements (Part B P6) ----------

export type RequirementState =
  | 'dispatched'
  | 'proposing'
  | 'proposed'
  | 'proposal_failed'
  | 'failed'
  | 'cancelled'
  | 'in_review'
  | 'changes_requested'
  | 'review_passed'
  | 'rejected'
  | 'awaiting_approval'
  | 'approved'
  | 'executing'
  | 'executed'
  | 'execution_failed'

export interface ProposalView {
  id: string
  attempt: number
  plan: ExecutionPlan
  summary: string
  created_at: string
}

export interface RequirementJobView {
  state: string
  attempts: number
}

/** Part B P8: the red-team check over the current proposal attempt. */
export interface RedTeamView {
  check_id: string
  status: 'queued' | 'running' | 'clear' | 'flagged' | 'error' | 'superseded'
  independence: string
  seat_model?: string
  plan_hash: string
  summary?: string
  finished_at?: string
}

/** Part B P8: the push-down of the current proposal attempt. */
export interface PushdownView {
  id: string
  status: string
  task_id?: string
  workspace_id: string
  plan_hash: string
  approved_by: string
  approved_at: string
  last_phase?: string
  last_error?: string
  failure_category?: string
  finished_at?: string
}

export interface RequirementView {
  id: string
  group_id: string
  anchor_message_id: string
  requested_by: string
  requested_by_member_id: string
  assignee_member_id: string
  assignee_ai_model_id: string
  assignee_handle: string
  title: string
  body_md: string
  acceptance?: string[]
  state: RequirementState
  failure_code?: string
  failure_detail?: string
  version: number
  proposal?: ProposalView
  job?: RequirementJobView
  /** Per-actor actions computed by the backend (P7). */
  allowed_actions?: string[]
  /** Part B P8: red-team check + push-down for the current attempt. */
  redteam?: RedTeamView
  pushdown?: PushdownView
  /** Part B P8 (D1): whether the requesting actor may approve. */
  can_approve?: boolean
  approve_blocked_reason?: string
  created_at: string
  updated_at: string
  state_changed_at: string
  proposed_at?: string
  terminal_at?: string
}

export interface ListRequirementsParams {
  state?: RequirementState
  assignee_member_id?: string
  cursor?: string
  limit?: number
}

export function listRequirements(
  groupId: string,
  params: ListRequirementsParams = {},
): Promise<{ items: RequirementView[]; next_cursor?: string }> {
  const qs = new URLSearchParams()
  if (params.state) qs.set('state', params.state)
  if (params.assignee_member_id) qs.set('assignee_member_id', params.assignee_member_id)
  if (params.cursor) qs.set('cursor', params.cursor)
  if (params.limit) qs.set('limit', String(params.limit))
  const suffix = qs.toString()
  return call(request(`/groups/${encodeURIComponent(groupId)}/requirements${suffix ? `?${suffix}` : ''}`))
}

export function getRequirement(groupId: string, requirementId: string): Promise<RequirementView> {
  return call(request(`/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}`))
}

export function retryRequirement(groupId: string, requirementId: string): Promise<RequirementView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}/retry`, {
      method: 'POST',
      body: '{}',
    }),
  )
}

export function cancelRequirement(groupId: string, requirementId: string, reason?: string): Promise<RequirementView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}/cancel`, {
      method: 'POST',
      body: JSON.stringify({ reason: reason ?? '' }),
    }),
  )
}

// ---------- reviews (Part B P7) ----------

export type ReviewVerdict = 'approve' | 'request_changes' | 'reject'
export type ReviewOutcome = 'passed' | 'changes_requested' | 'rejected' | 'inconclusive' | 'superseded'

export interface ReviewDecisionView {
  id: string
  reviewer_member_id: string
  reviewer_type: string
  reviewer_handle?: string
  status: 'pending' | 'running' | 'decided' | 'error' | 'withdrawn'
  /** Present only after the round closes (verdicts are blind while open). */
  verdict?: ReviewVerdict
  rationale?: string
  error?: string
  decided_at?: string
}

export interface ReviewRoundView {
  id: string
  proposal_round: number
  status: 'open' | 'closed'
  outcome?: ReviewOutcome
  rule: string
  quorum?: number
  started_by_member_id: string
  deadline_at?: string
  revision_note?: string
  created_at: string
  closed_at?: string
  decisions: ReviewDecisionView[]
}

export interface ReviewStateView {
  requirement_id: string
  state: RequirementState
  round: number
  can_decide: boolean
  can_force_close: boolean
  can_revise: boolean
  rounds: ReviewRoundView[]
}

function reviewBase(groupId: string, requirementId: string): string {
  return `/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}/review`
}

export function startReview(groupId: string, requirementId: string, reviewerMemberIds: string[]): Promise<ReviewStateView> {
  return call(
    request(reviewBase(groupId, requirementId), {
      method: 'POST',
      body: JSON.stringify({ reviewer_member_ids: reviewerMemberIds }),
    }),
  )
}

export function getReview(groupId: string, requirementId: string): Promise<ReviewStateView> {
  return call(request(reviewBase(groupId, requirementId)))
}

export function submitReviewDecision(
  groupId: string,
  requirementId: string,
  verdict: ReviewVerdict,
  rationale?: string,
): Promise<ReviewStateView> {
  return call(
    request(`${reviewBase(groupId, requirementId)}/decision`, {
      method: 'POST',
      body: JSON.stringify({ verdict, rationale: rationale ?? '' }),
    }),
  )
}

export function forceCloseReview(groupId: string, requirementId: string): Promise<ReviewStateView> {
  return call(request(`${reviewBase(groupId, requirementId)}/force-close`, { method: 'POST', body: '{}' }))
}

export function reviseRequirement(groupId: string, requirementId: string, note?: string): Promise<RequirementView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}/revise`, {
      method: 'POST',
      body: JSON.stringify({ note: note ?? '' }),
    }),
  )
}

// ---------- approval + push-down (Part B P8) ----------

export function approveRequirement(
  groupId: string,
  requirementId: string,
  planHash: string,
  expectedVersion: number,
): Promise<RequirementView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}/approve`, {
      method: 'POST',
      body: JSON.stringify({ plan_hash: planHash, expected_version: expectedVersion }),
    }),
  )
}

export function retryPushdown(groupId: string, requirementId: string): Promise<RequirementView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}/pushdown/retry`, {
      method: 'POST',
      body: '{}',
    }),
  )
}

export function retryRedTeam(groupId: string, requirementId: string): Promise<RequirementView> {
  return call(
    request(`/groups/${encodeURIComponent(groupId)}/requirements/${encodeURIComponent(requirementId)}/redteam/retry`, {
      method: 'POST',
      body: '{}',
    }),
  )
}

// ---------- directories (for pickers) ----------

/**
 * There is no GET /api/v1/users (WIRING F3): the only user directory is the
 * admin list (requires admin:users), whose rows carry the account subject
 * but no separate id — the subject IS the user identifier the backend group
 * directory resolves. Returns null on any failure so callers fall back to a
 * free-text field.
 */
export async function listUsers(): Promise<UserSummary[] | null> {
  try {
    const users = await listAdminUsers()
    return users.map((u) => ({ id: u.subject, username: u.subject, display_name: u.subject }))
  } catch {
    return null
  }
}

/** SSE endpoint URL (same base the REST calls use). */
export function eventsUrl(groupId: string, afterSeq = 0): string {
  return `${apiBase()}/groups/${encodeURIComponent(groupId)}/events?after_seq=${afterSeq}`
}
