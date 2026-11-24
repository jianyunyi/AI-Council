// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'

const api=vi.hoisted(()=>({
  createTask:vi.fn(),
  createWorkspace:vi.fn(),
  listWorkspaces:vi.fn(),
  startTask:vi.fn(),
  getTask:vi.fn(async()=>{throw new Error('not found')}),
  listTasks:vi.fn(async()=>({items:[]})),
  onUnauthorized:()=>()=>{},
}))
const push=vi.hoisted(()=>vi.fn())
const desktop=vi.hoisted(()=>({desktopBridge:vi.fn()}))

vi.mock('@/lib/api',()=>api)
vi.mock('next/navigation',()=>({useRouter:()=>({push}),usePathname:()=>'/'}))
vi.mock('@/lib/desktop',()=>({getDesktopRuntime:()=>undefined}))
vi.mock('@/lib/desktop-bridge',()=>desktop)
vi.mock('@/components/auth-session',()=>({
  AuthProvider:({children}:{children:React.ReactNode})=><>{children}</>,
  useAuth:()=>({status:'authenticated',identity:{subject:'admin'},isDesktop:false,can:()=>false}),
}))

import NewTask from './page'
import {AppShell} from '@/components/app-shell'

describe('NewTask',()=>{
  afterEach(()=>cleanup())

  beforeEach(()=>{
    window.localStorage.clear()
    api.listWorkspaces.mockResolvedValue([{id:'workspace-1',root:'C:/Projects/demo',is_git:true,dirty:false}])
    api.createTask.mockResolvedValue({id:'task-1',state:'DRAFT'})
    api.startTask.mockResolvedValue({id:'task-1',state:'PLANNING'})
    desktop.desktopBridge.mockReturnValue(undefined)
    push.mockReset()
  })

  it('groups the task creation form in the semantic operation workflow hierarchy',async()=>{
    const {container}=render(<AppShell><NewTask /></AppShell>)
    const page=container.querySelector('.operation-page')
    expect(page).toBeTruthy()
    expect(container.querySelector('.app-shell__main > main')).toBeNull()
    expect(page?.querySelector('.operation-band')).toBeTruthy()
    expect(page?.querySelector('.operation-plane')).toBeTruthy()
    expect(screen.getByRole('heading',{level:1,name:'新建任务'})).toBeTruthy()
  })

  it('creates the task in the selected registered workspace',async()=>{
    render(<NewTask />)
    const workspace=await screen.findByLabelText('工作区')
    fireEvent.change(workspace,{target:{value:'workspace-1'}})
    fireEvent.change(screen.getByLabelText('需求'),{target:{value:'交付工作区流程'}})
    fireEvent.change(screen.getByLabelText('新增验收标准'),{target:{value:'测试通过'}})
    fireEvent.click(screen.getByRole('button',{name:'添加'}))
    fireEvent.submit(screen.getByRole('button',{name:'开始协商'}).closest('form')!)
    await waitFor(()=>expect(api.createTask).toHaveBeenCalledWith({workspaceId:'workspace-1',requirement:'交付工作区流程',acceptance:['测试通过']}))
    expect(push).toHaveBeenCalledWith('/tasks/task-1')
  })

  it('registers the running desktop workspace before task creation',async()=>{
    api.listWorkspaces.mockResolvedValue([])
    api.createWorkspace.mockResolvedValue({id:'workspace-2',root:'C:/Projects/desktop',is_git:true,dirty:false})
    desktop.desktopBridge.mockReturnValue({status:vi.fn().mockResolvedValue({state:'ready',workspace:'C:/Projects/desktop'})})
    render(<NewTask />)
    expect(await screen.findByRole('option',{name:/C:\/Projects\/desktop/})).toBeTruthy()
    expect(api.createWorkspace).toHaveBeenCalledWith('.')
  })
})
