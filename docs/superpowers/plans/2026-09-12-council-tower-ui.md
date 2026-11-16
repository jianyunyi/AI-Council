# Council Tower UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the approved Council Tower UI with an accessible in-page Remotion demonstration and a coherent operational visual system.

**Architecture:** Preserve every REST, SSE, RBAC and desktop-bridge behavior. A client-only `CouncilTower` wraps a fixed Remotion sample composition plus semantic static fallback; global CSS provides the visual system while routes receive layout wrappers only.

**Tech Stack:** Next.js 14, React 18, TypeScript, CSS, Vitest, Playwright, `@remotion/player@4.0.523`.

---

## File map

- Create `web/components/council-tower.tsx`, `web/components/council-tower.test.tsx`, `web/components/remotion/deliberation-composition.tsx`.
- Modify `web/package.json`, `web/pnpm-lock.yaml`, `web/app/page.tsx`, `web/app/page.test.tsx`, `web/app/globals.css`, `web/app/globals.test.ts`, `web/components/app-shell.tsx`, `web/components/app-shell.test.tsx`.
- Modify `web/app/tasks/new/page.tsx`, `web/app/workspaces/page.tsx`, `web/app/providers/page.tsx`, `web/components/task/task-workbench.tsx`, `web/app/tasks/new/page.test.tsx`.

### Task 1: Add the Remotion presentation boundary

**Files:** Create `web/components/remotion/deliberation-composition.tsx`, `web/components/council-tower.tsx`, `web/components/council-tower.test.tsx`; modify `web/package.json`, `web/pnpm-lock.yaml`.

- [ ] **Step 1: Write a failing component test.**

```tsx
render(<CouncilTower />)
expect(screen.getByLabelText('协商钟塔演示')).toBeTruthy()
expect(screen.getByText('人工批准是执行的前提')).toBeTruthy()
```

- [ ] **Step 2: Confirm it fails.** Run `pnpm --dir web test -- council-tower.test.tsx`; expected: module absent.

- [ ] **Step 3: Add the exact Player dependency.** Run `pnpm --dir web add --save-exact @remotion/player@4.0.523`; confirm `"@remotion/player": "4.0.523"`.

- [ ] **Step 4: Implement the fixed, no-audio composition.**

```tsx
export const deliberationStages = ['独立提案', '匿名互审', '裁判决策', '人工批准', '受控执行'] as const
export function DeliberationComposition() { const active = Math.min(Math.floor(useCurrentFrame() / 72), 4); return <div className="deliberation-composition" aria-hidden="true" data-active-stage={active} /> }
```

Use only adjacent-stage opacity/transform interpolation; do not fetch data, render audio, use WebGL or claim that this sample is live task state.

- [ ] **Step 5: Implement `CouncilTower`.**

```tsx
<section className="council-tower" aria-label="协商钟塔演示"><div className="council-tower__fallback">{/* static labelled layers */}</div><Player component={DeliberationComposition} durationInFrames={360} compositionWidth={960} compositionHeight={720} fps={30} loop autoPlay muted controls={false} aria-hidden="true" /><aside className="council-tower__evidence" aria-label="协商证据轨">{/* stages */}</aside><p className="sr-only">人工批准是执行的前提。</p></section>
```

Guard Player with client-mounted state. The fallback is semantic and visible under reduced motion.

- [ ] **Step 6: Verify and commit.** Run `pnpm --dir web test -- council-tower.test.tsx` (PASS), then commit `feat(web): add council tower deliberation demo` with the five changed files.

### Task 2: Replace homepage composition without changing service controls

**Files:** Modify `web/app/page.tsx`, `web/app/page.test.tsx`; remove `web/components/council-orbit.tsx` and test only when no import remains.

- [ ] **Step 1: Extend homepage test.**

```tsx
expect(screen.getByLabelText('协商钟塔演示')).toBeTruthy()
expect(screen.getByRole('link',{name:'创建协商任务'}).getAttribute('href')).toBe('/tasks/new')
expect(screen.getByRole('link',{name:'选择工作区'}).getAttribute('href')).toBe('/workspaces')
```

- [ ] **Step 2: Confirm failure.** Run `pnpm --dir web test -- app/page.test.tsx`; expected: tower missing.

- [ ] **Step 3: Implement the approved three-region skeleton.**

```tsx
<section className="tower-hero" aria-labelledby="home-title"><div className="tower-hero__command">{/* existing title, copy, links */}</div><CouncilTower /><ol className="tower-hero__rail" aria-label="协商阶段">{/* five stages */}</ol></section>
```

Keep `refresh`, `toggle`, `exportDiagnostics`, labels and service panel behavior verbatim.

- [ ] **Step 4: Verify and commit.** Run `pnpm --dir web test -- app/page.test.tsx components/council-tower.test.tsx` (PASS), then commit `feat(web): use council tower on home`.

