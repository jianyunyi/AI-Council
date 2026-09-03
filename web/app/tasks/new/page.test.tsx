// @vitest-environment jsdom
import {fireEvent,render,screen,waitFor} from '@testing-library/react'
import {beforeEach,describe,expect,it,vi} from 'vitest'

const api=vi.hoisted(()=>({
  createTask:vi.fn(),
  createWorkspace:vi.fn(),
  listWorkspaces:vi.fn(),
  startTask:vi.fn(),
}))
const push=vi.hoisted(()=>vi.fn())
const desktop=vi.hoisted(()=>({desktopBridge:vi.fn()}))

vi.mock('@/lib/api',()=>api)
vi.mock('next/navigation',()=>({useRouter:()=>({push})}))
vi.mock('@/lib/desktop',()=>({getDesktopRuntime:()=>undefined}))
vi.mock('@/lib/desktop-bridge',()=>desktop)

import NewTask from './page'

describe('NewTask',()=>{
  beforeEach(()=>{
    api.listWorkspaces.mockResolvedValue([{id:'workspace-1',root:'C:/Projects/demo',is_git:true,dirty:false}])
    api.createTask.mockResolvedValue({id:'task-1',state:'DRAFT'})
    api.startTask.mockResolvedValue({id:'task-1',state:'PLANNING'})
    desktop.desktopBridge.mockReturnValue(undefined)
    push.mockReset()
  })

  it('creates the task in the selected registered workspace',async()=>{
    render(<NewTask />)
    const workspace=await screen.findByLabelText('工作区')
    fireEvent.change(workspace,{target:{value:'workspace-1'}})
    fireEvent.change(screen.getByLabelText('需求'),{target:{value:'交付工作区流程'}})
    fireEvent.change(screen.getByLabelText('验收标准'),{target:{value:'测试通过'}})
    fireEvent.submit(screen.getByRole('button',{name:'开始协作分析'}).closest('form')!)
    await waitFor(()=>expect(api.createTask).toHaveBeenCalledWith({workspaceId:'workspace-1',requirement:'交付工作区流程',acceptance:['测试通过']}))
    expect(push).toHaveBeenCalledWith('/tasks/task-1')
  })

  it('registers the running desktop workspace before task creation',async()=>{
    api.listWorkspaces.mockResolvedValue([])
    api.createWorkspace.mockResolvedValue({id:'workspace-2',root:'C:/Projects/desktop',is_git:true,dirty:false})
    desktop.desktopBridge.mockReturnValue({status:vi.fn().mockResolvedValue({state:'ready',workspace:'C:/Projects/desktop'})})
    render(<NewTask />)
    expect(await screen.findByRole('option',{name:/C:\/Projects\/desktop/})).toBeTruthy()
    expect(api.createWorkspace).toHaveBeenCalledWith('C:/Projects/desktop')
  })
})
