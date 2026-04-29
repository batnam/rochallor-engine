import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const FIXTURE = readFileSync(
  new URL('../fixtures/valid/loan-application-full.json', import.meta.url),
  'utf8',
);

/**
 * US2 cascading rename: after renaming a referenced step, every reference site
 * in the exported JSON reflects the new id.
 */
test('cascading-rename: every reference site updates in one action', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Import' }).click();
  await page.getByRole('textbox').fill(FIXTURE);
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();

  // Select the "merge-risk-results" JOIN_GATEWAY — referenced by the parallel
  // gateway's joinStep and by both branches' nextStep.
  await page.locator('.wm-node', { hasText: 'merge-risk-results' }).click();

  // Rename via inspector.
  const idInput = page.locator('.wm-inspector .wm-input').first();
  await idInput.fill('risk-join');
  await idInput.blur();

  // Open export dialog and inspect its textarea content for the new id.
  await page.getByRole('button', { name: 'Export' }).click();
  const exported = await page.locator('.wm-dialog .wm-dialog-textarea').inputValue();
  expect(exported).toContain('"risk-join"');
  expect(exported).not.toContain('"merge-risk-results"');
});
