import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const SAMPLE = JSON.parse(
  readFileSync(
    new URL('../fixtures/valid/loan-disbursement-workflow.json', import.meta.url),
    'utf8',
  ),
);

const BASE = 'http://mock-engine.example';

test.describe('engine integration (mocked)', () => {
  test.beforeEach(async ({ page }) => {
    let nextVersion = 5;

    await page.route(`${BASE}/v1/definitions*`, async (route) => {
      const url = new URL(route.request().url());
      if (route.request().method() === 'GET' && url.pathname === '/v1/definitions') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            items: [{ id: SAMPLE.id, name: SAMPLE.name, versions: [1, 2, 3, 4] }],
            page: 0,
            pageSize: 20,
            total: 1,
          }),
        });
        return;
      }
      if (route.request().method() === 'POST' && url.pathname === '/v1/definitions') {
        const body = JSON.parse((route.request().postData() ?? '{}') as string);
        await route.fulfill({
          status: 201,
          contentType: 'application/json',
          body: JSON.stringify({ id: body.id, name: body.name, version: nextVersion++ }),
        });
        return;
      }
      await route.continue();
    });

    await page.route(`${BASE}/v1/definitions/*`, async (route) => {
      const url = new URL(route.request().url());
      const parts = url.pathname.split('/').filter(Boolean);
      if (parts.length === 3) {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ ...SAMPLE, version: 4 }),
        });
        return;
      }
      if (parts.length === 5 && parts[3] === 'versions') {
        const v = Number(parts[4]);
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ ...SAMPLE, version: v }),
        });
        return;
      }
      await route.continue();
    });
  });

  test('browse → load → upload yields a new version', async ({ page }) => {
    await page.goto('/');

    // Configure the engine base URL.
    await page.getByRole('button', { name: 'Settings' }).click();
    const baseInput = page.getByPlaceholder('http://localhost:8080');
    await baseInput.fill(BASE);
    await page.getByRole('button', { name: 'Save' }).click();

    // Open the engine browser, list returns one definition.
    await page.getByRole('button', { name: 'Load from engine' }).click();
    const row = page.locator('.wm-engine-row', { hasText: SAMPLE.id });
    await expect(row).toBeVisible();

    // Select and load.
    await row.locator('.wm-engine-row-main').click();
    await page.getByRole('button', { name: 'Load', exact: true }).click();

    // Canvas now shows the loaded definition.
    await expect(page.locator('.wm-node')).toHaveCount(SAMPLE.steps.length);

    // Upload — confirm the dialog and assert a success banner appears.
    page.once('dialog', (dialog) => dialog.accept());
    await page.getByRole('button', { name: 'Upload' }).click();
    await expect(page.locator('.wm-banner', { hasText: 'Uploaded as version 5' })).toBeVisible();
  });
});
