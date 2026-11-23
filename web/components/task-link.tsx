'use client'

import Link from 'next/link'
import {useRouter} from 'next/navigation'
import {MouseEvent, useRef} from 'react'
import {navigateWithTransition} from '@/lib/navigate'
import type {Task} from '@/lib/types'

export function taskTitle(task: Task): string {
  return task.requirement.split('\n')[0].trim() || task.id
}

/* Row title link: participates in the list <-> detail view transition
   (§4.4) via the shared `task-title` name, applied only during navigation. */
export function TaskLink({task, className}: {task: Task; className?: string}) {
  const router = useRouter()
  const titleRef = useRef<HTMLSpanElement>(null)
  const href = `/tasks/${encodeURIComponent(task.id)}`
  function onClick(event: MouseEvent) {
    event.preventDefault()
    navigateWithTransition(href2 => router.push(href2), href, {fromEl: titleRef.current, toSelector: '[data-vt="task-title"]'})
  }
  return (
    <Link className={className ?? 'task-link'} href={href} onClick={onClick}>
      <span ref={titleRef} data-vt-row={task.id}>{taskTitle(task)}</span>
    </Link>
  )
}
