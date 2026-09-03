'use client'

const seats=['提案模型','审查模型','裁判模型']
const steps=['独立提案','匿名互审','人工批准','受控执行']

export function CouncilOrbit(){
  return <section className="council-orbit" aria-label="多智能体协商流程">
    <h2 className="council-orbit__title">多智能体协商流程</h2>
    <p className="council-orbit__core">需求核心</p>
    <div className="council-orbit__ring" aria-hidden="true" />
    <ul className="council-orbit__seats" aria-label="协商模型">
      {seats.map((seat,index)=><li className={`council-orbit__seat council-orbit__seat--${index+1}`} key={seat}>{seat}</li>)}
    </ul>
    <ol className="council-orbit__steps">
      {steps.map(step=><li key={step}>{step}</li>)}
    </ol>
  </section>
}
