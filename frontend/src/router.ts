import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw, Router } from 'vue-router'
import { useAuthStore } from './stores/auth'

const routes: RouteRecordRaw[] = [
  { path: '/', component: () => import('./views/PlasmaShell.vue') },
  { path: '/admin', component: () => import('./views/AdminView.vue'), meta: { requiresRoot: true } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
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
