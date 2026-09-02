import {cpSync, existsSync, rmSync} from 'node:fs'
import {spawnSync} from 'node:child_process'
import {dirname, resolve} from 'node:path'
import {fileURLToPath} from 'node:url'

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const next = resolve(webDir, 'node_modules', 'next', 'dist', 'bin', 'next')
const result = spawnSync(process.execPath, [next, 'build'], {
  cwd: webDir,
  env: {...process.env, DESKTOP_BUILD: '1'},
  stdio: 'inherit',
})
if (result.status !== 0) process.exit(result.status ?? 1)
const output = resolve(webDir, 'out')
const destination = resolve(webDir, '..', 'cmd', 'aicouncil-desktop', 'frontend', 'dist')
if (!existsSync(output)) throw new Error('Next.js desktop export was not generated')
rmSync(destination, {recursive: true, force: true})
cpSync(output, destination, {recursive: true})
