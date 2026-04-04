import { createRouter, createWebHashHistory } from 'vue-router'
import type { RouteRecordRaw, Router } from 'vue-router'
import { useAuthStore } from './stores/auth'

const routes: RouteRecordRaw[] = [
  { path: '/', component: () => import('./views/PlasmaShell.vue') },
  { path: '/admin', component: () => import('./views/AdminView.vue'), meta: { requiresRoot: true } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router: Router = createRouter({
  history: createWebHashHistory(),
  routes,
})

router.beforeEach((to) => {
  if (to.meta.requiresRoot) {
    const auth = useAuthStore()
    if (!auth.isRoot) return '/'
  }
})

export default router
