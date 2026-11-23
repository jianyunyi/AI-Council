// @vitest-environment jsdom
import {cleanup,render,screen} from '@testing-library/react'
import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'

const api=vi.hoisted(()=>({getTask:vi.fn(),approveTask:vi.fn(),executeTask:vi.fn()}))
vi.mock('@/lib/api',()=>api)
vi.mock('@/lib/events',()=>({subscribe:()=>()=>{}}))

import {TaskWorkbench} from './task-workbench'

describe('TaskWorkbench plan details',()=>{
  afterEach(()=>cleanup())
  beforeEach(()=>{
    api.getTask.mockResolvedValue({
      id:'task-1',state:'AWAITING_APPROVAL',requirement:'do it',plan_version:1,approval_hash:'h',
      plan:{version:1,acceptance:[],recovery:[],
        commands:[{executable:'go',args:['test','./...'],work_dir:'svc',timeout_seconds:30}],
        patches:[{path:'main.go',unified_diff:'@@ -1 +1 @@\n-a\n+b\n'}]},
    })
  })
  it('shows work_dir, timeout and the diff before approval',async()=>{
    render(<TaskWorkbench id="task-1"/>)
    expect(await screen.findByText(/工作目录：svc/)).toBeTruthy()
    expect(screen.getByText(/超时：30s/)).toBeTruthy()
    expect(screen.getByText(/-a/)).toBeTruthy() // diff body rendered
  })
})
