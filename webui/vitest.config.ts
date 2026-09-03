// 测试运行配置（Vitest）。与 vite.config.ts 刻意分离：
// 生产构建配置（vite build / tsc --noEmit 的对象）不应引入任何测试期依赖，
// 测试环境也不需要 Tailwind 插件（组件不 import CSS，css: false）。
import { fileURLToPath, URL } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    css: false,
    clearMocks: true,
    restoreMocks: true,
  },
})