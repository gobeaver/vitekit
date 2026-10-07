// A second entry that imports the same shared module as app.ts. When a page
// loads both, vitekit emits the shared chunk once.
import { badge } from './shared'

const root = document.querySelector<HTMLElement>('#admin')
if (root) {
  root.append(badge('Admin', 'A second entry point, sharing a chunk with the first.'))
}
