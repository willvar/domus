import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import Icons from 'unplugin-icons/vite'
import path from 'path'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd())
  const ossTarget = env.VITE_OSS_PROXY_TARGET

  return {
    plugins: [vue(), Icons({ compiler: 'vue3' })],
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
          rewrite: (path) => path.replace(/^\/oss-proxy/, ''),
        },
      } : {},
    },
  }
})
