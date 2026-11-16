// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const api=vi.hoisted(()=>({listWorkspaces:vi.fn(),createWorkspace:vi.fn(),listWorkspaceShares:vi.fn(),searchWorkspaceMembers:vi.fn(),putWorkspaceShare:vi.fn(),deleteWorkspaceShare:vi.fn()}))
const desktop=vi.hoisted(()=>({desktopBridge:vi.fn()}))
vi.mock('@/lib/api',()=>api)
vi.mock('@/lib/desktop-bridge',()=>desktop)
import Workspaces from './page'

afterEach(()=>{cleanup();vi.clearAllMocks()})

describe('Workspaces page',()=>{
 it('lets an owner grant and revoke a workspace share',async()=>{
  api.listWorkspaces.mockResolvedValue([{id:'ws1',root:'/workspace/repo',is_git:true,dirty:false,access:'editor',is_owner:true}])
  api.listWorkspaceShares.mockResolvedValueOnce([]).mockResolvedValueOnce([{user_id:'u2',subject:'editor@example.test',access:'editor',granted_by:'u1',created_at:'',updated_at:''}]).mockResolvedValueOnce([])
  api.searchWorkspaceMembers.mockResolvedValue([{subject:'editor@example.test'}])
  api.putWorkspaceShare.mockResolvedValue(undefined);api.deleteWorkspaceShare.mockResolvedValue(undefined)
  desktop.desktopBridge.mockReturnValue(undefined)
  render(<Workspaces/>)
  const subject=await screen.findByLabelText('成员账号')
  fireEvent.change(subject,{target:{value:'ed'}})
  fireEvent.click(await screen.findByRole('button',{name:'选择 editor@example.test'}))
  fireEvent.click(screen.getByRole('button',{name:'授予访问'}))
  await waitFor(()=>expect(api.putWorkspaceShare).toHaveBeenCalledWith('ws1','editor@example.test','editor'))
  expect(await screen.findByText(/editor@example.test/)).toBeTruthy()
  fireEvent.click(screen.getByRole('button',{name:'撤销 editor@example.test'}))
  await waitFor(()=>expect(api.deleteWorkspaceShare).toHaveBeenCalledWith('ws1','editor@example.test'))
 })

 it('shows a shared viewer workspace without owner controls',async()=>{
  api.listWorkspaces.mockResolvedValue([{id:'ws2',root:'/workspace/shared',is_git:false,dirty:false,access:'viewer',is_owner:false}])
  api.listWorkspaceShares.mockResolvedValue([{user_id:'owner',subject:'owner@example.test',access:'editor',granted_by:'owner',created_at:'',updated_at:''}])
  desktop.desktopBridge.mockReturnValue(undefined)
  render(<Workspaces/>)
  expect(await screen.findByText('查看者')).toBeTruthy()
  expect(await screen.findByText(/owner@example.test/)).toBeTruthy()
  expect(screen.queryByLabelText('成员账号')).toBeNull()
 })

 it('registers browser workspaces by path relative to the Runner root',async()=>{
  api.listWorkspaces.mockResolvedValue([]);api.createWorkspace.mockResolvedValue({id:'ws3',root:'/workspace/repo',is_git:false,dirty:false,access:'editor',is_owner:true})
  desktop.desktopBridge.mockReturnValue(undefined)
  render(<Workspaces/>)
  fireEvent.change(screen.getByLabelText('Runner 相对路径'),{target:{value:'repo'}})
  fireEvent.click(screen.getByRole('button',{name:'添加工作区'}))
  await waitFor(()=>expect(api.createWorkspace).toHaveBeenCalledWith('repo'))
 })

 it('shows a loading state while workspaces are being fetched',()=>{
  api.listWorkspaces.mockReturnValue(new Promise(()=>{}))
  desktop.desktopBridge.mockReturnValue(undefined)
  render(<Workspaces/>)
  expect(screen.getByText('正在读取工作区…')).toBeTruthy()
 })
})
