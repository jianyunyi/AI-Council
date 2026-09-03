# Council UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Present AI Council as a trustworthy multi-agent development command center with an accessible animated 3D deliberation scene.

**Architecture:** Keep the existing Next.js routes and API calls intact. Add an isolated client-side `CouncilOrbit` visual component to the home page, then replace the global CSS with a token-based graphite and teal visual system that styles all existing screens. CSS owns all motion so the desktop static export needs no new runtime dependency.

**Tech Stack:** Next.js 14, React 18, TypeScript, native CSS animations, Vitest, Testing Library.

---

### Task 1: Build the semantic 3D deliberation visual

**Files:**
- Create: `web/components/council-orbit.tsx`
- Create: `web/components/council-orbit.test.tsx`

- [ ] **Step 1: Write the failing component test**

```tsx
// @vitest-environment jsdom
import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {CouncilOrbit} from './council-orbit'

describe('CouncilOrbit', () => {
  it('exposes the multi-agent approval flow to assistive technology', () => {
    render(<CouncilOrbit />)
    expect(screen.getByLabelText('多智能体协商流程')).toBeTruthy()
    expect(screen.getByText('需求')).toBeTruthy()
    expect(screen.getByText('人工批准')).toBeTruthy()
    expect(screen.getByText('受控执行')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `node web/node_modules/vitest/vitest.mjs run web/components/council-orbit.test.tsx --config web/vitest.config.ts`

Expected: FAIL because `./council-orbit` does not exist.

- [ ] **Step 3: Implement the isolated visual component**

```tsx
'use client'

const seats = ['提案模型', '审查模型', '裁判模型']

export function CouncilOrbit() {
  return <section className="council-orbit" aria-label="多智能体协商流程">
    <div className="council-orbit__core">需求</div>
    <div className="council-orbit__ring" aria-hidden="true" />
    {seats.map((seat, index) => <span className={`council-orbit__seat council-orbit__seat--${index + 1}`} key={seat}>{seat}</span>)}
    <ol className="council-orbit__steps">
      <li>独立提案</li><li>匿名互审</li><li>人工批准</li><li>受控执行</li>
    </ol>
  </section>
}
```

- [ ] **Step 4: Run the component test to verify it passes**

Run: `node web/node_modules/vitest/vitest.mjs run web/components/council-orbit.test.tsx --config web/vitest.config.ts`

Expected: PASS with one test.

- [ ] **Step 5: Commit the visual component**

```bash
git add web/components/council-orbit.tsx web/components/council-orbit.test.tsx
git commit -m "feat: add animated council orbit visual"
```

### Task 2: Recompose the home page without changing product controls

**Files:**
- Modify: `web/app/page.tsx`
- Create: `web/app/page.test.tsx`
- Modify: `web/app/layout.tsx`

- [ ] **Step 1: Write the failing home-page test**

```tsx
// @vitest-environment jsdom
import {render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'

vi.mock('@/lib/desktop-bridge', () => ({desktopBridge: () => undefined}))
import Home from './page'

describe('Home', () => {
  it('shows the command-center promise and primary workflow links', () => {
    render(<Home />)
    expect(screen.getByRole('heading', {name: '让多模型达成可执行共识'})).toBeTruthy()
    expect(screen.getByRole('link', {name: '创建协商任务'}).getAttribute('href')).toBe('/tasks/new')
    expect(screen.getByRole('link', {name: '选择工作区'}).getAttribute('href')).toBe('/workspaces')
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `node web/node_modules/vitest/vitest.mjs run web/app/page.test.tsx --config web/vitest.config.ts`

Expected: FAIL because the redesigned headline and links are absent.

- [ ] **Step 3: Implement the asymmetric command-center home page**

Use `CouncilOrbit` in a `hero-grid`. Keep the existing desktop `Start`, `Stop`, `Status`, and diagnostic actions untouched, but place them in a `service-panel` after the hero. Render links to `/tasks/new` and `/workspaces` with the tested labels. Keep every interactive call behind the existing `desktopBridge()` guard and retain inline error feedback.

- [ ] **Step 4: Update the root layout semantics**

Keep the existing three route destinations and labels. Add a small static product descriptor beside the wordmark and set the main landmark class to `app-shell__main`; do not change route paths or link text.

- [ ] **Step 5: Run the page test to verify it passes**

Run: `node web/node_modules/vitest/vitest.mjs run web/app/page.test.tsx --config web/vitest.config.ts`

Expected: PASS with one test.

- [ ] **Step 6: Commit the page composition**

```bash
git add web/app/page.tsx web/app/page.test.tsx web/app/layout.tsx
git commit -m "feat: redesign council command center"
```

### Task 3: Apply the responsive fluid visual system

**Files:**
- Modify: `web/app/globals.css`

- [ ] **Step 1: Define the token system and page primitives**

Add graphite background, off-white text, and one teal accent through CSS custom properties. Style `app-shell`, `hero-grid`, `service-panel`, forms, buttons, status text, and focus-visible outlines. Keep one 16px card radius and preserve high-contrast input and button states.

- [ ] **Step 2: Implement only motivated motion**

Add `@keyframes` for the orbit ring, flowing beam, and background current. Apply transforms only to the `council-orbit` visual. Add a `@media (prefers-reduced-motion: reduce)` override that disables every animation and transform transition.

- [ ] **Step 3: Add explicit responsive layout rules**

At widths below 800px, collapse `hero-grid` to one column, constrain the 3D visual height, retain a 44px minimum control size, and prevent horizontal overflow. Do not use `100vh`.

- [ ] **Step 4: Build the desktop frontend to validate CSS, TypeScript, and static export**

Run: `node web/scripts/build-desktop.mjs`

Expected: Next.js completes static generation for `/`, `/providers`, `/workspaces`, `/tasks/new`, `/tasks/view`, and the desktop task fallback.

- [ ] **Step 5: Restore the tracked embed placeholder after build output**

Replace only `cmd/aicouncil-desktop/frontend/dist/index.html` with its committed placeholder text. Keep generated `_next` assets ignored.

- [ ] **Step 6: Commit the visual system**

```bash
git add web/app/globals.css
git commit -m "feat: add fluid command-center visual system"
```

### Task 4: Verify the complete redesign

**Files:**
- Verify: `web/components/council-orbit.test.tsx`
- Verify: `web/app/page.test.tsx`
- Verify: `web/lib/*.test.ts`

- [ ] **Step 1: Run the complete web test suite**

Run: `node web/node_modules/vitest/vitest.mjs run --config web/vitest.config.ts`

Expected: PASS with zero failing test files.

- [ ] **Step 2: Run the complete Go test suite**

Run: `$env:GOCACHE='F:\AI-council\.tmp-go-cache'; go test ./...`

Expected: PASS with zero failing packages.

- [ ] **Step 3: Inspect the final diff**

Run: `git diff --check; git status --short`

Expected: no whitespace errors and only intended source, test, and docs changes.

- [ ] **Step 4: Commit final verification-related files if necessary**

```bash
git add web
git commit -m "test: cover council command-center UI"
```
