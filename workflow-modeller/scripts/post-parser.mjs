import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const target = resolve(here, '..', 'src/domain/expression/parser.generated.ts');

const src = readFileSync(target, 'utf8');
const header = '// @ts-nocheck\n';
if (!src.startsWith(header)) {
  writeFileSync(target, header + src);
}
