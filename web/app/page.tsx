import Link from 'next/link'
export default function Home(){return <><h1>协同智能体开发议会</h1><p>让多个模型独立提案、匿名互审、裁判决策，并在人工批准后执行。</p><div className="card"><h2>开始一个任务</h2><p>先配置 Provider 与工作区，然后创建需求。</p><Link href="/tasks/new"><button>新建任务</button></Link></div></>}
