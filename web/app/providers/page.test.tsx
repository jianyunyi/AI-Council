// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const api=vi.hoisted(()=>({listProviderKeys:vi.fn(),createProviderKey:vi.fn(),deleteProviderKey:vi.fn(),listAIModels:vi.fn(async()=>[] as unknown[]),listProviderModels:vi.fn(async()=>({models:[] as {id:string;verified:boolean}[]})),createAIModel:vi.fn(),updateAIModel:vi.fn(),disableAIModel:vi.fn(),retryAIModelWorkspace:vi.fn()}))
vi.mock('@/lib/api',()=>api)
import Providers from './page'

afterEach(()=>{cleanup();vi.clearAllMocks()})

describe('Provider keys page',()=>{
 it('loads profiles and stores a key without showing its plaintext',async()=>{
  // The AI models section also lists provider keys; use a counter so call
  // order between the page and the section does not matter.
  let keyCalls=0
  api.listProviderKeys.mockImplementation(async()=>{keyCalls++;return keyCalls===1?[]:[{id:'k1',provider:'openai',model:'gpt-4o',masked_key:'••••cret',has_key:true,created_at:'2026-01-01'}]})
  api.createProviderKey.mockResolvedValue({id:'k1'})
  render(<Providers/>)
  fireEvent.change(screen.getByLabelText('API Key'),{target:{value:'sk-live-super-secret'}})
  fireEvent.click(screen.getByRole('button',{name:'加密保存 Key'}))
  await waitFor(()=>expect(api.createProviderKey).toHaveBeenCalledWith({provider:'openai',model:'gpt-4o',api_key:'sk-live-super-secret'}))
  expect(await screen.findByText(/••••cret/)).toBeTruthy()
  expect(screen.queryByText('sk-live-super-secret')).toBeNull()
 })
 it('deletes a saved profile',async()=>{
  api.listProviderKeys.mockResolvedValue([{id:'k1',provider:'deepseek',model:'deepseek-chat',masked_key:'••••7890',has_key:true,created_at:'2026-01-01'}])
  api.deleteProviderKey.mockResolvedValue(undefined)
  render(<Providers/>)
  fireEvent.click(await screen.findByRole('button',{name:'删除'}))
  fireEvent.click(await screen.findByRole('button',{name:'删除密钥'}))
  await waitFor(()=>expect(api.deleteProviderKey).toHaveBeenCalledWith('k1'))
  await waitFor(()=>expect(screen.queryByText(/deepseek-chat/)).toBeNull())
 })
})
