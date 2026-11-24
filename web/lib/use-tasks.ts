'use client'

import {useCallback, useEffect, useState} from 'react'
import {listTasks} from './api'
import type {TaskSummary} from './types'

/* The pre-list-endpoint client kept a localStorage index of task IDs
   (aicouncil.taskIndex.v1) and hydrated each task by ID. The server is now
   the source of truth: delete the legacy key once, without importing it. */
const LEGACY_INDEX_KEY = 'aicouncil.taskIndex.v1'
let legacyIndexCleaned = false

export function cleanupLegacyTaskIndex() {
  if (legacyIndexCleaned || typeof window === 'undefined') return
  legacyIndexCleaned = true
  try { window.localStorage.removeItem(LEGACY_INDEX_KEY) } catch { /* storage unavailable */ }
}

const MAX_LISTED = 1000

/* Loads the caller's accessible tasks from GET /tasks, walking the keyset
   cursor so list views are not limited to a single page. */
export function useTasks() {
  const [tasks, setTasks] = useState<TaskSummary[]>([])
  const [loading, setLoading] = useState(true)

  const reload = useCallback(() => {
    cleanupLegacyTaskIndex()
    setLoading(true)
    const collected: TaskSummary[] = []
    const walk = (cursor?: string): Promise<void> =>
      listTasks({limit: 200, cursor}).then(page => {
        collected.push(...page.items)
        if (page.next_cursor && collected.length < MAX_LISTED) return walk(page.next_cursor)
        return undefined
      })
    walk()
      .then(() => { setTasks(collected); setLoading(false) })
      .catch(() => { setTasks([]); setLoading(false) })
  }, [])

  useEffect(() => { reload() }, [reload])
  return {tasks, loading, reload}
}
