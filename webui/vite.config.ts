import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

// 开发态：Vite dev server 代理 API/WS 到本地 cloudpath-server（:8080）
// 生产态：vite build → dist/ → go:embed 进 server 单二进制（-tags embed_ui）

// 供应商分包（函数式，按真实路径判定）：
// 关键点——react-is / scheduler 等「React 生态小依赖」同时被 recharts 使用，
// 若用对象式 manualChunks（charts: ['recharts']），Rollup 会把这些共享模块塞进 charts chunk，
// 于是入口 chunk 静态 import charts → index.html 出现 charts 的 modulepreload，
// 390KB 图表库被拖进首屏，路由级懒加载白做。故必须显式把它们钉在 react chunk。
function vendorChunk(id: string): string | undefined {
  if (!id.includes('node_modules')) return undefined
  const at = (re: RegExp) => re.test(id)
  if (at(/[\\/]node_modules[\\/](react|react-dom|react-is|react-router|scheduler|use-sync-external-store)[\\/]/)) return 'react'
  if (at(/[\\/]node_modules[\\/]@tanstack[\\/]/)) return 'query'
  if (at(/[\\/]node_modules[\\/](recharts|react-smooth|victory-vendor|reselect|decimal\.js-light|d3-[^\\/]+)[\\/]/)) return 'charts'
  return undefined
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/healthz': 'http://127.0.0.1:8080',
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: vendorChunk,
      },
    },
  },
})
