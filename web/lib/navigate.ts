/* Same-document view transitions for list <-> detail navigation
   (DESIGN v2 §4.4, MWG: same-document-transitions). The shared title
   element is named only for the duration of the transition; without API
   support or with reduced motion we navigate synchronously. */

type Push = (href: string) => void

function waitForElement(selector: string, timeoutMs: number): Promise<HTMLElement | null> {
  return new Promise(resolve => {
    const existing = document.querySelector<HTMLElement>(selector)
    if (existing) return resolve(existing)
    const observer = new MutationObserver(() => {
      const found = document.querySelector<HTMLElement>(selector)
      if (found) {
        observer.disconnect()
        resolve(found)
      }
    })
    observer.observe(document.body, {childList: true, subtree: true})
    window.setTimeout(() => {
      observer.disconnect()
      resolve(document.querySelector<HTMLElement>(selector))
    }, timeoutMs)
  })
}

export function navigateWithTransition(push: Push, href: string, options?: {fromEl?: HTMLElement | null; toSelector?: string}) {
  const doc = document as Document & {startViewTransition?: (update: () => Promise<void> | void) => {finished: Promise<void>}}
  const reduce = typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (!doc.startViewTransition || reduce) {
    push(href)
    return
  }
  const from = options?.fromEl ?? null
  if (from) from.style.viewTransitionName = 'task-title'
  let to: HTMLElement | null = null
  const transition = doc.startViewTransition(async () => {
    if (from) from.style.viewTransitionName = ''
    push(href)
    if (options?.toSelector) {
      to = await waitForElement(options.toSelector, 400)
      if (to) to.style.viewTransitionName = 'task-title'
    }
  })
  transition.finished.finally(() => {
    if (from) from.style.viewTransitionName = ''
    if (to) to.style.viewTransitionName = ''
  }).catch(() => {})
}
