import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'

const run = (command, args) => execFileSync(command, args, { encoding: 'utf8' }).trim()
const sha256 = path => createHash('sha256').update(readFileSync(path)).digest('hex')

mkdirSync('artifacts', { recursive: true })
// npm 11 returns an array; npm 12 returns an object keyed by package name.
const packed = Object.values(JSON.parse(run('npm', ['pack', '--json', '--pack-destination', 'artifacts'])))[0]
renameSync(`artifacts/${packed.filename}`, 'artifacts/sdk.tgz')
const metadata = {
  name: packed.name,
  version: packed.version,
  source: run('git', ['rev-parse', 'HEAD']),
  dirty: run('git', ['status', '--porcelain', '--', '.', '../proto']) !== '',
  node: process.version,
  npm: run('npm', ['--version']),
  protoc: run('protoc', ['--version']),
  lockSha256: sha256('package-lock.json'),
  sha256: sha256('artifacts/sdk.tgz'),
}
writeFileSync('artifacts/SHA256SUMS', `${metadata.sha256}  sdk.tgz\n`)
writeFileSync('artifacts/package.json', `${JSON.stringify(metadata, null, 2)}\n`)
console.log(JSON.stringify(metadata, null, 2))
