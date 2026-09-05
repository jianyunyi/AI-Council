import {randomUUID} from 'node:crypto'
import {test, expect, type Page} from '@playwright/test'

const admin = {subject: 'admin', password: 'e2e-admin-password'}
const fixturePassword = 'e2e-reader-password'
const sessionCookieName = 'aicouncil_session'

async function signIn(page: Page, subject: string, password: string) {
  await page.goto('/login')
  await page.getByLabel('账号', {exact: true}).fill(subject)
  await page.getByLabel('密码', {exact: true}).fill(password)
  await page.getByRole('button', {name: '登录', exact: true}).click()
  await expect(page).toHaveURL(/\/account\/?$/)
}

test('administrator session persists without exposing its token to browser scripts', async ({page, context}) => {
  await signIn(page, admin.subject, admin.password)
  const cookie = (await context.cookies()).find(value => value.name === sessionCookieName)
  expect(cookie).toBeDefined()
  expect(cookie?.httpOnly).toBe(true)
  expect(cookie?.sameSite).toBe('Strict')

  await page.reload()
  await expect(page.getByRole('link', {name: '用户管理', exact: true})).toBeVisible()
  const me = await page.request.get('/api/v1/auth/me')
  expect(me.status()).toBe(200)
  const identity = (await me.json()).data
  expect(identity.subject).toBe(admin.subject)
  expect(identity.expires_at).toBeTruthy()
  expect(identity.permissions).toContain('admin:users')

  const browserState = await page.evaluate(() => ({
    cookies: document.cookie,
    local: {...localStorage},
    session: {...sessionStorage},
  }))
  expect(browserState.cookies).not.toContain(sessionCookieName)
  expect(JSON.stringify(browserState)).not.toContain(cookie!.value)
  expect(JSON.stringify(browserState)).not.toContain(admin.password)
  expect(Object.keys({...browserState.local, ...browserState.session}).join(' ')).not.toMatch(/token|aicouncil_session/i)
})

test('administrator creates a reader role and user, then enforces disablement and permissions', async ({page, browser}) => {
  const suffix = randomUUID().slice(0, 8)
  const roleName = `reader-${suffix}`
  const subject = `reader-user-${suffix}`
  await signIn(page, admin.subject, admin.password)
  await page.getByRole('link', {name: '用户管理', exact: true}).click()
  await expect(page).toHaveURL(/\/admin\/users\/?$/)

  await page.getByLabel('角色名称', {exact: true}).fill(roleName)
  await page.getByRole('checkbox', {name: 'task:read', exact: true}).check()
  await page.getByRole('checkbox', {name: 'workspace:read', exact: true}).check()
  const roleCreated = page.waitForResponse(response => response.url().endsWith('/api/v1/admin/roles') && response.request().method() === 'POST')
  await page.getByRole('button', {name: '创建角色', exact: true}).click()
  expect((await roleCreated).status()).toBe(201)
  await expect(page.getByText(roleName, {exact: true}).first()).toBeVisible()

  await page.getByLabel('账号', {exact: true}).fill(subject)
  await page.getByLabel('初始密码', {exact: true}).fill(fixturePassword)
  await page.getByRole('checkbox', {name: `分配初始角色 ${roleName}`, exact: true}).check()
  const userCreated = page.waitForResponse(response => response.url().endsWith('/api/v1/admin/users') && response.request().method() === 'POST')
  await page.getByRole('button', {name: '创建用户', exact: true}).click()
  expect((await userCreated).status()).toBe(201)
  await expect(page.getByRole('row').filter({hasText: subject})).toBeVisible()
  await expect(page.getByLabel('初始密码', {exact: true})).toHaveValue('')

  const userPath = `/api/v1/admin/users/${encodeURIComponent(subject)}`
  const disabled = await page.request.patch(userPath, {data: {disabled: true}})
  expect(disabled.status()).toBe(200)
  expect((await disabled.json()).data).toMatchObject({subject, disabled: true, roles: [roleName]})

  // Keep the administrator's cookie in its own context for the enable operation.
  const readerContext = await browser.newContext({baseURL: test.info().project.use.baseURL})
  try {
    const readerPage = await readerContext.newPage()
    await readerPage.goto('/login')
    await readerPage.getByLabel('账号', {exact: true}).fill(subject)
    await readerPage.getByLabel('密码', {exact: true}).fill(fixturePassword)
    const rejected = readerPage.waitForResponse(response => response.url().endsWith('/api/v1/auth/login') && response.request().method() === 'POST')
    await readerPage.getByRole('button', {name: '登录', exact: true}).click()
    expect((await rejected).status()).toBe(401)
    await expect(readerPage.getByRole('alert').filter({hasText: 'invalid credentials'})).toBeVisible()
    expect((await readerContext.cookies()).some(cookie => cookie.name === sessionCookieName)).toBe(false)

    expect((await page.request.patch(userPath, {data: {disabled: false}})).status()).toBe(200)
    await signIn(readerPage, subject, fixturePassword)
    const me = await readerPage.request.get('/api/v1/auth/me')
    expect(me.status()).toBe(200)
    expect((await me.json()).data).toMatchObject({subject, roles: [roleName], permissions: ['task:read', 'workspace:read']})
    await expect(readerPage.getByRole('link', {name: '用户管理', exact: true})).toHaveCount(0)
    expect((await readerPage.request.get('/api/v1/workspaces')).status()).toBe(200)
    expect((await readerPage.request.get('/api/v1/admin/users')).status()).toBe(403)
    expect((await readerPage.request.get('/api/v1/admin/roles')).status()).toBe(403)
    expect((await readerPage.request.post('/api/v1/tasks', {data: {}})).status()).toBe(403)

    await readerPage.goto('/admin/users')
    await expect(readerPage.getByRole('button', {name: '创建用户', exact: true})).toHaveCount(0)
    await expect(readerPage.getByRole('button', {name: '创建角色', exact: true})).toHaveCount(0)
    await expect(readerPage.getByRole('alert').first()).toBeVisible()
  } finally {
    await readerContext.close()
  }
})

test('logout clears the browser session and revokes the old cookie', async ({page, context, playwright, baseURL}) => {
  await signIn(page, admin.subject, admin.password)
  const cookie = (await context.cookies()).find(value => value.name === sessionCookieName)
  expect(cookie).toBeDefined()
  const loggedOut = page.waitForResponse(response => response.url().endsWith('/api/v1/auth/logout') && response.request().method() === 'POST')
  await page.getByRole('button', {name: '退出登录', exact: true}).click()
  expect((await loggedOut).status()).toBe(204)
  await expect(page.getByRole('link', {name: '用户管理', exact: true})).toHaveCount(0)
  expect((await context.cookies()).some(value => value.name === sessionCookieName)).toBe(false)
  expect((await page.request.get('/api/v1/auth/me')).status()).toBe(401)

  const replay = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: {Cookie: `${sessionCookieName}=${cookie!.value}`},
  })
  try {
    expect((await replay.get('/api/v1/auth/me')).status()).toBe(401)
  } finally {
    await replay.dispose()
  }
})
