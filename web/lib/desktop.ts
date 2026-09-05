export type DesktopRuntime = {
  apiBase: string
  sessionToken: string
}

declare global {
  interface Window {
    __AI_COUNCIL_DESKTOP__?: DesktopRuntime
  }
}

export function getDesktopRuntime(): DesktopRuntime | undefined {
  if (typeof window === 'undefined') return undefined
  const runtime = window.__AI_COUNCIL_DESKTOP__
  if (!runtime?.apiBase || !runtime.sessionToken) return undefined
  return runtime
}

export function apiBase(): string {
  return getDesktopRuntime()?.apiBase ?? (process.env.NEXT_PUBLIC_API_BASE || '/api/v1')
}

export function authorizationHeader(): Record<string, string> {
  const token = getDesktopRuntime()?.sessionToken
  return token ? {authorization: `Bearer ${token}`} : {}
}
