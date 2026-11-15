import {Easing,interpolate,staticFile,useCurrentFrame,useVideoConfig} from 'remotion'

export const DELIBERATION_STAGES=['独立提案','匿名互审','裁判决策','人工批准','受控执行'] as const
export const DELIBERATION_FPS=30
export const DELIBERATION_DURATION_IN_FRAMES=360

export function getActiveStageIndex(frame:number,fps:number){
  return Math.floor(frame/(2*fps))%DELIBERATION_STAGES.length
}

export function DeliberationComposition(){
  const frame=useCurrentFrame()
  const {fps}=useVideoConfig()
  const activeStage=getActiveStageIndex(frame,fps)

  return <div style={{background:'#07111f',color:'#dcecff',fontFamily:'system-ui, sans-serif',height:'100%',overflow:'hidden',position:'relative'}}>
    <img alt="" src={staticFile('council-tower-plate.png')} style={{filter:'saturate(1.08) contrast(1.04)',height:'112%',left:'50%',objectFit:'contain',opacity:interpolate(frame,[0,fps*2],[0.42,0.9],{extrapolateRight:'clamp',easing:Easing.bezier(0.16,1,0.3,1)}),position:'absolute',top:'-6%',translate:'-50% 0',width:'72%'}} />
    <div style={{background:'linear-gradient(180deg, rgba(7,17,31,.04), rgba(7,17,31,.78))',inset:0,position:'absolute'}} />
    <div style={{alignItems:'center',border:'1px solid rgba(131,167,161,.8)',borderRadius:'50% 50% 12% 12%',display:'flex',flexDirection:'column',height:'68%',justifyContent:'center',left:'37%',position:'absolute',top:'11%',width:'26%'}}>
      <span style={{color:'#83a7a1',fontSize:28,fontWeight:700}}>AI</span>
      <span style={{fontSize:12,letterSpacing:2}}>COUNCIL</span>
    </div>
    <div style={{bottom:'12%',display:'flex',gap:12,left:'6%',position:'absolute',right:'6%'}}>
      {DELIBERATION_STAGES.map((stage,index)=><div key={stage} style={{alignItems:'center',background:index===activeStage?(index===3?'#e7d6b0':'#c76f38'):'rgba(7,17,31,.78)',borderRadius:8,color:index===activeStage?'#1b2731':'#dcecff',display:'flex',flex:1,fontSize:12,justifyContent:'center',minHeight:46,opacity:interpolate(frame,[index*2*fps,index*2*fps+12],[0.55,1],{extrapolateLeft:'clamp',extrapolateRight:'clamp',easing:Easing.bezier(0.16,1,0.3,1)}),scale:index===activeStage?1.04:1,textAlign:'center'}}>{stage}</div>)}
    </div>
  </div>
}
