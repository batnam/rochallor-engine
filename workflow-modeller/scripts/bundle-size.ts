/**
 * Bundle-size budget guard (T093).
 *
 * Builds the SPA, sums the gzipped size of every emitted JS asset, and fails
 * if the total exceeds the budget defined in plan.md (500 KB gzipped).
 */
import { execSync } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const BUDGET_GZIP_BYTES = 500 * 1024;

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, '..');
const ASSETS_DIR = join(ROOT, 'dist', 'assets');

console.log('Building production bundle…');
const viteBin = join(ROOT, 'node_modules', '.bin', 'vite');
execSync(`"${viteBin}" build`, { stdio: 'inherit', cwd: ROOT });

const jsAssets = readdirSync(ASSETS_DIR).filter((f) => f.endsWith('.js'));
if (jsAssets.length === 0) {
  console.error(`No JS assets found in ${ASSETS_DIR}`);
  process.exit(1);
}

let totalRaw = 0;
let totalGzip = 0;
const rows: Array<{ file: string; raw: number; gzip: number }> = [];

for (const file of jsAssets) {
  const buf = readFileSync(join(ASSETS_DIR, file));
  const gz = gzipSync(buf);
  rows.push({ file, raw: buf.length, gzip: gz.length });
  totalRaw += buf.length;
  totalGzip += gz.length;
}

rows.sort((a, b) => b.gzip - a.gzip);
console.log('');
console.log('Asset                                 Raw         Gzip');
console.log('─'.repeat(60));
for (const r of rows) {
  console.log(`${r.file.padEnd(36)} ${kb(r.raw).padStart(8)}  ${kb(r.gzip).padStart(8)}`);
}
console.log('─'.repeat(60));
console.log(`${'TOTAL'.padEnd(36)} ${kb(totalRaw).padStart(8)}  ${kb(totalGzip).padStart(8)}`);
console.log(`Budget: ${kb(BUDGET_GZIP_BYTES)} gzipped`);

if (totalGzip > BUDGET_GZIP_BYTES) {
  const over = totalGzip - BUDGET_GZIP_BYTES;
  console.error(`\n✗ Bundle exceeds budget by ${kb(over)} gzipped`);
  process.exit(1);
}

console.log(`\n✓ Within budget (${kb(BUDGET_GZIP_BYTES - totalGzip)} headroom)`);

function kb(bytes: number): string {
  return `${(bytes / 1024).toFixed(1)} KB`;
}
