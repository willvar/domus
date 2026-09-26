import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import Icons from 'unplugin-icons/vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig(({ mode }) => {
  const apiTarget = process.env.VITE_API_TARGET || 'http://127.0.0.1:8088'
  const env = loadEnv(mode, __dirname, 'DOMUS_')
  const allowedHosts = (env.DOMUS_DEV_ALLOWED_HOSTS || '').split(',').map(host => host.trim()).filter(Boolean)
  return {
    plugins: [vue(), Icons({ compiler: 'vue3' })],
    build: {
      cssCodeSplit: false,
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, 'src'),
      },
    },
    css: {
      preprocessorOptions: {
        scss: {
          additionalData: `@use "@/styles/index" as *;\n`,
        },
      },
    },
    server: {
      port: 8089,
      allowedHosts,
      proxy: {
        '^/(auth|user|audit|trash|task)(/|$)': apiTarget,
        '^/file($|/)': apiTarget,
        '^/admin/oss': apiTarget,
        '^/ws($|/)': { target: apiTarget, ws: true },
      },
    },
  }
})
