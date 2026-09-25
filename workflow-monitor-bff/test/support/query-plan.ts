import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

import type { MonitorDatabase } from "../../src/common/database/monitor-database";

interface Plan {
  "Node Type": string;
  "Relation Name"?: string;
  "Actual Rows": number;
  "Actual Loops": number;
  "Rows Removed by Filter"?: number;
  "Rows Removed by Index Recheck"?: number;
  "Shared Hit Blocks": number;
  "Shared Read Blocks": number;
  "Temp Read Blocks": number;
  "Temp Written Blocks": number;
  Plans?: Plan[];
}

interface Explanation {
  "Execution Time": number;
  Plan: Plan;
}

function scannedRows(plan: Plan): number {
  const own = plan["Relation Name"]
    ? (plan["Actual Rows"] +
        (plan["Rows Removed by Filter"] ?? 0) +
        (plan["Rows Removed by Index Recheck"] ?? 0)) *
      plan["Actual Loops"]
    : 0;
  return (
    own + (plan.Plans ?? []).reduce((sum, child) => sum + scannedRows(child), 0)
  );
}

export async function verifyQueryBudget(
  name: string,
  database: MonitorDatabase,
  run: () => Promise<unknown>,
  budget: { medianMs: number; sharedBlocks: number; scannedRows: number },
): Promise<void> {
  // Run the real query builder, then explain that exact SQL and its parameters.
  const spy = jest.spyOn(database, "query");
  let call: [string, unknown[]?];
  try {
    await run();
    expect(spy).toHaveBeenCalledTimes(1);
    call = spy.mock.calls[0];
  } finally {
    spy.mockRestore();
  }
  const samples: Explanation[] = [];
  for (let sample = 0; sample < 3; sample += 1) {
    const result = await database.query<{ "QUERY PLAN": Explanation[] }>(
      `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) ${call[0]}`,
      call[1],
    );
    samples.push(result.rows[0]["QUERY PLAN"][0]);
  }
  const metrics = {
    medianMs: samples
      .map((sample) => sample["Execution Time"])
      .sort((a, b) => a - b)[1],
    sharedBlocks: Math.max(
      ...samples.map(
        ({ Plan: plan }) =>
          plan["Shared Hit Blocks"] + plan["Shared Read Blocks"],
      ),
    ),
    scannedRows: Math.max(
      ...samples.map(({ Plan: plan }) => scannedRows(plan)),
    ),
    tempBlocks: Math.max(
      ...samples.map(
        ({ Plan: plan }) =>
          plan["Temp Read Blocks"] + plan["Temp Written Blocks"],
      ),
    ),
  };
  const directory = path.resolve(__dirname, "../../testresults/query-plans");
  await mkdir(directory, { recursive: true });
  await writeFile(
    path.join(directory, `${name}.json`),
    JSON.stringify(
      { name, sql: call[0], parameters: call[1], metrics, budget, samples },
      null,
      2,
    ),
  );
  process.stdout.write(
    `${JSON.stringify({ queryPlan: name, ...metrics, budget })}\n`,
  );
  for (const sample of samples) {
    expect(sample.Plan["Node Type"]).toBe("Limit");
    expect(sample.Plan["Actual Rows"]).toBeGreaterThan(0);
    expect(sample.Plan["Actual Rows"]).toBeLessThanOrEqual(51);
  }
  expect(metrics.medianMs).toBeLessThanOrEqual(budget.medianMs);
  expect(metrics.sharedBlocks).toBeLessThanOrEqual(budget.sharedBlocks);
  expect(metrics.scannedRows).toBeLessThanOrEqual(budget.scannedRows);
}
