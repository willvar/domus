import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import App from './App.vue'
import './assets/theme.css'
import './assets/plasma-menu.css'
import { useServiceWorker } from './composables/useServiceWorker'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')

// Register Service Worker for client-side file decryption
const sw = useServiceWorker()
sw.register()
