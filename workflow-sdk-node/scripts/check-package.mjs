import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { copyFileSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const sdk = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const tarball = process.argv[2]
assert(tarball, 'Usage: node scripts/check-package.mjs <tarball> [--test-failures]')
const consumer = mkdtempSync(join(tmpdir(), 'rochallor-node-consumer-'))
const options = { cwd: consumer, encoding: 'utf8', timeout: 120_000 }
const run = (command, args) => execFileSync(command, args, { ...options, stdio: 'inherit' })
const importPackage = ['--input-type=module', '-e', 'await import("rochallor-workflow-sdk-node")']
const installed = join(consumer, 'node_modules/rochallor-workflow-sdk-node')

function rejectsImport(label, pattern) {
  const result = spawnSync(process.execPath, importPackage, options)
  assert.ifError(result.error)
  assert.notEqual(result.status, 0, `${label}: broken consumer unexpectedly passed`)
  assert.match(result.stderr, pattern, `${label}: failed for an unrelated reason`)
  console.log(`Package gate rejected: ${label} (exit ${result.status})`)
}

function changeFile(path, change, check) {
  const original = readFileSync(path)
  try {
    change(path, original)
    check()
  } finally {
    writeFileSync(path, original)
  }
}

try {
  writeFileSync(join(consumer, 'package.json'), JSON.stringify({
    private: true, type: 'module',
    allowScripts: { '@confluentinc/kafka-javascript': true },
  }))
  run('npm', ['install', '--omit=dev', '--no-audit', '--no-fund', resolve(tarball)])
  run('npm', ['audit', '--omit=dev', '--audit-level=high'])
  run('npm', ['ls', '--omit=dev', '--depth=1'])
  for (const file of ['consumer.ts', 'tsconfig.json']) {
    copyFileSync(join(sdk, 'test/package', file), join(consumer, file))
  }
  // The compiler is only a tool. Module/type resolution happens in the external consumer.
  run(process.execPath, [join(sdk, 'node_modules/typescript/bin/tsc'), '-p', 'tsconfig.json'])
  run(process.execPath, ['dist/consumer.js'])

  if (process.argv.includes('--test-failures')) {
    changeFile(join(installed, 'dist/index.js'), path => rmSync(path), () =>
      rejectsImport('missing public entry point', /ERR_MODULE_NOT_FOUND/))
    changeFile(join(installed, 'dist/client/rest.js'), (path, original) =>
      writeFileSync(path, `exports.__esModule = true;\n${original}`), () =>
      rejectsImport('CommonJS emitted inside an ESM package', /exports is not defined/))
    changeFile(join(installed, 'dist/generated/workflow/v1/engine.js'), (path, original) =>
      writeFileSync(path, original.toString().replace('struct.js', 'struct')), () =>
      rejectsImport('generated import missing .js extension', /ERR_MODULE_NOT_FOUND/))

    const runtime = join(consumer, 'node_modules/@bufbuild/protobuf')
    const hidden = join(consumer, 'hidden-protobuf')
    renameSync(runtime, hidden)
    try {
      rejectsImport('missing protobuf runtime dependency', /Cannot find package '@bufbuild\/protobuf'/)
    } finally {
      renameSync(hidden, runtime)
    }
    run(process.execPath, ['dist/consumer.js'])
  }
  console.log('External tarball consumer: ESM, declarations, clients and runners passed')
} finally {
  rmSync(consumer, { recursive: true, force: true })
}
