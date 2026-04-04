import { createApp } from 'vue'
import type { App } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import AppComponent from './App.vue'
import './styles/global.scss'
import { useServiceWorker } from './composables/useServiceWorker'

const app: App = createApp(AppComponent)
app.use(createPinia())
app.use(router)
app.mount('#app')

// Register Service Worker for client-side file decryption
const sw = useServiceWorker()
sw.register()
