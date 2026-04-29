import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const FIXTURE = readFileSync(
  new URL('../fixtures/valid/loan-application-full.json', import.meta.url),
  'utf8',
);

test('visualise: paste a valid workflow renders the expected graph', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Import' }).click();
  await page.getByRole('textbox').fill(FIXTURE);
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();

  // 14 step nodes on the canvas.
  await expect(page.locator('.wm-node')).toHaveCount(14);

  // Entry badge on the first step.
  await expect(page.locator('.wm-node--entry').first()).toContainText('ENTRY');

  // Boundary edge (dashed) present — the manual-review-task TIMER.
  await expect(page.locator('.wm-edge-boundary')).toHaveCount(1);

  // At least one of each distinct edge class.
  for (const klass of ['wm-edge-sequential', 'wm-edge-conditional', 'wm-edge-parallel']) {
    await expect(page.locator(`.${klass}`).first()).toBeVisible();
  }
});
