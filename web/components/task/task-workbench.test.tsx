// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
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
  it('requires all four checks and a 1.2s hold to approve, then executes as a separate step',async()=>{
    api.approveTask.mockResolvedValue({id:'task-1',state:'AWAITING_APPROVAL',requirement:'do it',plan_version:1,approval_hash:'h',approved:true,approval_expires_at:Math.floor(Date.now()/1000)+600,plan:{version:1,acceptance:[],recovery:[],commands:[],patches:[]}})
    api.executeTask.mockResolvedValue({id:'task-1',state:'EXECUTING',requirement:'do it'})
    render(<TaskWorkbench id="task-1"/>)
    const hold=await screen.findByRole('button',{name:'按住 1.2 秒批准'})
    expect(hold.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(screen.getByText('被签名的计划原文（批准对象）'))
    fireEvent.click(screen.getByRole('button',{name:'标记已阅'}))
    for(const name of [/计划原文/,/diff/,/执行命令并修改工作区文件/,/验收标准与恢复策略/]){
      fireEvent.click(screen.getByRole('checkbox',{name}))
    }
    expect(hold.getAttribute('aria-disabled')).toBe('false')
    fireEvent.pointerDown(hold)
    await waitFor(()=>expect(api.approveTask).toHaveBeenCalledWith('task-1',1,'h'),{timeout:3000})
    expect(api.executeTask).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole('button',{name:'执行计划'}))
    await waitFor(()=>expect(api.executeTask).toHaveBeenCalledWith('task-1'))
  })
})
