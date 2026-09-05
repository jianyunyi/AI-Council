import {afterEach,describe,expect,it,vi} from 'vitest'
import {approveTask,createTask,createWorkspace,executeTask,listWorkspaces,startTask} from './api'
import {ApiError,request,login,me,logout,listUsers,createUser,updateUser,listRoles,createRole,updateRole,listPermissions} from './api'

describe('task lifecycle API',()=>{
 afterEach(()=>{vi.restoreAllMocks()})
 it('sends the plan approval hash and execution requests',async()=>{
  const fetchMock=vi.spyOn(globalThis,'fetch').mockImplementation(async()=>new Response(JSON.stringify({data:{id:'t-1',state:'SUCCEEDED'},request_id:'r'}),{status:200,headers:{'content-type':'application/json'}}))
  await startTask('t-1'); await approveTask('t-1',3,'hash-3'); await executeTask('t-1')
  expect(fetchMock).toHaveBeenNthCalledWith(1,expect.stringContaining('/tasks/t-1/start'),expect.objectContaining({method:'POST'}))
  expect(fetchMock).toHaveBeenNthCalledWith(2,expect.stringContaining('/tasks/t-1/approve'),expect.objectContaining({body:'{"plan_version":3,"approval_hash":"hash-3"}'}))
  expect(fetchMock).toHaveBeenNthCalledWith(3,expect.stringContaining('/tasks/t-1/execute'),expect.objectContaining({method:'POST'}))
 })
 it('serializes registered workspace requests using the REST field names',async()=>{
  const fetchMock=vi.spyOn(globalThis,'fetch').mockImplementation(async()=>new Response(JSON.stringify({data:{id:'workspace-1',root:'C:/Projects/demo',is_git:true,dirty:false},request_id:'r'}),{status:201,headers:{'content-type':'application/json'}}))
  await createWorkspace('C:/Projects/demo')
  await listWorkspaces()
  await createTask({workspaceId:'workspace-1',requirement:'ship',acceptance:['tests pass']})
  expect(fetchMock).toHaveBeenNthCalledWith(1,expect.stringContaining('/workspaces'),expect.objectContaining({method:'POST',body:'{"root":"C:/Projects/demo"}'}))
  expect(fetchMock).toHaveBeenNthCalledWith(2,expect.stringContaining('/workspaces'),expect.any(Object))
  expect(fetchMock.mock.calls[1][1]?.method).toBeUndefined()
  expect(fetchMock).toHaveBeenNthCalledWith(3,expect.stringContaining('/tasks'),expect.objectContaining({method:'POST',body:'{"workspace_id":"workspace-1","requirement":"ship","acceptance":["tests pass"]}'}))
 })
})

describe('browser authentication and management API',()=>{
 afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals();vi.unstubAllEnvs()})
 function respond(data:unknown,status=200){return new Response(JSON.stringify({data,error:null,request_id:'r'}),{status})}

 it('sends same-origin cookies and merges custom headers with desktop bearer authorization',async()=>{
  vi.stubGlobal('window',{__AI_COUNCIL_DESKTOP__:{apiBase:'http://127.0.0.1:19000/api/v1',sessionToken:'desktop-token'}})
  const fetchMock=vi.spyOn(globalThis,'fetch').mockResolvedValue(respond({}))
  await request('/tasks',{headers:new Headers({'x-request-id':'custom'})})
  const init=fetchMock.mock.calls[0][1]!
  expect(init.credentials).toBe('same-origin')
  const headers=new Headers(init.headers)
  expect(headers.get('authorization')).toBe('Bearer desktop-token')
  expect(headers.get('content-type')).toBe('application/json')
  expect(headers.get('x-request-id')).toBe('custom')
 })

 it('logs in and reads identity without persisting a JavaScript token, and accepts a 204 logout',async()=>{
  vi.stubEnv('NEXT_PUBLIC_API_BASE','')
  delete process.env.NEXT_PUBLIC_API_BASE
  const identity={subject:'alice',roles:['reader'],permissions:['task:read'],expires_at:null}
  const fetchMock=vi.spyOn(globalThis,'fetch').mockResolvedValueOnce(respond(identity)).mockResolvedValueOnce(respond(identity)).mockResolvedValueOnce(new Response(null,{status:204}))
  expect(await login({subject:'alice',password:'secret'})).toEqual(identity)
  expect(await me()).toEqual(identity)
  expect(await logout()).toBeUndefined()
  expect(fetchMock).toHaveBeenNthCalledWith(1,'/api/v1/auth/login',expect.objectContaining({method:'POST',credentials:'same-origin',body:'{"subject":"alice","password":"secret"}'}))
  expect(fetchMock).toHaveBeenNthCalledWith(2,'/api/v1/auth/me',expect.objectContaining({credentials:'same-origin'}))
  expect(fetchMock).toHaveBeenNthCalledWith(3,'/api/v1/auth/logout',expect.objectContaining({method:'POST'}))
 })

 it.each([401,403])('preserves HTTP %s and the server error code',async status=>{
  vi.spyOn(globalThis,'fetch').mockResolvedValue(new Response(JSON.stringify({data:null,error:{code:status===401?'unauthorized':'forbidden',message:'access denied'},request_id:'r'}),{status}))
  const error=await me().catch(error=>error)
  expect(error).toBeInstanceOf(ApiError)
  expect(error).toMatchObject({status,code:status===401?'unauthorized':'forbidden',message:'access denied'})
 })

 it('preserves HTTP errors when a proxy returns a non-JSON response',async()=>{
  vi.spyOn(globalThis,'fetch').mockResolvedValue(new Response('upstream unavailable',{status:502}))
  await expect(me()).rejects.toMatchObject({status:502,code:'http_error'})
 })

 it('rejects a malformed successful response at the protocol boundary',async()=>{
  vi.spyOn(globalThis,'fetch').mockResolvedValue(new Response('not json',{status:200}))
  await expect(me()).rejects.toMatchObject({status:200,code:'invalid_response'})
 })

 it('encodes user and role names and sends management fields without renaming them',async()=>{
  const fetchMock=vi.spyOn(globalThis,'fetch').mockImplementation(async()=>respond([]))
  await listUsers(); await createUser({subject:'a/b',password:'secret',roles:['reader']});await updateUser('a/b',{disabled:true,roles:[]})
  await listRoles();await createRole({name:'ops team',permissions:['task:read']});await updateRole('ops/team',{permissions:['admin:*']});await listPermissions()
  const calls=fetchMock.mock.calls
  expect(calls.map(call=>call[0])).toEqual(['/api/v1/admin/users','/api/v1/admin/users','/api/v1/admin/users/a%2Fb','/api/v1/admin/roles','/api/v1/admin/roles','/api/v1/admin/roles/ops%2Fteam','/api/v1/admin/permissions'])
  expect(calls[1][1]).toMatchObject({method:'POST',body:JSON.stringify({subject:'a/b',password:'secret',roles:['reader']})})
  expect(calls[2][1]).toMatchObject({method:'PATCH',body:JSON.stringify({disabled:true,roles:[]})})
  expect(calls[4][1]).toMatchObject({method:'POST',body:JSON.stringify({name:'ops team',permissions:['task:read']})})
  expect(calls[5][1]).toMatchObject({method:'PATCH',body:JSON.stringify({permissions:['admin:*']})})
 })
})
