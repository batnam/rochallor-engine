import { type Locator, expect, test } from '@playwright/test';

async function expectBalancedDialog(dialog: Locator) {
  await expect(dialog).toBeVisible();
  const viewport = dialog.page().viewportSize();
  const bounds = await dialog.boundingBox();
  if (!viewport || !bounds) throw new Error('Dialog or viewport missing');

  expect(bounds.height).toBeLessThan(viewport.height - 16);
  expect(bounds.width).toBeLessThan(viewport.width);
  expect(Math.abs(bounds.y + bounds.height / 2 - viewport.height / 2)).toBeLessThan(2);
  expect(Math.abs(bounds.x + bounds.width / 2 - viewport.width / 2)).toBeLessThan(2);

  const content = await dialog.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const style = getComputedStyle(element);
    return {
      left:
        rect.left + Number.parseFloat(style.borderLeftWidth) + Number.parseFloat(style.paddingLeft),
      right:
        rect.left +
        element.clientWidth +
        Number.parseFloat(style.borderLeftWidth) -
        Number.parseFloat(style.paddingRight),
    };
  });
  for (const control of await dialog.locator('input, select, textarea, button').all()) {
    const rect = await control.boundingBox();
    if (!rect) throw new Error('Dialog control missing');
    expect(rect.x).toBeGreaterThanOrEqual(content.left - 1);
    expect(rect.x + rect.width).toBeLessThanOrEqual(content.right + 1);
    if (!(await control.evaluate((element) => element instanceof HTMLTextAreaElement))) {
      expect(rect.height).toBeLessThan(48);
    }
    if (await control.evaluate((element) => element instanceof HTMLButtonElement)) {
      expect(rect.height).toBeGreaterThanOrEqual(32);
      const dialogFont = await dialog.evaluate((element) => getComputedStyle(element).fontSize);
      await expect(control).toHaveCSS('font-size', dialogFont);
    }
  }
}

for (const { button, title } of [
  { button: 'Engine Settings', title: 'Engine settings' },
  { button: 'Import Workflow', title: 'Import workflow JSON' },
  { button: 'Export Workflow', title: 'Export workflow JSON' },
  { button: 'Save / Load Drafts Workflows', title: 'Drafts (0 / 10)' },
  { button: 'Load Workflow from engine', title: 'Load Workflow from engine' },
  { button: 'Delete step', title: 'Delete step “end”' },
]) {
  test(`${button} dialog stays balanced when resizing`, async ({ page }) => {
    await page.route('http://localhost:8080/**', (route) =>
      route.fulfill({ json: { items: [], total: 0 } }),
    );
    await page.goto('/');
    await page.getByRole('button', { name: 'End', exact: true }).click();
    await page.locator('.wm-node--end').click();
    await page.getByRole('button', { name: button, exact: true }).click();
    const dialog = page.getByRole('dialog', { name: title, exact: true });
    if (button === 'Load Workflow from engine') {
      await expect(dialog.getByText('No definitions found.')).toBeVisible();
    }

    let initialHeight: number | undefined;
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 1000, height: 650 },
      { width: 600, height: 650 },
    ]) {
      await page.setViewportSize(viewport);
      await expectBalancedDialog(dialog);
      const bounds = await dialog.boundingBox();
      if (!bounds) throw new Error('Dialog missing');
      if (initialHeight === undefined) initialHeight = bounds.height;
      if (viewport.width >= 1000) {
        expect(Math.abs(bounds.height - initialHeight)).toBeLessThan(2);
      }
    }
  });
}

test('long engine dialog scrolls within a short window and keeps actions reachable', async ({
  page,
}) => {
  await page.route('http://localhost:8080/**', (route) =>
    route.fulfill({
      json: {
        items: Array.from({ length: 20 }, (_, i) => ({
          id: `workflow-${i}`,
          name: `Workflow ${i}`,
          versions: [1],
        })),
        total: 20,
      },
    }),
  );
  await page.setViewportSize({ width: 1000, height: 400 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Load Workflow from engine', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Load Workflow from engine' });
  await expect(dialog.locator('.wm-engine-row')).toHaveCount(20);
  await expectBalancedDialog(dialog);
  expect(await dialog.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(
    true,
  );
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toHaveCount(0);
});
