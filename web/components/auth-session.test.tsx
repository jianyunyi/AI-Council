// @vitest-environment jsdom
import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'
import {AuthProvider,RequirePermission,can,useAuth} from './auth-session'
import {listUsers} from '@/lib/api'

const identity={subject:'alice',roles:['reader'],permissions:['task:read'],expires_at:null}
function response(data:unknown,status=200){return new Response(JSON.stringify({data,error:status===401?{code:'unauthorized',message:'Session expired'}:null,request_id:'r'}),{status})}
function Controls(){
 const auth=useAuth()
 return <><output aria-label="session">{auth.status}:{auth.identity?.subject??'none'}:{auth.isDesktop?'desktop':'browser'}</output>
  <button onClick={()=>void auth.signIn('alice','secret').catch(()=>{})}>Sign in</button>
  <button onClick={()=>void auth.signOut().catch(()=>{})}>Sign out</button>
  <button onClick={()=>void auth.refresh()}>Refresh</button>
  <button onClick={()=>void listUsers().catch(()=>{})}>Request users</button>
  {auth.error&&<p>{auth.error.message}</p>}</>
}
function Session({permission='task:read',allowDesktop=false}:{permission?:string;allowDesktop?:boolean}){
 return <AuthProvider><Controls/><RequirePermission permission={permission} allowDesktop={allowDesktop}><p>Protected content</p></RequirePermission></AuthProvider>
}

afterEach(()=>{cleanup();vi.restoreAllMocks();vi.useRealTimers();delete window.__AI_COUNCIL_DESKTOP__;delete window.go})

describe('permission matching',()=>{
 it('matches exact permissions and namespace wildcards without granting other namespaces',()=>{
  expect(can(identity,'task:read')).toBe(true)
  expect(can(identity,'task:create')).toBe(false)
  const admin={...identity,permissions:['admin:*']}
  expect(can(admin,'admin:user:write')).toBe(true)
  expect(can(admin,'task:create')).toBe(false)
  expect(can(admin,'administrator:write')).toBe(false)
  expect(can(null,'admin:users:read')).toBe(false)
 })
})

