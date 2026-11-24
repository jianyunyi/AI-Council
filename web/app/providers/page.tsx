'use client'
import {useCallback,useEffect,useState} from 'react'
import {ConfirmDialog, ConfirmState, showToast} from '@/components/ui'
import {AIModelsSection} from '@/components/ai-models'
import {createProviderKey,deleteProviderKey,listProviderKeys,ProviderKeyProfile} from '@/lib/api'

export default function Providers(){
 const [provider,setProvider]=useState<ProviderKeyProfile['provider']>('openai'),[model,setModel]=useState('gpt-4o'),[key,setKey]=useState(''),[showKey,setShowKey]=useState(false),[profiles,setProfiles]=useState<ProviderKeyProfile[]>([]),[message,setMessage]=useState(''),[busy,setBusy]=useState(false)
 const [confirm,setConfirm]=useState<ConfirmState|null>(null),[pendingDelete,setPendingDelete]=useState<ProviderKeyProfile|null>(null)
 const refresh=useCallback(async()=>{try{setProfiles(await listProviderKeys())}catch(error){setMessage(error instanceof Error?error.message:'读取 Provider Key 失败')}},[])
 useEffect(()=>{void refresh()},[refresh])
 async function save(){if(!key.trim()||!model.trim())return;setBusy(true);setMessage('');try{await createProviderKey({provider,model:model.trim(),api_key:key});setKey('');setMessage('密钥已加密保存。新任务会使用此账号配置的模型。');showToast({message:'密钥已加密保存。'});await refresh()}catch(error){setMessage(error instanceof Error?error.message:'保存失败')}finally{setBusy(false)}}
 function remove(profile:ProviderKeyProfile){setPendingDelete(profile);setConfirm({title:`删除 ${profile.provider} · ${profile.model} 的密钥？`,description:<p>删除后，使用该模型的任务将无法继续。密钥在服务器端加密保存，删除不可恢复。</p>,confirmLabel:'删除密钥'})}
 async function resolveDelete(confirmed:boolean){setConfirm(null);const profile=pendingDelete;setPendingDelete(null);if(!confirmed||!profile)return;setBusy(true);try{await deleteProviderKey(profile.id);setProfiles(items=>items.filter(item=>item.id!==profile.id));setMessage('密钥已删除。')}catch(error){setMessage(error instanceof Error?error.message:'删除失败')}finally{setBusy(false)}}
 return <div className="page-stack">
  <div className="page-head"><div><h1>Provider</h1><p className="page-sub">为当前账号配置模型厂商与密钥。密钥在服务器端使用 AES-GCM 加密，不会再次明文显示。</p></div></div>
  <section className="card"><h2>添加密钥</h2><div className="form-grid">
   <label>厂商<select value={provider} onChange={event=>setProvider(event.target.value as ProviderKeyProfile['provider'])}><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="deepseek">DeepSeek</option></select></label>
   <label>模型<input value={model} onChange={event=>setModel(event.target.value)} autoComplete="off" /></label>
   <label>API Key<span className="key-field"><input type={showKey?'text':'password'} value={key} onChange={event=>setKey(event.target.value)} autoComplete="new-password" /><button type="button" className="small secondary" aria-pressed={showKey} onClick={()=>setShowKey(value=>!value)}>{showKey?'隐藏':'显示'}</button></span></label>
  </div>{message&&<p role="status">{message}</p>}<div className="form-actions"><button onClick={save} disabled={busy||!key.trim()||!model.trim()}>{busy?'正在保存…':'加密保存 Key'}</button></div></section>
  <section className="card"><h2>已配置模型</h2>{profiles.length===0?<p className="empty">还没有配置模型密钥。</p>:<div className="tw"><table className="tbl"><thead><tr><th>厂商</th><th>模型</th><th>密钥</th><th>操作</th></tr></thead><tbody>{profiles.map(profile=><tr key={profile.id}><td>{profile.provider}</td><td>{profile.model}</td><td>{profile.masked_key}</td><td><button className="btn small danger" disabled={busy} onClick={()=>remove(profile)}>删除</button></td></tr>)}</tbody></table></div>}
  <p className="small muted">议会角色（分析、提案、互评、裁决、红队、执行、验证）使用哪个模型在「设置」页的议会角色区分配。</p></section>
  <AIModelsSection/>
  <ConfirmDialog request={confirm} onResolve={resolveDelete}/>
 </div>
}
