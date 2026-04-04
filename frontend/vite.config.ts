import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import Icons from 'unplugin-icons/vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd())
  const ossTarget = env.VITE_OSS_PROXY_TARGET

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
      port: 5173,
      proxy: ossTarget ? {
        '/oss-proxy': {
          target: ossTarget,
          changeOrigin: true,
          rewrite: (p: string) => p.replace(/^\/oss-proxy/, ''),
        },
      } : undefined,
    },
  }
})
