import {TaskWorkbench} from '@/components/task/task-workbench'
export function generateStaticParams(){return [{id:'desktop'}]}
export default function TaskPage({params}:{params:{id:string}}){return <TaskWorkbench id={params.id}/>}
