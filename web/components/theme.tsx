'use client'

import {createContext, useCallback, useContext, useEffect, useState} from 'react'

export type ThemeMode = 'dark' | 'light' | 'system'
export type Density = 'standard' | 'compact'
export type DiffView = 'unified' | 'side-by-side'

type Prefs = {
  theme: ThemeMode
  density: Density
  diffView: DiffView
  setTheme: (theme: ThemeMode) => void
  setDensity: (density: Density) => void
  setDiffView: (view: DiffView) => void
}

const fallback: Prefs = {
  theme: 'dark',
  density: 'standard',
  diffView: 'unified',
  setTheme: () => {},
  setDensity: () => {},
  setDiffView: () => {},
}

const PrefsContext = createContext<Prefs>(fallback)

function read<T extends string>(key: string, valid: readonly T[], initial: T): T {
  if (typeof window === 'undefined') return initial
  const value = window.localStorage.getItem(key) as T | null
  return value && valid.includes(value) ? value : initial
}

export function PrefsProvider({children}: {children: React.ReactNode}) {
  const [theme, setThemeState] = useState<ThemeMode>('dark')
  const [density, setDensityState] = useState<Density>('standard')
  const [diffView, setDiffViewState] = useState<DiffView>('unified')

  useEffect(() => {
    setThemeState(read('aicouncil.prefs.theme', ['dark', 'light', 'system'] as const, 'dark'))
    setDensityState(read('aicouncil.prefs.density', ['standard', 'compact'] as const, 'standard'))
    setDiffViewState(read('aicouncil.prefs.diffView', ['unified', 'side-by-side'] as const, 'unified'))
  }, [])

  useEffect(() => {
    const root = document.documentElement
    if (theme === 'system') root.removeAttribute('data-theme')
    else root.dataset.theme = theme
    window.localStorage.setItem('aicouncil.prefs.theme', theme)
  }, [theme])

  useEffect(() => {
    const root = document.documentElement
    if (density === 'compact') root.dataset.density = 'compact'
    else root.removeAttribute('data-density')
    window.localStorage.setItem('aicouncil.prefs.density', density)
  }, [density])

  const setTheme = useCallback((next: ThemeMode) => setThemeState(next), [])
  const setDensity = useCallback((next: Density) => setDensityState(next), [])
  const setDiffView = useCallback((next: DiffView) => {
    setDiffViewState(next)
    window.localStorage.setItem('aicouncil.prefs.diffView', next)
  }, [])

  return <PrefsContext.Provider value={{theme, density, diffView, setTheme, setDensity, setDiffView}}>{children}</PrefsContext.Provider>
}

export function usePrefs() {
  return useContext(PrefsContext)
}
