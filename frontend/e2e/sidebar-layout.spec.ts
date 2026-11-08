import { expect, test, type WebSocketRoute } from '@playwright/test'

test('place options keep inset spacing inside selected and hover backgrounds', async ({ page }, testInfo) => {
  await page.routeWebSocket('**/ws', socket => socket.onMessage(message => {
    const request = JSON.parse(String(message))
    if (request.id) socket.send(JSON.stringify({ id: request.id, ok: true, data: {} }))
  }))
  await page.route('**/user', route => route.fulfill({ json: { username: 'layout-fixture', role: 'user' } }))
  await page.route('**/file/?*', route => route.fulfill({ json: { files: [] } }))
  await page.route('**/trash/**', route => route.fulfill({ json: { files: [] } }))
  await page.route('**/task/', route => route.fulfill({ json: [] }))
  await page.route('**/file/upload/cleanup', route => route.fulfill({ json: {} }))
  await page.route('**/audit/', route => route.fulfill({ json: {} }))
  await page.goto('/files')

  const places = page.locator('.place-list')
  const items = places.locator('.n-menu-item-content')
  await expect(items).toHaveCount(2)
  for (const width of [1280, 900, 768]) {
    await page.setViewportSize({ width, height: 800 })
    await expect(places).toBeVisible()
    for (const item of await items.all()) {
      const inset = await item.evaluate(element => {
        const background = getComputedStyle(element, '::before')
        const bounds = element.getBoundingClientRect()
        const icon = element.querySelector('.n-menu-item-content__icon')!.getBoundingClientRect()
        const header = element.querySelector('.n-menu-item-content-header')!
        return {
          left: icon.left - bounds.left - parseFloat(background.left),
          overflowing: header.scrollWidth > header.clientWidth,
        }
      })
      expect(inset.left).toBe(16)
      expect(inset.overflowing).toBeFalsy()
    }
    if (width === 1280) {
      await page.locator('.file-sidebar').screenshot({ path: testInfo.outputPath('places.png') })
    }
  }

  await places.getByRole('menuitem', { name: /回收站|Trash/, exact: true }).click()
  await expect(page).toHaveURL(/place=trash/)
  await expect(items.filter({ hasText: /回收站|Trash/ })).toHaveClass(/selected/)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('.file-sidebar')).toBeHidden()
  await expect(page.locator('.mobile-brand')).toBeVisible()
  await expect(page.getByRole('button', { name: /清空回收站|Empty Trash/, exact: true })).toBeVisible()
})

test('activity reconciles independent worker progress, retains errors and supports retry and cancellation', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('domus_locale','en'))
  let socket: WebSocketRoute | undefined
  await page.routeWebSocket('**/ws', connection => {
    socket = connection
    connection.onMessage(message => {
      const request=JSON.parse(String(message)); if(request.id) connection.send(JSON.stringify({id:request.id,ok:true,data:{}}))
    })
  })
  await page.route('**/user',route=>route.fulfill({json:{username:'task-fixture',role:'user'}}))
  await page.route('**/file/?*',route=>route.fulfill({json:{files:[]}}))
  await page.route('**/file/upload/cleanup',route=>route.fulfill({json:{}}))
  await page.route('**/audit/',route=>route.fulfill({json:{}}))
  let task = { task_id:'job-1', type:'transcode', name:'sample.mp4', profile:'720p', status:'queued', phase:'', progress:0, error:'' }
  let taskReads = 0
  await page.route('**/task/',route=>{ taskReads++; return route.fulfill({json:[task]}) })
  const changed = () => socket!.send(JSON.stringify({ event:'task.update', data:task }))
  let retries=0, cancellations=0
  await page.route('**/task/job-1/retry',route=>{ retries++; task={...task,task_id:'job-2',status:'queued',phase:'',progress:0,error:''}; return route.fulfill({json:{ok:true}}) })
  await page.route('**/task/job-2',route=>{ cancellations++; task={...task,status:'cancelling'}; return route.fulfill({json:{ok:true}}) })
  await page.goto('/files')
  await page.locator('.activity-button').click()
  const activity=page.locator('.activity-center')
  await expect(activity).toContainText('sample.mp4')
  await expect(activity).toContainText('Waiting')
  await page.waitForLoadState('networkidle')
  const initialReads = taskReads
  await page.waitForTimeout(5500)
  expect(taskReads, 'idle task views must not poll').toBe(initialReads)
  task={...task,status:'running',phase:'transcoding',progress:0.35}
  changed()
  await expect(activity).toContainText('35%',{timeout:10_000})
  task={...task,status:'failed',phase:'failed',error:'transcode_probe_failed'}
  changed()
  await expect(activity).toContainText('Cannot inspect this video',{timeout:10_000})
  await activity.getByRole('button',{name:'Retry',exact:true}).click()
  expect(retries).toBe(1)
  await activity.getByRole('button',{name:'Cancel',exact:true}).click()
  expect(cancellations).toBe(1)
  await expect(activity).toContainText('Cancelling')
  task={...task,status:'cancelled'}
  changed()
  await expect(activity).toContainText('Cancelled',{timeout:10_000})
  // A dropped hint is recovered by a single snapshot after WS reconnection.
  const beforeReconnect = taskReads
  task={...task,status:'completed',progress:1}
  socket!.close()
  await expect(activity).toContainText('Completed')
  await page.waitForLoadState('networkidle')
  expect(taskReads).toBe(beforeReconnect + 1)
})
