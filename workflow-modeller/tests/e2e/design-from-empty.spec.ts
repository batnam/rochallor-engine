import { expect, test } from '@playwright/test';

/**
 * Design-from-empty journey: add steps, validate, edit, then start fresh.
 */
test('design-from-empty: add steps → validate → start a new workflow', async ({ page }) => {
  await page.goto('/');
  // Seed the id/name in the definition panel (workflow fields appear when nothing is selected).
  await page.getByRole('button', { name: 'Service Task', exact: true }).first().click();
  await page.getByRole('button', { name: 'Decision', exact: true }).first().click();
  await page.getByRole('button', { name: 'End', exact: true }).first().click();

  await expect(page.locator('.wm-node')).toHaveCount(3);

  // Run validation — dangling refs are expected for the freshly-seeded workflow.
  await page.getByRole('button', { name: 'Validate' }).click();

  // Clean up: rename the first step + attach it to the END.
  await page.locator('.wm-node').first().click();
  // Rename via inspector
  const idInput = page.locator('.wm-inspector .wm-input').first();
  await idInput.fill('entry');
  await idInput.blur();

  // Starting a new workflow uses the shared confirmation dialog.
  await page.getByRole('button', { name: 'New Workflow', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'OK' }).click();
  await expect(page.locator('.wm-canvas-empty')).toBeVisible();
});