describe('browser session and permission guards',()=>{
 it('withholds protected content during identity loading and shows a login prompt after 401',async()=>{
  let resolve!:(value:Response)=>void
  vi.spyOn(globalThis,'fetch').mockImplementation(()=>new Promise(done=>{resolve=done}))
  render(<Session/> )
  expect(screen.getByLabelText('session').textContent).toBe('loading:none:browser')
  expect(screen.queryByText('Protected content')).toBeNull()
  await act(async()=>resolve(response(null,401)))
  expect(screen.getByLabelText('session').textContent).toBe('anonymous:none:browser')
  expect(screen.getByRole('link',{name:'登录'}).getAttribute('href')).toBe('/login')
 })

 it('logs in, grants the identity permissions and clears identity after logout is confirmed',async()=>{
  let finishLogout!:(value:Response)=>void
  vi.spyOn(globalThis,'fetch').mockResolvedValueOnce(response(null,401)).mockResolvedValueOnce(response(identity)).mockImplementationOnce(()=>new Promise(done=>{finishLogout=done}))
  const storage=vi.spyOn(Storage.prototype,'setItem')
  render(<Session/> )
  await waitFor(()=>expect(screen.getByLabelText('session').textContent).toBe('anonymous:none:browser'))
  fireEvent.click(screen.getByRole('button',{name:'Sign in'}))
  expect(await screen.findByText('Protected content')).toBeTruthy()
  expect(screen.getByLabelText('session').textContent).toBe('authenticated:alice:browser')
  expect(storage).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button',{name:'Sign out'}))
  expect(screen.getByText('Protected content')).toBeTruthy()
  expect(screen.getByLabelText('session').textContent).toBe('authenticated:alice:browser')
  await act(async()=>finishLogout(new Response(null,{status:204})))
  expect(screen.queryByText('Protected content')).toBeNull()
  expect(screen.getByLabelText('session').textContent).toBe('anonymous:none:browser')
 })

 it('keeps the authenticated identity and surfaces an error when logout revocation fails',async()=>{
  vi.spyOn(globalThis,'fetch').mockResolvedValueOnce(response(identity)).mockResolvedValueOnce(new Response('upstream unavailable',{status:502}))
  render(<Session/> )
  await screen.findByText('Protected content')
  fireEvent.click(screen.getByRole('button',{name:'Sign out'}))
  await waitFor(()=>expect(screen.getByText('upstream unavailable').textContent).toBeTruthy())
  expect(screen.getByLabelText('session').textContent).toBe('authenticated:alice:browser')
  expect(screen.getByText('Protected content')).toBeTruthy()
 })

 it('shows a permission alert without mounting protected content',async()=>{
  vi.spyOn(globalThis,'fetch').mockResolvedValue(response(identity))
  render(<Session permission="admin:users:read"/> )
  expect((await screen.findByRole('alert')).textContent).toContain('权限')
  expect(screen.queryByText('Protected content')).toBeNull()
 })

 it('clears stale identity when a business API responds with 401',async()=>{
  vi.spyOn(globalThis,'fetch').mockResolvedValueOnce(response(identity)).mockResolvedValueOnce(response(null,401))
  render(<Session/> )
  await screen.findByText('Protected content')
  fireEvent.click(screen.getByRole('button',{name:'Request users'}))
  await waitFor(()=>expect(screen.getByLabelText('session').textContent).toBe('anonymous:none:browser'))
  expect(screen.queryByText('Protected content')).toBeNull()
 })

 it('shows network failures as a retryable error, then recovers identity',async()=>{
  vi.spyOn(globalThis,'fetch').mockRejectedValueOnce(new Error('Network unavailable')).mockResolvedValueOnce(response(identity))
  render(<Session/> )
  await waitFor(()=>expect(screen.getByLabelText('session').textContent).toBe('error:none:browser'))
  expect(screen.queryByText('Protected content')).toBeNull()
  expect(screen.getByRole('alert').textContent).toContain('Network unavailable')
  fireEvent.click(screen.getByRole('button',{name:'重试'}))
  expect(await screen.findByText('Protected content')).toBeTruthy()
 })

 it('does not restore an identity from a pending refresh after confirmed logout',async()=>{
  let finishRefresh!:(value:Response)=>void
  vi.spyOn(globalThis,'fetch').mockResolvedValueOnce(response(identity)).mockImplementationOnce(()=>new Promise(done=>{finishRefresh=done})).mockResolvedValueOnce(new Response(null,{status:204}))
  render(<Session/> )
  await screen.findByText('Protected content')
  fireEvent.click(screen.getByRole('button',{name:'Refresh'}))
  fireEvent.click(screen.getByRole('button',{name:'Sign out'}))
  await act(async()=>finishRefresh(response(identity)))
  expect(screen.getByLabelText('session').textContent).toBe('anonymous:none:browser')
  expect(screen.queryByText('Protected content')).toBeNull()
 })

 it('expires identity at the server-provided expiry',async()=>{
  vi.useFakeTimers()
  const expires_at=new Date(Date.now()+1000).toISOString()
  vi.spyOn(globalThis,'fetch').mockResolvedValue(response({...identity,expires_at}))
  render(<Session/> )
  await act(async()=>{})
  expect(screen.getByText('Protected content')).toBeTruthy()
  await act(async()=>vi.advanceTimersByTime(1001))
  expect(screen.getByLabelText('session').textContent).toBe('anonymous:none:browser')
  expect(screen.queryByText('Protected content')).toBeNull()
 })

 it('allows explicit desktop business access without inventing an admin identity',async()=>{
  window.__AI_COUNCIL_DESKTOP__={apiBase:'http://127.0.0.1:18080/api/v1',sessionToken:'desktop-token'}
  const fetchMock=vi.spyOn(globalThis,'fetch')
  render(<AuthProvider><Controls/><RequirePermission permission="task:create" allowDesktop><p>Desktop action</p></RequirePermission><RequirePermission permission="admin:users:read"><p>Admin action</p></RequirePermission></AuthProvider>)
  await waitFor(()=>expect(screen.getByLabelText('session').textContent).toBe('anonymous:none:desktop'))
  expect(screen.getByText('Desktop action')).toBeTruthy()
  expect(screen.queryByText('Admin action')).toBeNull()
  expect(fetchMock).not.toHaveBeenCalled()
 })
})
