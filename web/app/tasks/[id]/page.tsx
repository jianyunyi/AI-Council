import {TaskWorkbench} from '@/components/task/task-workbench'
export default function TaskPage({params}:{params:{id:string}}){return <TaskWorkbench id={params.id}/>} 
