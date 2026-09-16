import { gzipSync } from 'node:zlib'
import { readdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'

const limits = { '.js': 90 * 1024, '.css': 30 * 1024 }
const assets = join(import.meta.dirname, '..', 'dist', 'assets')
const files = await readdir(assets)

for (const extension of Object.keys(limits)) {
  const matching = files.filter((file) => file.endsWith(extension))
  const bytes = matching.reduce(
    async (total, file) => (await total) + gzipSync(await readFile(join(assets, file))).byteLength,
    Promise.resolve(0),
  )
  const size = await bytes
  const limit = limits[extension]
  if (size > limit) {
    throw new Error(`${extension} gzip size ${size} exceeds budget ${limit}`)
  }
  console.log(`${extension} gzip: ${size}/${limit} bytes`)
}
