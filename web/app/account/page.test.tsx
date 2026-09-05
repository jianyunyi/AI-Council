// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const signOut=vi.fn()
const auth={status:'authenticated',identity:authIdentity() as ReturnType<typeof authIdentity>|null,error:null as Error|null,signOut,refresh:vi.fn()}
function authIdentity(){return {subject:'admin',roles:['admin'],permissions:['admin:*','task:read'],expires_at:'2030-01-01T00:00:00Z'}}
vi.mock('@/components/auth-session',()=>({useAuth:()=>auth}))
import AccountPage from './page'

afterEach(()=>{cleanup();vi.clearAllMocks();auth.status='authenticated';auth.identity=authIdentity();auth.error=null})

describe('AccountPage',()=>{
 it('shows the safe identity projection and logs out explicitly',async()=>{
  signOut.mockResolvedValue(undefined)
  render(<AccountPage/> )
  expect(screen.getByRole('heading',{name:'账户与权限'})).toBeTruthy()
  expect(screen.getAllByText('admin')).toHaveLength(2)
  expect(screen.getByText('admin:*')).toBeTruthy()
  fireEvent.click(screen.getByRole('button',{name:'退出登录'}))
  await waitFor(()=>expect(signOut).toHaveBeenCalledOnce())
 })

 it('distinguishes session loading and transport errors from anonymous state',()=>{
  auth.status='loading';auth.identity=null
  const {rerender}=render(<AccountPage/> )
  expect(screen.getByRole('status').textContent).toContain('正在读取')
  auth.status='error';auth.error=new Error('Network unavailable')
  rerender(<AccountPage/> )
  expect(screen.getByRole('alert').textContent).toContain('Network unavailable')
  expect(screen.queryByText('尚未登录')).toBeNull()
 })
})
