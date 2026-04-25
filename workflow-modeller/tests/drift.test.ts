/**
 * Drift guard (T068) — pairs the TypeScript validator with the Go engine
 * validator and fails if the two disagree on whether a fixture should be
 * accepted.
 *
 * The check is intentionally asymmetric: SC-002 only requires that the
 * editor never accepts something the engine rejects. The reverse case
 * (TS rejects, Go accepts) is permitted — the editor is allowed to be
 * stricter than the engine (e.g. it bans nested parallel gateways and
 * lints decision expressions, neither of which the Go validator does).
 */
import { basename } from 'node:path';

import { beforeAll, describe, expect, it } from 'vitest';

import {
  checkFixture,
  compileGoRunner,
  isGoAvailable,
  listFixtures,
} from '../scripts/drift-check';

const validFixtures = listFixtures('valid');
const invalidFixtures = listFixtures('invalid');
const goAvailable = isGoAvailable();

describe.skipIf(!goAvailable)('drift guard — Go vs TS validators', () => {
  beforeAll(() => {
    compileGoRunner();
  }, 60_000);

  describe('valid fixtures — both validators must accept', () => {
    for (const fixture of validFixtures) {
      it(basename(fixture), () => {
        const r = checkFixture(fixture);
        expect(r.ts, `TS rejected: ${JSON.stringify(r.ts.diagnostics)}`).toMatchObject({
          accepted: true,
        });
        expect(r.go, `Go rejected: ${r.go.error}`).toMatchObject({ accepted: true });
        expect(r.drift, 'SC-002 drift detected').toBe(false);
      });
    }
  });

  describe('invalid fixtures — TS must reject; Go agreement is informational', () => {
    for (const fixture of invalidFixtures) {
      it(basename(fixture), () => {
        const r = checkFixture(fixture);
        expect(
          r.ts.accepted,
          `TS accepted an invalid fixture (${basename(fixture)}); expected at least one error diagnostic`,
        ).toBe(false);
        // SC-002 protection: TS rejecting is sufficient. Go may accept (TS is
        // stricter on some rules) — that direction is OK.
        expect(r.drift, 'SC-002 drift: TS accepts but Go rejects').toBe(false);
      });
    }
  });
});

describe.skipIf(goAvailable)('drift guard — skipped (no `go` on PATH)', () => {
  it('skips drift checks because Go is not installed', () => {
    expect(isGoAvailable()).toBe(false);
  });
});
