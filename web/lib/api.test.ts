import {afterEach,describe,expect,it,vi} from 'vitest'
import {approveTask,createTask,createWorkspace,executeTask,listWorkspaces,startTask} from './api'

describe('task lifecycle API',()=>{
 afterEach(()=>vi.restoreAllMocks())
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
