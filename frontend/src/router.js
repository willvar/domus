import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuthStore } from './stores/auth'

const routes = [
  { path: '/', component: () => import('./views/PlasmaShell.vue') },
  { path: '/admin', component: () => import('./views/AdminView.vue'), meta: { requiresRoot: true } },
  { path: '/s/:shareId', component: () => import('./views/ShareView.vue'), meta: { public: true } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
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
