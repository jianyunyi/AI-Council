'use client'

import Link from 'next/link'
import {createContext,useCallback,useContext,useEffect,useRef,useState} from 'react'
import {ApiError,login,logout,me,onUnauthorized} from '@/lib/api'
import {getDesktopRuntime} from '@/lib/desktop'
import {Identity} from '@/lib/types'

type AuthStatus='loading'|'anonymous'|'authenticated'|'error'
type AuthValue={
 status:AuthStatus
 identity:Identity|null
 error:Error|null
 isDesktop:boolean
 refresh:()=>Promise<void>
 signIn:(subject:string,password:string)=>Promise<void>
 signOut:()=>Promise<void>
 can:(permission:string)=>boolean
}

const AuthContext=createContext<AuthValue|undefined>(undefined)

export function can(identity:Identity|null|undefined,permission:string){
 return Boolean(identity?.permissions.some(granted=>granted===permission||(granted.endsWith(':*')&&permission.startsWith(granted.slice(0,-1)))))
}

export function AuthProvider({children}:{children:React.ReactNode}){
 const [runtime,setRuntime]=useState<'checking'|'browser'|'desktop'>('checking')
 const isDesktop=runtime==='desktop'
 const [status,setStatus]=useState<AuthStatus>('loading')
 const [identity,setIdentity]=useState<Identity|null>(null)
 const [error,setError]=useState<Error|null>(null)
 const revision=useRef(0)

 const clear=useCallback(()=>{revision.current++;setIdentity(null);setError(null);setStatus('anonymous')},[])
 const refresh=useCallback(async()=>{
  if(runtime!=='browser'){if(runtime==='desktop')setStatus('anonymous');return}
  const current=++revision.current
  setStatus('loading');setError(null)
  try{
   const next=await me()
   if(current!==revision.current)return
   setIdentity(next);setStatus('authenticated')
  }catch(reason){
   if(current!==revision.current)return
   setIdentity(null)
   if(reason instanceof ApiError&&reason.status===401){setStatus('anonymous');return}
   setError(reason instanceof Error?reason:new Error('无法读取会话'));setStatus('error')
  }
 },[runtime])

 useEffect(()=>{const next=Boolean(getDesktopRuntime())?'desktop':'browser';setRuntime(next);if(next==='desktop')setStatus('anonymous')},[])
 useEffect(()=>{if(runtime==='browser')void refresh()},[runtime,refresh])
 useEffect(()=>onUnauthorized(clear),[clear])
 useEffect(()=>{
  if(status!=='authenticated'||!identity?.expires_at)return
  const delay=new Date(identity.expires_at).getTime()-Date.now()
  if(delay<=0){clear();return}
  const timer=window.setTimeout(clear,delay)
  return()=>window.clearTimeout(timer)
 },[clear,identity?.expires_at,status])

 const signIn=useCallback(async(subject:string,password:string)=>{
  const current=++revision.current
  setError(null);setStatus('loading')
  try{const next=await login({subject,password});if(current===revision.current){setIdentity(next);setStatus('authenticated')}}
  catch(reason){if(current===revision.current){const next=reason instanceof Error?reason:new Error('登录失败');setIdentity(null);setError(next);setStatus('anonymous')}throw reason}
 },[])
 const signOut=useCallback(async()=>{
  setError(null)
  try{await logout();clear()}catch(reason){if(reason instanceof ApiError&&reason.status===401){clear();return}const next=reason instanceof Error?reason:new Error('退出失败');setError(next);throw reason}
 },[clear])
 const value:AuthValue={status,identity,error,isDesktop,refresh,signIn,signOut,can:permission=>can(identity,permission)}
 return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(){const value=useContext(AuthContext);if(!value)throw new Error('useAuth must be used within AuthProvider');return value}

export function RequirePermission({permission,allowDesktop=false,children}:{permission:string;allowDesktop?:boolean;children:React.ReactNode}){
 const auth=useAuth()
 if(auth.status==='loading')return <p role="status">正在验证访问权限…</p>
 if(auth.status==='error')return <div role="alert"><p>{auth.error?.message??'无法验证访问权限'}</p><button className="secondary" onClick={()=>void auth.refresh()}>重试</button></div>
 if(auth.isDesktop&&allowDesktop)return <>{children}</>
 if(auth.status==='anonymous')return <p role="alert">请先 <Link href="/login">登录</Link> 后继续。</p>
 if(!auth.can(permission))return <p role="alert">当前账户没有访问此功能的权限。</p>
 return <>{children}</>
}
