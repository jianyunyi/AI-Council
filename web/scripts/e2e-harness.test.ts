import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {describe,expect,it} from 'vitest'

describe('Playwright production harness',()=>{
 it('starts an isolated RBAC Council service and proxies it through the web origin',()=>{
  const harness=readFileSync(resolve(__dirname,'..','e2e','server.mjs'),'utf8')
  const nextConfig=readFileSync(resolve(__dirname,'..','next.config.mjs'),'utf8')
  const playwright=readFileSync(resolve(__dirname,'..','playwright.config.ts'),'utf8')
  expect(harness).toContain('mkdtempSync')
  expect(harness).toContain('--rbac-bootstrap-subject')
  expect(harness).toContain('AUTH_COOKIE_SECURE')
  expect(harness).toContain('COUNCIL_BOOTSTRAP_PASSWORD')
  expect(nextConfig).toContain('E2E_API_ORIGIN')
  expect(playwright).toContain('node e2e/server.mjs')
  expect(playwright).toContain('reuseExistingServer:false')
 })
})
