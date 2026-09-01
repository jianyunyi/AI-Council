import {afterEach,describe,expect,it,vi} from 'vitest'
import {approveTask,executeTask,startTask} from './api'

describe('task lifecycle API',()=>{
 afterEach(()=>vi.restoreAllMocks())
 it('sends the plan approval hash and execution requests',async()=>{
  const fetchMock=vi.spyOn(globalThis,'fetch').mockImplementation(async()=>new Response(JSON.stringify({data:{id:'t-1',state:'SUCCEEDED'},request_id:'r'}),{status:200,headers:{'content-type':'application/json'}}))
  await startTask('t-1'); await approveTask('t-1',3,'hash-3'); await executeTask('t-1')
  expect(fetchMock).toHaveBeenNthCalledWith(1,expect.stringContaining('/tasks/t-1/start'),expect.objectContaining({method:'POST'}))
  expect(fetchMock).toHaveBeenNthCalledWith(2,expect.stringContaining('/tasks/t-1/approve'),expect.objectContaining({body:'{"plan_version":3,"approval_hash":"hash-3"}'}))
  expect(fetchMock).toHaveBeenNthCalledWith(3,expect.stringContaining('/tasks/t-1/execute'),expect.objectContaining({method:'POST'}))
 })
})
