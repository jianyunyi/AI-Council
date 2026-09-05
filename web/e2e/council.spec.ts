import {test, expect} from '@playwright/test'

test('authenticated task setup exposes manual approval workflow', async ({page}) => {
  const login = await page.request.post('/api/v1/auth/login', {
    data: {subject: 'admin', password: 'e2e-admin-password'},
  })
  expect(login.status()).toBe(200)

  await page.goto('/tasks/new')
  await expect(page.getByRole('heading', {name: '新建任务'})).toBeVisible()
  await expect(page.getByRole('button', {name: '开始协作分析'})).toBeVisible()
  await page.getByLabel('需求', {exact: true}).fill('add ready endpoint')
  await page.getByLabel('验收标准', {exact: true}).fill('tests pass')
  await expect(page.getByRole('spinbutton', {name: 'Quorum'})).toHaveValue('2')
  expect((await page.request.get('/api/v1/workspaces')).status()).toBe(200)
})
