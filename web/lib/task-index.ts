/* The council API has no task-list endpoint (GET /tasks does not exist);
   tasks are addressable only by ID. To let the command center and the task
   list show real tasks, the web client keeps a local index of task IDs it
   has created or opened, and hydrates each one through GET /tasks/:id.
   The index lives in localStorage on this browser/desktop shell only. */

const KEY = 'aicouncil.taskIndex.v1'

type Entry = {id: string; seenAt: number}

function readAll(): Entry[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = window.localStorage.getItem(KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as Entry[]
    return Array.isArray(parsed) ? parsed.filter(entry => typeof entry?.id === 'string') : []
  } catch {
    return []
  }
}

function writeAll(entries: Entry[]) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(KEY, JSON.stringify(entries.slice(0, 200)))
  } catch {
    /* storage full / unavailable: the index is a convenience, not a record */
  }
}

export function rememberTask(id: string) {
  if (!id) return
  const entries = readAll().filter(entry => entry.id !== id)
  entries.unshift({id, seenAt: Date.now()})
  writeAll(entries)
}

export function forgetTask(id: string) {
  writeAll(readAll().filter(entry => entry.id !== id))
}

export function listTaskIds(): string[] {
  return readAll().map(entry => entry.id)
}
