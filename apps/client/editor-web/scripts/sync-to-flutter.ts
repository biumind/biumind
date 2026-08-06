// Copy `dist/` into the two locations Flutter consumes:
//   * client/web/editor/    — served as part of the Flutter Web bundle, used
//                             by HtmlElementView + iframe (same origin).
//   * client/assets/editor/ — bundled into the native app, served by
//                             InAppLocalhostServer at runtime.
//
// Both targets are .gitignored — they regenerate from `npm run build`.

import { cp, mkdir, rm, stat } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const editorWebRoot = resolve(here, '..')
const dist = resolve(editorWebRoot, 'dist')
const clientRoot = resolve(editorWebRoot, '..')

const targets = [
  resolve(clientRoot, 'web', 'editor'),
  resolve(clientRoot, 'assets', 'editor'),
]

async function exists(path: string): Promise<boolean> {
  try {
    await stat(path)
    return true
  } catch {
    return false
  }
}

async function main(): Promise<void> {
  if (!(await exists(dist))) {
    console.error(`[sync-to-flutter] dist/ not found at ${dist}. Run \`npm run build:bundle\` first.`)
    process.exit(1)
  }
  for (const target of targets) {
    if (await exists(target)) await rm(target, { recursive: true, force: true })
    await mkdir(dirname(target), { recursive: true })
    await cp(dist, target, { recursive: true })
    console.log(`[sync-to-flutter] copied dist/ → ${target}`)
  }
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
