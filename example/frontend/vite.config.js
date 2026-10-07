import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  build: {
    // vitekit reads .vite/manifest.json to resolve hashed filenames, CSS
    // chains and code-split chunks. Without this there is no manifest to read.
    manifest: true,
    outDir: 'dist',
    rollupOptions: {
      // Several entries, so the examples can show a page pulling in more than
      // one and vitekit deduplicating whatever they share.
      input: {
        main: 'src/main.jsx',
        app: 'src/app.ts',
        admin: 'src/admin.ts',
        theme: 'src/theme.css',
      },
    },
  },
  server: {
    // The Go server owns the HTML document and loads modules from Vite's
    // origin, so those requests are cross-origin and need CORS.
    cors: true,
  },
  // Fast Refresh must stay on: the Go template renders {{ viteReactPreamble }},
  // which imports /@react-refresh from the dev server.
  // The Go orchestrator writes and removes .vite/hot itself, so no hot-file
  // plugin is needed here.
  plugins: [react()],
})
