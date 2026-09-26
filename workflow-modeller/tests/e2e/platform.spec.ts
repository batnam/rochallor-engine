import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { type Locator, expect, test } from '@playwright/test';

const fixture = fileURLToPath(
  new URL('../fixtures/valid/loan-disbursement-workflow.json', import.meta.url),
);

async function expectContentSizedDialog(dialog: Locator) {
  await expect(dialog).toBeVisible();
  const bounds = await dialog.boundingBox();
  const viewport = dialog.page().viewportSize();
  if (!bounds || !viewport) throw new Error('Dialog or viewport missing');
  expect(bounds.height).toBeLessThan(300);
  expect(Math.abs(bounds.y + bounds.height / 2 - viewport.height / 2)).toBeLessThan(2);
  for (const button of await dialog.getByRole('button').all()) {
    const buttonBounds = await button.boundingBox();
    if (!buttonBounds) throw new Error('Dialog button missing');
    expect(buttonBounds.height).toBeLessThan(48);
  }
}

test('opens a JSON file and downloads it with workflow and layout intact', async ({ page }) => {
  const original = JSON.parse(await readFile(fixture, 'utf8'));
  await page.goto('/');
  await page.getByRole('button', { name: 'Import Workflow', exact: true }).click();
  const picker = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: 'Choose JSON file' }).click();
  await (await picker).setFiles(fixture);
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect(page.locator('.wm-node')).toHaveCount(original.steps.length);
  await page.getByRole('button', { name: 'Export Workflow', exact: true }).click();
  const exported = JSON.parse(await page.locator('.wm-dialog-textarea').inputValue());
  expect(exported.steps).toEqual(original.steps);
  expect(Object.keys(exported.metadata.workflowModeller.layout)).toHaveLength(
    original.steps.length,
  );
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Save JSON' }).click();
  const path = await (await download).path();
  if (!path) throw new Error('Download missing');
  expect(JSON.parse(await readFile(path, 'utf8'))).toEqual(exported);
  await expect(page.getByRole('status')).toHaveText('Download started.');
});

test('shared dialogs validate names, support Escape and confirm destructive actions', async ({
  page,
}) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Decision Table', exact: true }).click();
  await page.locator('.wm-node--decision_table').click();
  await page.getByRole('button', { name: '+ add rule' }).click();
  await page.getByRole('button', { name: '+ add input' }).click();
  const prompt = page.getByRole('dialog', { name: 'Input column name (a variable name):' });
  await expectContentSizedDialog(prompt);
  await expect(prompt.getByRole('textbox')).toBeFocused();
  await prompt.getByRole('textbox').fill('score');
  await prompt.getByRole('textbox').press('Enter');
  await expect(page.getByRole('button', { name: 'Rename score', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '+ add input' }).click();
  await prompt.getByRole('textbox').fill('score');
  await prompt.getByRole('button', { name: 'OK' }).click();
  await expect(prompt.getByRole('alert')).toHaveText('Column "score" already exists.');
  await expectContentSizedDialog(prompt);
  await page.keyboard.press('Escape');
  await expect(prompt).toHaveCount(0);
  await page.getByRole('button', { name: 'New Workflow', exact: true }).click();
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1000, height: 650 },
  ]) {
    await page.setViewportSize(viewport);
    await expectContentSizedDialog(page.getByRole('dialog'));
  }
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
  await expect(page.locator('.wm-node')).toHaveCount(1);
  await page.getByRole('button', { name: 'New Workflow', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'OK' }).click();
  await expect(page.locator('.wm-node')).toHaveCount(0);
});
