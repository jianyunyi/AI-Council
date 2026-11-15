import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {describe, expect, it} from 'vitest'

const stylesheet = readFileSync(resolve(__dirname, 'globals.css'), 'utf8')

describe('global visual system', () => {
  it('defines the application, home, and service layout primitives', () => {
    for (const selector of ['.app-shell', '.tower-hero', '.service-panel']) {
      expect(stylesheet).toContain(selector)
    }
    expect(stylesheet).toMatch(/--card-radius:\s*16px/)
    expect(stylesheet).toMatch(/\.card\s*\{[\s\S]*?border-radius:\s*var\(--card-radius\)/)
    expect(stylesheet).toContain(':focus-visible')
  })

  it('defines the Council Tower palette and its operational surfaces', () => {
    for (const [token, color] of Object.entries({ink:'#1b2731',clay:'#c76f38',bone:'#e7d6b0',mineral:'#83a7a1'})) {
      expect(stylesheet).toMatch(new RegExp(`--tower-${token}:\\s*${color}`))
    }
    for (const selector of [
      '.tower-hero__command', '.tower-hero__rail', '.council-tower',
      '.council-tower__evidence', '.operation-page', '.operation-band', '.operation-plane',
    ]) {
      expect(stylesheet).toContain(selector)
    }
    expect(stylesheet).not.toContain('council-orbit')
    expect(stylesheet).not.toContain('gradient(')
    expect(stylesheet).not.toMatch(/border-(?:left|right):\s*(?:[2-9]|\d{2,})px\s+solid/)
  })

  it('gives the command, tower, and evidence rail three shrinkable desktop columns', () => {
    expect(stylesheet).toMatch(/\.tower-hero\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*[\d.]+fr\)\s+minmax\(0,\s*[\d.]+fr\)\s+minmax\(0,\s*[\d.]+fr\)/)
    expect(stylesheet).toMatch(/\.tower-hero\s*>\s*\*\s*\{[^}]*min-width:\s*0/)
  })

  it('preserves reduced-motion and mobile safety requirements', () => {
    expect(stylesheet).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?animation:\s*none[\s\S]*?transition:\s*none/)
    expect(stylesheet).toMatch(/@media\s*\(max-width:\s*800px\)\s*\{[\s\S]*?\.tower-hero\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/)
    expect(stylesheet).toMatch(/(?:button,\s*\.hero-grid__actions a|input,\s*textarea,\s*select)\s*\{[\s\S]*?min-height:\s*44px/)
    expect(stylesheet).toMatch(/@media\s*\(max-width:\s*800px\)\s*\{[\s\S]*?(?:overflow-wrap:\s*anywhere|overflow-x:\s*hidden)/)
    expect(stylesheet).not.toContain('100vh')
  })

  it('replaces the motion player with one visible static tower plate for reduced motion', () => {
    expect(stylesheet).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.council-tower__player\s*\{[^}]*display:\s*none/)
    expect(stylesheet).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.council-tower:has\(\.council-tower__player\)\s+\.council-tower__plate\s*\{[^}]*display:\s*block/)
    expect(stylesheet).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?\.council-tower__fallback\s*\{[^}]*display:\s*none/)
  })

  it('gives the approval checkbox label a 44px touch target', () => {
    expect(stylesheet).toMatch(/label:has\(input\[type="checkbox"\]\)\s*\{[^}]*min-height:\s*44px[^}]*align-items:\s*center/)
  })

  it('keeps compact operational actions at least 44px tall', () => {
    expect(stylesheet).toMatch(/\.table-action,\s*\.quiet-button\s*\{[^}]*min-height:\s*44px/)
  })

  it('styles the complete RBAC path and operational states', () => {
    for (const selector of ['.auth-layout', '.auth-card', '.identity-band', '.management-columns', '.management-table', '.editor-panel', '.status-chip']) {
      expect(stylesheet).toContain(selector)
    }
    expect(stylesheet).toContain('::selection')
    expect(stylesheet).toContain('scrollbar-color')
    expect(stylesheet).toContain('.sr-only')
    expect(stylesheet).toContain('[role="status"]')
    expect(stylesheet).toContain('[role="alert"]')
    expect(stylesheet).toMatch(/@media\s*\(max-width:\s*800px\)[\s\S]*?\.management-columns\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/)
  })

  it('keeps display tracking within the Impeccable craft floor', () => {
    expect(stylesheet).not.toMatch(/letter-spacing:\s*-\.(?:0[5-9]|[1-9]\d)em/)
  })
})
