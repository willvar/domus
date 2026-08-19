import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw, Router } from 'vue-router'
import { useAuthStore } from './stores/auth'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/files' },
  { path: '/files', component: () => import('./views/FileShell.vue') },
  { path: '/preview', component: () => import('./views/FilePreview.vue') },
  // Old bookmarks remain harmless, but there is only one product shell now.
  { path: '/desktop', redirect: '/files' },
  { path: '/m/:pathMatch(.*)*', redirect: '/files' },
  { path: '/admin', component: () => import('./views/AdminView.vue'), meta: { requiresRoot: true } },
  { path: '/:pathMatch(.*)*', redirect: '/files' },
]

const router: Router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  if (to.meta.requiresRoot) {
    const auth = useAuthStore()
    await auth.ensureAuthInitialized()
    if (!auth.isRoot) return '/'
  }
})

export default router
