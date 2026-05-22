import { expect, test } from '@playwright/test';

/**
 * Drag a Decision Table from the palette, set hit policy + linear nextStep,
 * add a single rule, and verify the canvas shows exactly one outbound edge
 * (no per-rule fan-out).
 *
 * NOTE: selector strategy mirrors the other e2e specs (.wm-palette-item +
 * accessible names). Fix selectors here before deleting tests if the UI
 * drifts.
 */
test('decision-table: drag tile → set hitPolicy + nextStep → exactly one outbound edge', async ({
  page,
}) => {
  await page.goto('/');

  const paletteItems = page.locator('.wm-palette-item');
  // One END target + the Decision Table tile.
  await paletteItems.filter({ hasText: 'End' }).first().click();
  await paletteItems.filter({ hasText: 'Decision Table' }).first().click();

  // Canvas: 2 nodes, 0 edges (DT.nextStep is still empty).
  await expect(page.locator('.wm-node')).toHaveCount(2);
  await expect(page.locator('.react-flow__edge')).toHaveCount(0);

  // Select the Decision Table node.
  await page.locator('.wm-node--decision_table').first().click();

  // Hit policy selector defaults to U.
  const hitPolicy = page.getByLabel('Hit policy');
  await expect(hitPolicy).toBeVisible();
  await expect(hitPolicy).toHaveValue('U');
  await hitPolicy.selectOption('F');

  // Next-step picker, then point it at the END.
  const nextStep = page.getByLabel('Next step');
  await expect(nextStep).toBeVisible();
  await nextStep.selectOption({ index: 1 }); // first non-empty option = the END

  // Add a rule; the rule row must not contain a per-row step <select>.
  await page.getByRole('button', { name: '+ add rule' }).click();
  const ruleRow = page.locator('.wm-dt-rule-row').first();
  await expect(ruleRow).toBeVisible();
  await expect(ruleRow.locator('select')).toHaveCount(0);

  // Canvas: a single outbound edge from the DT to the END.
  await expect(page.locator('.react-flow__edge')).toHaveCount(1);
});
