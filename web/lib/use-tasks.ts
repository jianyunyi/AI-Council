'use client'

import {useCallback, useEffect, useState} from 'react'
import {getTask} from './api'
import {forgetTask, listTaskIds} from './task-index'
import type {Task} from './types'

function isNotFound(error: unknown): boolean {
  const text = JSON.stringify(error ?? '').toLowerCase()
  return text.includes('not_found') || text.includes('not found') || text.includes('404')
}

/* Hydrates the local task index through GET /tasks/:id (the API has no
   list endpoint). Entries the server no longer knows are pruned. */
export function useIndexedTasks() {
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)

  const reload = useCallback(() => {
    const ids = listTaskIds()
    if (!ids.length) {
      setTasks([])
      setLoading(false)
      return
    }
    setLoading(true)
    Promise.allSettled(ids.map(id => getTask(id))).then(results => {
      const found: Task[] = []
      results.forEach((result, index) => {
        if (result.status === 'fulfilled') found.push(result.value)
        else if (isNotFound(result.reason)) forgetTask(ids[index])
      })
      setTasks(found)
      setLoading(false)
    })
  }, [])

  useEffect(() => { reload() }, [reload])
  return {tasks, loading, reload}
}
