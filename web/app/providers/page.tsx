'use client'
import {useCallback,useEffect,useState} from 'react'
import {createProviderKey,deleteProviderKey,listProviderKeys,ProviderKeyProfile} from '@/lib/api'

export default function Providers(){
 const [provider,setProvider]=useState<ProviderKeyProfile['provider']>('openai'),[model,setModel]=useState('gpt-4o'),[key,setKey]=useState(''),[profiles,setProfiles]=useState<ProviderKeyProfile[]>([]),[message,setMessage]=useState(''),[busy,setBusy]=useState(false)
 const refresh=useCallback(async()=>{try{setProfiles(await listProviderKeys())}catch(error){setMessage(error instanceof Error?error.message:'读取 Provider Key 失败')}},[])
 useEffect(()=>{void refresh()},[refresh])
 async function save(){if(!key.trim()||!model.trim())return;setBusy(true);setMessage('');try{await createProviderKey({provider,model:model.trim(),api_key:key});setKey('');setMessage('密钥已加密保存。新任务会使用此账号配置的模型。');await refresh()}catch(error){setMessage(error instanceof Error?error.message:'保存失败')}finally{setBusy(false)}}
 async function remove(id:string){setBusy(true);try{await deleteProviderKey(id);setProfiles(items=>items.filter(item=>item.id!==id));setMessage('密钥已删除。')}catch(error){setMessage(error instanceof Error?error.message:'删除失败')}finally{setBusy(false)}}
 return <div className="operation-page"><header className="operation-band"><h1>模型 Provider</h1></header><section className="operation-plane"><div className="card"><p>密钥在服务器端使用 AES-GCM 加密，并仅关联到当前账号。它不会再次明文显示。</p><label>厂商<select value={provider} onChange={event=>setProvider(event.target.value as ProviderKeyProfile['provider'])}><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="deepseek">DeepSeek</option></select></label><label>模型<input value={model} onChange={event=>setModel(event.target.value)} autoComplete="off" /></label><label>API Key<input type="password" value={key} onChange={event=>setKey(event.target.value)} autoComplete="new-password" /></label>{message&&<p role="status">{message}</p>}<button onClick={save} disabled={busy||!key.trim()||!model.trim()}>{busy?'正在保存…':'加密保存 Key'}</button></div><div className="card"><h2>已配置模型</h2>{profiles.length===0?<p>还没有配置模型密钥。</p>:<ul>{profiles.map(profile=><li key={profile.id}>{profile.provider} · {profile.model} · {profile.masked_key}<button disabled={busy} onClick={()=>void remove(profile.id)}>删除</button></li>)}</ul>}</div></section></div>
}
