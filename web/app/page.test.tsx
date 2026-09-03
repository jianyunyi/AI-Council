// @vitest-environment jsdom
import {render,screen} from '@testing-library/react'
import {describe,expect,it,vi} from 'vitest'

vi.mock('@/lib/desktop-bridge',()=>({desktopBridge:()=>undefined}))

import Home from './page'

describe('Home',()=>{
  it('shows the command-center promise and primary workflow links',()=>{
    render(<Home />)

    expect(screen.getByRole('heading',{name:'让多模型达成可执行共识'})).toBeTruthy()
    expect(screen.getByRole('link',{name:'创建协商任务'}).getAttribute('href')).toBe('/tasks/new')
    expect(screen.getByRole('link',{name:'选择工作区'}).getAttribute('href')).toBe('/workspaces')
  })
})
