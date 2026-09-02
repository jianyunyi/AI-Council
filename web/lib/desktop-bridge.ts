export type DesktopStatus = {
  state: string
  workspace: string
  council_url: string
  last_error?: string
}

type BoundDesktopApp = {
  SaveProviderKey(provider: string, key: string): Promise<void>
  OpenWorkspace(path: string): Promise<void>
  Start(): Promise<DesktopStatus>
  Stop(): Promise<void>
  Status(): Promise<DesktopStatus>
  ExportDiagnostics(destination: string): Promise<string>
}

declare global {
  interface Window {
    go?: { main?: { DesktopApp?: BoundDesktopApp } }
  }
}

export function desktopBridge(){
  if(typeof window==='undefined') return undefined
  const app=window.go?.main?.DesktopApp
  if(!app) return undefined
  return {
    saveProviderKey:(provider:string,key:string)=>app.SaveProviderKey(provider,key),
    openWorkspace:(path:string)=>app.OpenWorkspace(path),
    start:()=>app.Start(),
    stop:()=>app.Stop(),
    status:()=>app.Status(),
    exportDiagnostics:(destination:string)=>app.ExportDiagnostics(destination),
  }
}
