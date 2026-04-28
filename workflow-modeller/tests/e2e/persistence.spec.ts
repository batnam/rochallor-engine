import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from '@playwright/test';

const FIXTURE = readFileSync(
  resolve(__dirname, '../fixtures/valid/loan-disbursement-workflow.json'),
  'utf8',
);

test('persistence: edits survive a page reload', async ({ page }) => {
  await page.goto('/');

  await page.getByRole('button', { name: 'Import' }).click();
  await page.getByRole('textbox').fill(FIXTURE);
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();

  const importedCount = await page.locator('.wm-node').count();
  expect(importedCount).toBeGreaterThan(0);

  // Add a service-task to confirm in-memory edits also persist.
  await page.locator('.wm-palette-item--service_task').click();
  const afterAdd = await page.locator('.wm-node').count();
  expect(afterAdd).toBe(importedCount + 1);

  await page.reload();

  // Same browser context → localStorage retained → state restored.
  await expect(page.locator('.wm-node')).toHaveCount(afterAdd);
});
