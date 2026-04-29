import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const SAMPLE = readFileSync(
  new URL('../fixtures/valid/loan-disbursement-workflow.json', import.meta.url),
  'utf8',
);

const BASE = 'http://mock-engine.example';

test('engine-unreachable: status flips to unreachable and pending edit is preserved', async ({
  page,
}) => {
  // All engine traffic is rejected at the network layer.
  await page.route(`${BASE}/**`, (route) => route.abort());

  await page.goto('/');

  // First, paste a definition so we have a non-empty canvas.
  await page.getByRole('button', { name: 'Import' }).click();
  await page.getByRole('textbox').fill(SAMPLE);
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();
  await expect(page.locator('.wm-node').first()).toBeVisible();
  const nodeCountBefore = await page.locator('.wm-node').count();

  // Configure the engine base URL.
  await page.getByRole('button', { name: 'Settings' }).click();
  const baseInput = page.getByPlaceholder('http://localhost:8080');
  await baseInput.fill(BASE);

  // Test connection — expect "unreachable".
  await page.getByRole('button', { name: 'Test connection' }).click();
  await expect(page.locator('.wm-status--unreachable')).toBeVisible();
  await page.getByRole('button', { name: 'Save' }).click();

  // Toolbar status flips to unreachable.
  await expect(page.locator('.wm-engine-status--unreachable')).toBeVisible();

  // Canvas state preserved — no silent data loss.
  await expect(page.locator('.wm-node')).toHaveCount(nodeCountBefore);
});
