'use client'

import {WorkspaceKind} from '@/lib/types'

export type WorkspaceKindFilter = WorkspaceKind|'all'

const KIND_OPTIONS:{value:WorkspaceKindFilter;label:string}[] = [
  {value:'all',label:'全部'},
  {value:'standard',label:'标准'},
  {value:'model_private',label:'模型私有'},
  {value:'group_shared',label:'群共享'},
]

export const WORKSPACE_KIND_LABEL:Record<WorkspaceKind,string> = {
  standard:'标准',
  model_private:'模型私有',
  group_shared:'群共享',
}

export function KindFilterChips({value,onChange}:{value:WorkspaceKindFilter;onChange:(v:WorkspaceKindFilter)=>void}) {
  return (
    <div className="radio-row" role="radiogroup" aria-label="工作区类型">
      {KIND_OPTIONS.map(option => (
        <button key={option.value} role="radio" aria-checked={value === option.value}
          className={value === option.value ? '' : 'secondary'} onClick={() => onChange(option.value)}>
          {option.label}
        </button>
      ))}
    </div>
  )
}

export function ModelPrivateBanner({modelLabel,readOnly}:{modelLabel?:string;readOnly:boolean}) {
  return (
    <div role="note" className="card">
      <p>🔒 这是 AI 模型{modelLabel ? <> <strong>{modelLabel}</strong></> : ''} 的私有工作区。只有在人工批准后，执行才会进入这里。{readOnly ? '当前账号对该工作区为只读。' : ''}</p>
    </div>
  )
}
