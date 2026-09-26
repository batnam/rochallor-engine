import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const FIXTURE = readFileSync(
  new URL('../fixtures/valid/loan-application-full.json', import.meta.url),
  'utf8',
);

const undoKey = process.platform === 'darwin' ? 'Meta+z' : 'Control+z';

test('undo-cascade: one Ctrl/Cmd-Z reverts every reference site atomically', async ({ page }) => {
  await page.goto('/');

  await page.getByRole('button', { name: 'Import' }).click();
  await page
    .getByRole('dialog', { name: 'Import workflow JSON' })
    .getByRole('textbox')
    .fill(FIXTURE);
  await page.getByRole('button', { name: 'Import', exact: true }).last().click();

  // merge-risk-results is the JOIN_GATEWAY referenced from the parallel gateway's
  // joinStep AND from both branches' nextStep — at least three reference sites.
  await page.locator('.react-flow__node[data-id="merge-risk-results"]').click();
  const idInput = page.locator('.wm-inspector .wm-input').first();
  await idInput.fill('risk-join');
  await idInput.blur();

  await page.getByRole('button', { name: 'Export' }).click();
  let exported = await page.locator('.wm-dialog .wm-dialog-textarea').inputValue();
  expect(JSON.stringify(JSON.parse(exported).steps)).toContain('"risk-join"');
  expect(JSON.stringify(JSON.parse(exported).steps)).not.toContain('"merge-risk-results"');
  await page.locator('.wm-dialog').getByRole('button', { name: 'Close' }).click();

  // Move focus back to the document body so the keyboard shortcut isn't swallowed.
  await page.locator('body').click({ position: { x: 1, y: 1 } });
  await page.keyboard.press(undoKey);

  await page.getByRole('button', { name: 'Export' }).click();
  exported = await page.locator('.wm-dialog .wm-dialog-textarea').inputValue();
  expect(JSON.stringify(JSON.parse(exported).steps)).toContain('"merge-risk-results"');
  expect(JSON.stringify(JSON.parse(exported).steps)).not.toContain('"risk-join"');
  await page.locator('.wm-dialog').getByRole('button', { name: 'Close' }).click();
  await page.locator('body').click({ position: { x: 1, y: 1 } });
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+Shift+z' : 'Control+Shift+z');
  await page.getByRole('button', { name: 'Export' }).click();
  exported = await page.locator('.wm-dialog .wm-dialog-textarea').inputValue();
  expect(JSON.stringify(JSON.parse(exported).steps)).toContain('"risk-join"');
  expect(JSON.stringify(JSON.parse(exported).steps)).not.toContain('"merge-risk-results"');
});
