import {defineConfig} from 'vitest/config'
import {fileURLToPath,URL} from 'node:url'

export default defineConfig({
  root:fileURLToPath(new URL('./',import.meta.url)),
  esbuild:{jsx:'automatic'},
  resolve:{alias:{'@':fileURLToPath(new URL('./',import.meta.url))}},
  test: { include:['**/*.{test,spec}.{ts,tsx}'],exclude: ['e2e/**', '**/node_modules/**'] },
})
