'use client'

import dynamic from 'next/dynamic'
import {useEffect,useState} from 'react'
import {DELIBERATION_DURATION_IN_FRAMES,DELIBERATION_FPS,DELIBERATION_STAGES,DeliberationComposition} from './remotion/deliberation-composition'

const Player=dynamic(()=>import('@remotion/player').then(module=>module.Player),{ssr:false})

const visuallyHidden={
  clip:'rect(0 0 0 0)',
  clipPath:'inset(50%)',
  height:1,
  margin:-1,
  overflow:'hidden',
  position:'absolute' as const,
  whiteSpace:'nowrap' as const,
  width:1,
}

function DeliberationDetails(){
  return <aside className="council-tower__evidence" style={visuallyHidden}>
    <ol>
      {DELIBERATION_STAGES.map(stage=><li key={stage}>{stage}</li>)}
    </ol>
    <p>人工批准是执行的前提</p>
  </aside>
}

function StaticTower(){
  return <div aria-hidden="true" className="council-tower__fallback" style={{alignItems:'center',background:'#07111f',color:'#dcecff',flexDirection:'column',gap:24,justifyContent:'center',minHeight:280,padding:24}}>
    <div style={{border:'2px solid #45d4c3',borderRadius:'50% 50% 12% 12%',padding:'44px 24px'}}>AI COUNCIL</div>
    <div style={{display:'flex',flexWrap:'wrap',gap:8,justifyContent:'center'}}>{DELIBERATION_STAGES.map(stage=><span key={stage} style={{background:'#14253b',borderRadius:8,padding:'8px 12px'}}>{stage}</span>)}</div>
  </div>
}

export function CouncilTower(){
  const [mounted,setMounted]=useState(false)

  useEffect(()=>setMounted(true),[])

  return <section className="council-tower" aria-label="协商钟塔演示">
    <DeliberationDetails />
    <img alt="" aria-hidden="true" className="council-tower__plate" src="/council-tower-plate.png" />
    {mounted?<div aria-hidden="true" className="council-tower__player"><Player className="council-tower__player" component={DeliberationComposition} durationInFrames={DELIBERATION_DURATION_IN_FRAMES} fps={DELIBERATION_FPS} compositionWidth={960} compositionHeight={540} controls={false} style={{width:'100%'}} /></div>:null}
    <StaticTower />
  </section>
}
