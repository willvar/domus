import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw, Router } from 'vue-router'
import { useAuthStore } from './stores/auth'

const routes: RouteRecordRaw[] = [
  { path: '/', component: () => import('./views/PlatformEntry.vue') },
  { path: '/desktop', component: () => import('./views/PlasmaShell.vue') },
  {
    path: '/m',
    component: () => import('./views/MobileShell.vue'),
    children: [
      { path: '', redirect: '/m/files' },
      { path: 'files', component: () => import('./views/mobile/FilesView.vue') },
      { path: 'recent', component: () => import('./views/mobile/RecentView.vue') },
      { path: 'shared', component: () => import('./views/mobile/SharedView.vue') },
      { path: 'me', component: () => import('./views/mobile/ProfileView.vue') },
      { path: 'preferences', component: () => import('./views/mobile/PreferencesView.vue') },
      { path: 'security', component: () => import('./views/mobile/SecurityView.vue') },
      { path: 'search', component: () => import('./views/mobile/SearchView.vue') },
      { path: 'preview', component: () => import('./views/mobile/PreviewView.vue') },
      { path: 'details', component: () => import('./views/mobile/FileDetailView.vue') },
      { path: 'activity', component: () => import('./views/mobile/ActivityView.vue') },
      { path: 'path-picker', component: () => import('./views/mobile/PathPickerView.vue') },
      { path: 'terminal', component: () => import('./views/mobile/TerminalView.vue') },
    ],
  },
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