### Task 3: Establish the global Council Tower system

**Files:** Modify `web/app/globals.css`, `web/app/globals.test.ts`, `web/components/app-shell.tsx`, `web/components/app-shell.test.tsx`.

- [ ] **Step 1: Add failing system assertions.**

```ts
for (const token of ['--tower-ink','--tower-clay','--tower-bone','--tower-mineral']) expect(stylesheet).toContain(token)
for (const selector of ['.tower-hero','.council-tower','.council-tower__evidence','.operation-band']) expect(stylesheet).toContain(selector)
```

- [ ] **Step 2: Confirm failure.** Run `pnpm --dir web test -- app/globals.test.ts`; expected: tokens absent.

- [ ] **Step 3: Implement tokens and motion fallback.**

```css
:root { --tower-ink:#1b2731; --tower-clay:#c76f38; --tower-bone:#e7d6b0; --tower-mineral:#83a7a1; }
.operation-band { border-inline-start:3px solid var(--tower-clay); }
@media (prefers-reduced-motion: reduce) { .council-tower__player { display:none; } .council-tower__fallback { display:grid; } }
```

Retain 44px targets, focus, status roles, mobile overflow safety and `100dvh`. Use low-radius planes and stage bands, not nested rounded cards.

- [ ] **Step 4: Change header semantics only.** Set nav `aria-label` to `控制台索引`; retain every href and `canManage` condition; extend existing permission regression test.

- [ ] **Step 5: Verify and commit.** Run `pnpm --dir web test -- app/globals.test.ts components/app-shell.test.tsx` (PASS), then commit `feat(web): establish council tower design system`.

### Task 4: Apply operating layer to real workflows

**Files:** Modify `web/app/tasks/new/page.tsx`, `web/app/workspaces/page.tsx`, `web/app/providers/page.tsx`, `web/components/task/task-workbench.tsx`, `web/app/tasks/new/page.test.tsx`.

- [ ] **Step 1: Add failing form wrapper test.**

```tsx
expect(document.querySelector('.operation-page')).toBeTruthy()
expect(screen.getByRole('heading',{name:'新建任务'})).toBeTruthy()
```

- [ ] **Step 2: Confirm failure.** Run `pnpm --dir web test -- app/tasks/new/page.test.tsx`; expected: wrapper absent.

- [ ] **Step 3: Add semantic shells only.**

```tsx
<section className="operation-page operation-page--task-create"><header className="operation-band"><p>01 / 协商输入</p><h1>新建任务</h1></header><form className="operation-plane" onSubmit={submit}>{/* existing fields in identical order */}</form></section>
```

Use matching `operation-page`, `operation-band`, `operation-plane` wrappers for Workspaces, Providers and TaskWorkbench. Preserve callbacks, SSE subscription, labels, errors, status roles and input order.

- [ ] **Step 4: Verify and commit.** Run `pnpm --dir web test -- app/tasks/new/page.test.tsx` (PASS), then commit `feat(web): apply tower system to workflows`.

### Task 5: Validate and finish

**Files:** Create uncommitted `.impeccable/review/desktop.png`, `.impeccable/review/mobile.png`; create `DESIGN.md` only after final review.

- [ ] **Step 1: Run functional checks.**

```powershell
pnpm --dir web test
pnpm --dir web build
pnpm --dir web e2e
```

Expected: Vitest and build PASS. If the known local Windows Chromium error recurs, preserve the failure and rely on CI/Linux; do not weaken E2E.

- [ ] **Step 2: Capture 1440px and 390px.** Inspect correct route, loaded regions, no horizontal overflow and readable static stage list under reduced motion.

- [ ] **Step 3: Run the required detector once.** Run `node C:\Users\asus\.codex\skills\impeccable\scripts\detect.mjs --json web/app/page.tsx web/app/globals.css web/components/council-tower.tsx web/components/app-shell.tsx`; batch-fix mechanical findings without rerun.

- [ ] **Step 4: Obtain fresh finish review.** Provide user request, approved comp, surface brief, `PRODUCT.md`, screenshots, detector output and changed files; follow `recapture`, `rebuild`, `fix` or `ship` exactly.

- [ ] **Step 5: Document and final-check.** After `ship`, run the Impeccable documenter, then run `pnpm --dir web test`, `pnpm --dir web build`, and `git diff --check`. Commit source, lock, specs, plan, product and design docs; do not commit decision mocks unless user asks.

## Self-review

- Spec coverage: Tasks 1–2 implement static-accessible Remotion homepage, Task 3 sets global system, Task 4 protects workflows, Task 5 performs responsive, functional and design completion.
- Placeholder scan: every change lists files, test condition and command; Windows Playwright limitation is explicitly bounded.
- Type consistency: `CouncilTower`, `DeliberationComposition`, `operation-page`, `operation-band` and `operation-plane` are named consistently.
