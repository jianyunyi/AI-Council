// @vitest-environment jsdom
import {cleanup,render,screen} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const auth={status:'authenticated',identity:{subject:'admin'},isDesktop:false,can:((permission:string)=>permission==='admin:permissions') as (permission:string)=>boolean}
vi.mock('./auth-session',()=>({AuthProvider:({children}:{children:React.ReactNode})=><>{children}</>,useAuth:()=>auth}))
import {AppShell} from './app-shell'

afterEach(()=>{cleanup();auth.can=permission=>permission==='admin:permissions'})

describe('AppShell',()=>{
 it('names the navigation as the console index and preserves its destinations',()=>{
  render(<AppShell><p>content</p></AppShell>)
  expect(screen.getByRole('navigation',{name:'控制台索引'})).toBeTruthy()
  for(const [name,href] of [['任务','/tasks/new'],['模型','/providers'],['工作区','/workspaces'],['admin','/account']]){
   expect(screen.getByRole('link',{name}).getAttribute('href')).toBe(href)
  }
 })
 it('does not expose an empty management destination for permission-catalog-only identities',()=>{
  render(<AppShell><p>content</p></AppShell>)
  expect(screen.queryByRole('link',{name:'用户管理'})).toBeNull()
  auth.can=permission=>permission==='admin:roles'
  cleanup();render(<AppShell><p>content</p></AppShell>)
  expect(screen.getByRole('link',{name:'用户管理'})).toBeTruthy()
 })
})
