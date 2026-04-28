import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const BROKEN = readFileSync(
  new URL('../fixtures/invalid/ref-resolves.json', import.meta.url),
  'utf8',
);
const CLEAN = readFileSync(
  new URL('../fixtures/valid/loan-disbursement-workflow.json', import.meta.url),
  'utf8',
);

test('validate: broken fixture surfaces diagnostics, focuses node, gates Export', async ({
  page,
}) => {
  await page.goto('/');

  // 1. Import a fixture with a dangling nextStep reference.
  await page.getByRole('button', { name: 'Import' }).click();
  await page.getByRole('textbox').fill(BROKEN);
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();

  // 2. Run validation.
  await page.getByRole('button', { name: 'Validate' }).click();

  // 3. The validation panel reports the REF_RESOLVES diagnostic.
  const refResolves = page.locator('.wm-diagnostic--error', { hasText: 'REF_RESOLVES' });
  await expect(refResolves).toBeVisible();

  // 4. Export is disabled with a tooltip explaining the block.
  const exportBtn = page.getByRole('button', { name: 'Export' });
  await expect(exportBtn).toBeDisabled();
  await expect(exportBtn).toHaveAttribute('title', /Fix.*validation error/);

  // 5. Clicking the diagnostic focuses the offending node (selects + highlights it).
  await refResolves.locator('button').click();
  await expect(page.locator('.wm-node[data-id="s"]')).toHaveClass(/wm-node--has-error/);

  // 6. Loading a clean fixture clears the diagnostics and re-enables Export.
  await page.getByRole('button', { name: 'Import' }).click();
  await page.getByRole('textbox').fill(CLEAN);
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();
  await page.getByRole('button', { name: 'Validate' }).click();

  await expect(page.locator('.wm-diagnostic--error')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Export' })).toBeEnabled();
});
