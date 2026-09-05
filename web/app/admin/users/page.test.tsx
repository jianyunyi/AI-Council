// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const auth={status:'authenticated',identity:{subject:'admin',roles:['admin'],permissions:['admin:*'],expires_at:null},error:null as Error|null,refresh:vi.fn(),can:(permission:string)=>permission.startsWith('admin:')}
vi.mock('@/components/auth-session',()=>({useAuth:()=>auth,RequirePermission:({children}:{children:React.ReactNode})=><>{children}</>}))
const api=vi.hoisted(()=>({
 ApiError:class ApiError extends Error{constructor(public status:number,public code:string,message:string){super(message)}},
 listUsers:vi.fn(),listRoles:vi.fn(),listPermissions:vi.fn(),createUser:vi.fn(),createRole:vi.fn(),updateUser:vi.fn(),updateRole:vi.fn(),
}))
vi.mock('@/lib/api',()=>api)
import UserManagementPage from './page'

afterEach(()=>{cleanup();vi.clearAllMocks();auth.status='authenticated';auth.error=null;auth.can=permission=>permission.startsWith('admin:')})

describe('UserManagementPage',()=>{
 it('renders meaningful loading, data, and empty permission states',async()=>{
  api.listUsers.mockResolvedValue([{subject:'admin',disabled:false,roles:['admin']}])
  api.listRoles.mockResolvedValue([{name:'admin',permissions:['admin:*']}])
  api.listPermissions.mockResolvedValue([])
  render(<UserManagementPage/> )
  expect(screen.getByRole('status').textContent).toContain('正在加载')
  expect(await screen.findByRole('row',{name:/admin/})).toBeTruthy()
  expect(screen.getByText('暂无可分配权限')).toBeTruthy()
 })

 it('creates a role and user, then clears the password',async()=>{
  api.listUsers.mockResolvedValue([])
  api.listRoles.mockResolvedValue([])
  api.listPermissions.mockResolvedValue([{name:'task:read'},{name:'workspace:read'}])
  api.createRole.mockResolvedValue({name:'reader',permissions:['task:read']})
  api.createUser.mockResolvedValue({subject:'operator',disabled:false,roles:['reader']})
  render(<UserManagementPage/> )
  await screen.findByText('暂无用户')
  fireEvent.change(screen.getByLabelText('角色名称'),{target:{value:'reader'}})
  fireEvent.click(screen.getByRole('checkbox',{name:'task:read'}))
  fireEvent.click(screen.getByRole('button',{name:'创建角色'}))
  await waitFor(()=>expect(api.createRole).toHaveBeenCalledWith({name:'reader',permissions:['task:read']}))
  fireEvent.change(screen.getByLabelText('账号'),{target:{value:'operator'}})
  fireEvent.change(screen.getByLabelText('初始密码'),{target:{value:'safe-passphrase'}})
  fireEvent.click(screen.getByRole('checkbox',{name:'分配初始角色 reader'}))
  fireEvent.click(screen.getByRole('button',{name:'创建用户'}))
  await waitFor(()=>expect(api.createUser).toHaveBeenCalledWith({subject:'operator',password:'safe-passphrase',roles:['reader']}))
  expect((screen.getByLabelText('初始密码') as HTMLInputElement).value).toBe('')
 })

 it('withholds administration data while session state is unresolved or failed',()=>{
  auth.status='loading'
  const {rerender}=render(<UserManagementPage/> )
  expect(screen.getByRole('status').textContent).toContain('正在验证')
  expect(api.listUsers).not.toHaveBeenCalled()
  auth.status='error';auth.error=new Error('Network unavailable')
  rerender(<UserManagementPage/> )
  expect(screen.getByRole('alert').textContent).toContain('Network unavailable')
  expect(screen.queryByText('没有管理用户或角色的权限')).toBeNull()
 })

 it('clears cached administration data and refreshes identity after a forbidden reload',async()=>{
  api.listUsers.mockResolvedValueOnce([{subject:'admin',disabled:false,roles:['admin']}]).mockRejectedValueOnce(new api.ApiError(403,'forbidden','access revoked'))
  api.listRoles.mockResolvedValueOnce([]).mockResolvedValueOnce([])
  api.listPermissions.mockResolvedValueOnce([]).mockResolvedValueOnce([])
  render(<UserManagementPage/> )
  await screen.findByRole('row',{name:/admin/})
  fireEvent.click(screen.getByRole('button',{name:'刷新数据'}))
  await waitFor(()=>expect(auth.refresh).toHaveBeenCalledOnce())
  expect(screen.queryByRole('row',{name:/admin/})).toBeNull()
  expect(screen.getByRole('alert').textContent).toContain('access revoked')
 })

 it('does not mount management controls without administrative permissions',()=>{
  auth.can=()=>false
  render(<UserManagementPage/> )
  expect(screen.getByRole('alert').textContent).toContain('没有管理用户或角色的权限')
  expect(screen.queryByRole('button',{name:'创建用户'})).toBeNull()
  auth.can=permission=>permission.startsWith('admin:')
 })
})
