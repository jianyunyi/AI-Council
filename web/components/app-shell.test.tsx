// @vitest-environment jsdom
import {cleanup,render,screen} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const auth={status:'authenticated',identity:{subject:'admin'},isDesktop:false,can:((permission:string)=>permission==='admin:permissions') as (permission:string)=>boolean}
vi.mock('./auth-session',()=>({AuthProvider:({children}:{children:React.ReactNode})=><>{children}</>,useAuth:()=>auth}))
vi.mock('next/navigation',()=>({usePathname:()=>'/',useRouter:()=>({push:vi.fn()})}))
vi.mock('@/lib/api',()=>({listWorkspaces:vi.fn(async()=>[]),getTask:vi.fn(async()=>{throw new Error('none')}),onUnauthorized:()=>()=>{}}))
import {AppShell} from './app-shell'

afterEach(()=>{cleanup();auth.can=permission=>permission==='admin:permissions'})

describe('AppShell',()=>{
 it('names the navigation as the main navigation and preserves its destinations',()=>{
  render(<AppShell><p>content</p></AppShell>)
  expect(screen.getByRole('navigation',{name:'主导航'})).toBeTruthy()
  for(const [name,href] of [['指挥中心','/'],['任务','/tasks'],['新建任务','/tasks/new'],['制品','/artifacts'],['工作区','/workspaces'],['Provider','/providers'],['设置','/settings'],['admin','/account']]){
   expect(screen.getByRole('link',{name}).getAttribute('href')).toBe(href)
  }
 })
 it('does not expose an empty management destination for permission-catalog-only identities',()=>{
  render(<AppShell><p>content</p></AppShell>)
  expect(screen.queryByRole('link',{name:'用户与权限'})).toBeNull()
  auth.can=permission=>permission==='admin:roles'
  cleanup();render(<AppShell><p>content</p></AppShell>)
  expect(screen.getByRole('link',{name:'用户与权限'})).toBeTruthy()
 })
})
