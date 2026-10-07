// Imported by both app.ts and admin.ts, so Vite splits it into its own chunk
// and records it in the manifest's "imports" array.
export function badge(title: string, detail: string): HTMLElement {
  const element = document.createElement('section')
  element.className = 'badge'
  const heading = document.createElement('h2')
  heading.textContent = title
  const paragraph = document.createElement('p')
  paragraph.textContent = detail
  element.append(heading, paragraph)
  return element
}
