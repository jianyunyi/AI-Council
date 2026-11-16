// @vitest-environment jsdom
import {cleanup,render,screen,within} from '@testing-library/react'
import {renderToStaticMarkup} from 'react-dom/server'
import {afterEach,describe,expect,it,vi} from 'vitest'
import {CouncilTower} from './council-tower'
import {getActiveStageIndex} from './remotion/deliberation-composition'

vi.mock('@remotion/player',()=>({
  Player:({className,style}:{className?:string,style?:React.CSSProperties})=><div className={className} data-testid="remotion-player" style={style} />,
}))

afterEach(cleanup)

describe('CouncilTower',()=>{
  it('keeps the deliberation stages and human approval accessible without the animation player',()=>{
    const {container}=render(<CouncilTower />)
    const evidence=within(container.querySelector('.council-tower__evidence') as HTMLElement)

    expect(screen.getByLabelText('协商钟塔演示')).toBeTruthy()
    ;['独立提案','匿名互审','裁判决策','人工批准','受控执行'].forEach(label=>{
      expect(evidence.getByText(label)).toBeTruthy()
    })
    expect(evidence.getByText('人工批准是执行的前提')).toBeTruthy()
  })

  it('sizes the mounted player to its container width',async()=>{
    render(<CouncilTower />)

    expect((await screen.findByTestId('remotion-player')).style.width).toBe('100%')
  })

  it('keeps the static fallback available after the player mounts',async()=>{
    const {container}=render(<CouncilTower />)

    await screen.findByTestId('remotion-player')
    expect(container.querySelector('.council-tower__fallback')).toBeTruthy()
  })

  it('ships the approved council-tower plate as a visual layer',()=>{
    const {container}=render(<CouncilTower />)
    const plate=container.querySelector('.council-tower__plate') as HTMLImageElement

    expect(plate).toBeTruthy()
    expect(plate.src).toContain('/council-tower-plate.png')
    expect(plate.alt).toBe('')
  })

  it('leaves fallback visibility to the responsive stylesheet',()=>{
    const {container}=render(<CouncilTower />)

    expect((container.querySelector('.council-tower__fallback') as HTMLElement).style.display).toBe('')
  })

  it('provides the tower CSS hooks for the player, fallback, and evidence',async()=>{
    const fallbackMarkup=renderToStaticMarkup(<CouncilTower />)
    const {container}=render(<CouncilTower />)

    expect(container.querySelector('.council-tower')).toBeTruthy()
    expect((await screen.findByTestId('remotion-player')).classList.contains('council-tower__player')).toBe(true)
    expect(fallbackMarkup).toContain('council-tower__fallback')
    expect(fallbackMarkup).toContain('council-tower__evidence')
  })

  it('advances the highlighted stage on the two-second council cadence',()=>{
    expect(getActiveStageIndex(0,30)).toBe(0)
    expect(getActiveStageIndex(60,30)).toBe(1)
    expect(getActiveStageIndex(240,30)).toBe(4)
  })
})
