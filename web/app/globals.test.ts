import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {describe, expect, it} from 'vitest'

const stylesheet = readFileSync(resolve(__dirname, 'globals.css'), 'utf8')

describe('global visual system (DESIGN v2)', () => {
  it('defines the oklch + light-dark() token palette with dark as the default scheme', () => {
    expect(stylesheet).toMatch(/:root\s*\{[^}]*color-scheme:\s*light dark/)
    expect(stylesheet).toMatch(/--page-bg:\s*light-dark\(oklch\(97\.5%[^,]+,\s*oklch\(16%/)
    expect(stylesheet).toMatch(/--page-wait:\s*light-dark\(oklch\(54%[^,]+,\s*oklch\(79%/)
    expect(stylesheet).toMatch(/:root\[data-theme="dark"\]\s*\{\s*color-scheme:\s*dark/)
    expect(stylesheet).toMatch(/:root\[data-theme="light"\]\s*\{\s*color-scheme:\s*light/)
    expect(stylesheet).toContain('color-mix(in oklab,')
    expect(stylesheet).not.toContain('100vh')
  })

  it('reserves the amber token for the awaiting-approval state and approval action', () => {
    expect(stylesheet).toMatch(/\.badge--wait\s*\{[^}]*var\(--page-wait\)/)
    expect(stylesheet).toMatch(/\.hold-btn[\s\S]*?var\(--page-wait\)/)
    for (const state of ['draft', 'delib', 'wait', 'exec', 'done', 'fail']) {
      expect(stylesheet).toContain(`.badge--${state}`)
    }
  })

  it('makes <main> the only size container and reflows the task grid by container width', () => {
    expect(stylesheet).toMatch(/\.app-main\s*\{[^}]*container-type:\s*inline-size/)
    expect(stylesheet).toContain('@container main (min-width: 1180px)')
    expect(stylesheet).toContain('@container main (min-width: 880px)')
    expect(stylesheet).toMatch(/\.task-grid\s*\{[^}]*grid-template-areas:\s*"ap" "tl" "center"/)
  })

  it('keeps every table inside its own horizontal scroll container', () => {
    expect(stylesheet).toMatch(/\.tw\s*\{\s*overflow-x:\s*auto/)
    expect(stylesheet).toMatch(/table\.tbl td\s*\{[^}]*height:\s*var\(--row-h\)/)
  })

  it('styles native dialog and popover with top-layer entry/exit transitions', () => {
    expect(stylesheet).toContain('@starting-style')
    expect(stylesheet).toContain('allow-discrete')
    expect(stylesheet).toMatch(/dialog:modal\s*\{\s*overscroll-behavior:\s*contain/)
    expect(stylesheet).toMatch(/html:has\(dialog:modal\)\s*\{\s*overflow:\s*hidden/)
  })

  it('derives the approval panel readiness from checkbox state with :has() and a CSS counter', () => {
    expect(stylesheet).toContain('.apbox:has(.ap-check input[data-k="1"]:checked)')
    expect(stylesheet).toContain('counter(ap-left)')
    expect(stylesheet).toMatch(/\.apbox \.ap-check input:not\(:checked\)\s*\{\s*counter-increment:\s*ap-left/)
  })

  it('provides forced-colors fallbacks so state never depends on color alone', () => {
    expect(stylesheet).toMatch(/@media\s*\(forced-colors:\s*active\)\s*\{[\s\S]*?\.badge\s*\{\s*border:\s*1px solid CanvasText/)
    expect(stylesheet).toContain('Highlight')
  })

  it('honours reduced motion globally', () => {
    expect(stylesheet).toMatch(/@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{\s*\*,\s*\*::before,\s*\*::after\s*\{\s*animation:\s*none !important;\s*transition:\s*none !important/)
  })

  it('gives the approval checkbox label a 44px touch target and compact actions 44px height', () => {
    expect(stylesheet).toMatch(/label:has\(input\[type="checkbox"\]\)\s*\{[^}]*min-height:\s*44px[^}]*align-items:\s*center/)
    expect(stylesheet).toMatch(/\.table-action,\s*button\.quiet-button\s*\{[^}]*min-height:\s*44px/)
  })

  it('keeps shared primitives used across pages', () => {
    for (const selector of ['.app-shell', '.topbar', '.sidenav', '.auth-layout', '.auth-card', '.management-columns', '.management-table', '.editor-panel', '.status-chip', '.sr-only', '::selection', 'scrollbar-color', '[role="status"]', '[role="alert"]', ':focus-visible']) {
      expect(stylesheet).toContain(selector)
    }
    expect(stylesheet).not.toMatch(/letter-spacing:\s*-\.(?:0[5-9]|[1-9]\d)em/)
  })
})
