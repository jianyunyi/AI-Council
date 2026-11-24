// @vitest-environment jsdom
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'

const api=vi.hoisted(()=>({listAIModels:vi.fn(),listProviderKeys:vi.fn(),listProviderModels:vi.fn(),createAIModel:vi.fn(),updateAIModel:vi.fn(),disableAIModel:vi.fn(),retryAIModelWorkspace:vi.fn()}))
vi.mock('@/lib/api',()=>api)
vi.mock('@/components/auth-session',()=>({useOptionalAuth:()=>({isDesktop:false,identity:null,can:()=>true})}))
import {AIModelsSection} from './ai-models'

afterEach(()=>{cleanup();vi.clearAllMocks()})

const model={id:'aim_1',provider_profile_id:'pp1',provider_name:'openai · gpt-4o',provider_type:'openai',model:'gpt-4o',handle:'gpt-4o',display_name:'GPT-4o',default_params:{},status:'active',workspace:{workspace_id:'workspace-9',kind:'model_private',status:'ready',attempts:1,runner_ready:true,updated_at:''},created_at:'',updated_at:''}

describe('AI models section',()=>{
 it('lists enabled models with workspace status',async()=>{
  api.listAIModels.mockResolvedValue([model])
  api.listProviderKeys.mockResolvedValue([])
  render(<AIModelsSection/>)
  expect(await screen.findByText('@gpt-4o')).toBeTruthy()
  expect(await screen.findByText('就绪')).toBeTruthy()
 })
 it('enables a model from a provider profile',async()=>{
  api.listAIModels.mockResolvedValue([])
  api.listProviderKeys.mockResolvedValue([{id:'pp1',provider:'openai',model:'gpt-4o',masked_key:'••••',has_key:true,created_at:''}])
  api.listProviderModels.mockResolvedValue({provider_profile_id:'pp1',provider:'openai',source:'profile',models:[{id:'gpt-4o',verified:true}]})
  api.createAIModel.mockResolvedValue(model)
  render(<AIModelsSection/>)
  fireEvent.click(await screen.findByRole('button',{name:'启用模型'}))
  fireEvent.change(screen.getByLabelText(/Provider 档案/),{target:{value:'pp1'}})
  fireEvent.change(await screen.findByLabelText(/模型/),{target:{value:'gpt-4o'}})
  fireEvent.click(screen.getByRole('button',{name:'启用'}))
  await waitFor(()=>expect(api.createAIModel).toHaveBeenCalledWith({provider_profile_id:'pp1',model:'gpt-4o',handle:'gpt-4o',display_name:undefined}))
 })
 it('disables a model after confirmation',async()=>{
  api.listAIModels.mockResolvedValue([model])
  api.listProviderKeys.mockResolvedValue([])
  api.disableAIModel.mockResolvedValue({...model,status:'disabled'})
  render(<AIModelsSection/>)
  fireEvent.click(await screen.findByRole('button',{name:'停用'}))
  fireEvent.click(await screen.findByRole('button',{name:'停用模型'}))
  await waitFor(()=>expect(api.disableAIModel).toHaveBeenCalledWith('aim_1'))
 })
})
