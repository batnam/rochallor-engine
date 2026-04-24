import { expect, test } from '@playwright/test';

test('visualise-malformed: garbage JSON surfaces an error banner and leaves the canvas empty', async ({
  page,
}) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Import' }).click();
  await page.getByRole('textbox').fill('{ "id":');
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();

  // Error banner becomes visible inside the dialog.
  await expect(page.locator('.wm-dialog-errors')).toBeVisible();

  // Cancel the dialog.
  await page.getByRole('button', { name: 'Cancel' }).click();

  // Canvas is still empty (no step nodes).
  await expect(page.locator('.wm-canvas-empty')).toBeVisible();
  await expect(page.locator('.wm-node')).toHaveCount(0);
});
