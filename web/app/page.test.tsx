// @vitest-environment jsdom
import {render,screen} from '@testing-library/react'
import {describe,expect,it,vi} from 'vitest'

vi.mock('@/lib/api',()=>({getTask:vi.fn(async()=>{throw new Error('not found')}),listTasks:vi.fn(async()=>({items:[]}))}))
vi.mock('@/components/app-shell',()=>({useWorkspace:()=>({workspaces:[],currentId:'',setCurrentId:()=>{},reload:()=>{}})}))

import Home from './page'

describe('Home (command center)',()=>{
  it('shows the command center heading, KPI overview and primary actions',async()=>{
    render(<Home />)

    expect(screen.getByRole('heading',{name:'指挥中心'})).toBeTruthy()
    expect(screen.getAllByRole('link',{name:'＋ 新建任务'})[0].getAttribute('href')).toBe('/tasks/new')
    expect(screen.getByRole('link',{name:'全部任务'}).getAttribute('href')).toBe('/tasks')
    expect(screen.getByLabelText('任务概览').textContent).toContain('待批准')
    expect(await screen.findByRole('heading',{name:'还没有任务'})).toBeTruthy()
  })
})
