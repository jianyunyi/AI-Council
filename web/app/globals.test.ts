import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {describe, expect, it} from 'vitest'

const stylesheet = readFileSync(resolve(__dirname, 'globals.css'), 'utf8')

describe('global visual system', () => {
  it('defines the application, home, and service layout primitives', () => {
    for (const selector of ['.app-shell', '.hero-grid', '.service-panel']) {
      expect(stylesheet).toContain(selector)
    }
    expect(stylesheet).toMatch(/--card-radius:\s*16px/)
    expect(stylesheet).toMatch(/\.card\s*\{[\s\S]*?border-radius:\s*var\(--card-radius\)/)
    expect(stylesheet).toContain(':focus-visible')
  })

  it('defines every element of the council orbit scene and its motion', () => {
    for (const selector of [
      '.council-orbit',
      '.council-orbit__core',
      '.council-orbit__ring',
      '.council-orbit__seat--1',
      '.council-orbit__seat--2',
      '.council-orbit__seat--3',
    ]) {
      expect(stylesheet).toContain(selector)
    }
    for (const animation of ['background-current', 'energy-flow', 'field-turn', 'orbit-spin', 'core-pulse', 'seat-drift']) {
      expect(stylesheet).toContain(`@keyframes ${animation}`)
    }
  })

  it('preserves reduced-motion and mobile safety requirements', () => {
    expect(stylesheet).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[\s\S]*?animation:\s*none[\s\S]*?transition:\s*none/)
    expect(stylesheet).toMatch(/@media\s*\(max-width:\s*800px\)\s*\{[\s\S]*?\.hero-grid\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/)
    expect(stylesheet).toMatch(/(?:button,\s*\.hero-grid__actions a|input,\s*textarea,\s*select)\s*\{[\s\S]*?min-height:\s*44px/)
    expect(stylesheet).toMatch(/@media\s*\(max-width:\s*800px\)\s*\{[\s\S]*?(?:overflow-wrap:\s*anywhere|overflow-x:\s*hidden)/)
    expect(stylesheet).not.toContain('100vh')
  })

  it('caps the mobile orbit without contradictory height constraints', () => {
    expect(stylesheet).toMatch(/@media\s*\(max-width:\s*800px\)\s*\{[\s\S]*?\.council-orbit\s*\{[^}]*height:\s*min\(350px,\s*55svh\)[^}]*min-height:\s*0/)
    expect(stylesheet).not.toMatch(/@media\s*\(max-width:\s*800px\)\s*\{[\s\S]*?\.council-orbit\s*\{[^}]*min-height:\s*350px[^}]*max-height:\s*55svh/)
  })

  it('gives the approval checkbox label a 44px touch target', () => {
    expect(stylesheet).toMatch(/label:has\(input\[type="checkbox"\]\)\s*\{[^}]*min-height:\s*44px[^}]*align-items:\s*center/)
  })

  it('styles the complete RBAC path and operational states', () => {
    for (const selector of ['.auth-layout', '.auth-card', '.identity-band', '.management-columns', '.management-table', '.editor-panel', '.status-chip']) {
      expect(stylesheet).toContain(selector)
    }
    expect(stylesheet).toContain('::selection')
    expect(stylesheet).toContain('scrollbar-color')
    expect(stylesheet).toContain('.sr-only')
    expect(stylesheet).toMatch(/@media\s*\(max-width:\s*800px\)[\s\S]*?\.management-columns\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/)
  })

  it('keeps display tracking within the Impeccable craft floor', () => {
    expect(stylesheet).not.toMatch(/letter-spacing:\s*-\.(?:0[5-9]|[1-9]\d)em/)
  })
})
