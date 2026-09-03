// @vitest-environment jsdom
import {render,screen} from '@testing-library/react'
import {describe,expect,it} from 'vitest'
import {CouncilOrbit} from './council-orbit'

describe('CouncilOrbit',()=>{
  it('exposes the multi-agent approval flow to assistive technology',()=>{
    render(<CouncilOrbit />)

    expect(screen.getByLabelText('多智能体协商流程')).toBeTruthy()
    const labels=['需求核心','提案模型','审查模型','裁判模型','独立提案','匿名互审','人工批准','受控执行']
    labels.forEach(label=>{
      expect(screen.getByText(label)).toBeTruthy()
    })
  })
})
