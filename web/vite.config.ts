import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

// Element Plus components are registered on demand; the imperative APIs and
// directives used in scripts are imported explicitly in main.ts.
export default defineConfig(({ mode }) => ({
  // Component tests stub Element Plus, so the on-demand resolver (which also
  // injects theme CSS Node cannot load) is only active outside vitest.
  plugins: [
    vue(),
    ...(mode === 'test' ? [] : [Components({ resolvers: [ElementPlusResolver()], dts: 'src/components.d.ts' })]),
  ],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: {
    proxy: { '/api': { target: process.env.VITE_API_TARGET || 'http://127.0.0.1:8080', changeOrigin: true } },
  },
  test: {
    environment: 'happy-dom',
    include: ['tests/**/*.test.{ts,mjs}'],
    globals: false,
  },
  build: {
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: (id) => {
          if (id.includes('node_modules/element-plus') || id.includes('node_modules/@element-plus'))
            return 'element-plus'
          if (id.includes('node_modules/lucide-vue-next')) return 'icons'
          if (id.includes('node_modules/vue') || id.includes('node_modules/@vue')) return 'vue'
        },
      },
    },
  },
}))
