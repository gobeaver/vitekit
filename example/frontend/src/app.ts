// A TypeScript entry with no framework, sharing a module with the admin entry.
// vitekit is only reading Vite's output, so the language and framework are
// Vite's business, not the Go server's.
import { badge } from './shared'

const root = document.querySelector<HTMLElement>('#app')
if (root) {
  root.append(badge('Go + Vite', 'This page was rendered by Go and hydrated by a TypeScript entry.'))
}
