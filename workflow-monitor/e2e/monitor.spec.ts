import { execFileSync } from "node:child_process";

import { expect, test } from "@playwright/test";

test.describe.configure({ mode: "serial" });

test("loads a Process Instance through the production relative API route", async ({
  page,
}) => {
  const scripts: string[] = [];
  page.on("request", (request) => {
    if (request.resourceType() === "script") scripts.push(request.url());
  });
  await page.goto("/");

  await expect(
    page.getByRole("cell", { name: "release-instance-052" }),
  ).toBeVisible();
  await expect(
    page.getByRole("row", { name: /release-instance-052.*WAITING/ }),
  ).toBeVisible();
  expect(scripts.some((url) => /\/ProcessInstanceRoute-/.test(url))).toBe(
    false,
  );
  await page
    .getByRole("link", { name: "release-instance-052", exact: true })
    .click();
  await expect(
    page.getByRole("group", { name: "Execution Diagram" }),
  ).toBeVisible();
  expect(scripts.some((url) => /\/ProcessInstanceRoute-/.test(url))).toBe(true);
});

test("filters Process Instances and follows the opaque cursor", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByLabel("Business Key").fill("release-001");
  await page.getByRole("button", { name: "Apply Filters" }).click();

  await expect(
    page.getByRole("cell", { name: "release-instance-001" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\?businessKey=release-001$/);

  await page.goto("/");
  await page.getByRole("button", { name: "Next" }).click();

  await expect(
    page.getByRole("cell", { name: "release-instance-002" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\?cursor=/);
});

test("shows parallel Current Token Positions and every Step Execution attempt", async ({
  page,
}, testInfo) => {
  await page.goto("/process-instances/release-parallel");

  await expect(
    page.getByRole("heading", {
      name: "Process Instance release-parallel",
    }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Review A, Current Token Position, RUNNING, attempt 2"),
  ).toBeVisible();
  await expect(
    page.getByLabel("Review B, Current Token Position, RUNNING, attempt 1"),
  ).toBeVisible();
  await expect(
    page.getByRole("row", { name: /parallel-a-attempt-1.*FAILED/ }),
  ).toBeVisible();
  await expect(
    page.getByRole("row", { name: /parallel-a-attempt-2.*RUNNING/ }),
  ).toBeVisible();
  const context = page.getByRole("region", {
    name: "Current execution context",
  });
  await expect(
    context.getByRole("heading", {
      name: "review-a — Waiting for task completion",
    }),
  ).toBeVisible();
  await expect(
    context.getByRole("heading", {
      name: "review-b — Waiting for task completion",
    }),
  ).toBeVisible();
  await expect(context.getByText(/Boundary timer review-timer/)).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("execution-context.png"),
    fullPage: true,
  });
});

test("opens Incident Error Details and highlights the failed step", async ({
  page,
}) => {
  await page.goto("/incidents");
  await page.getByRole("link", { name: "release-failed-execution" }).click();

  await expect(
    page.getByRole("heading", {
      name: "Incident release-failed-execution",
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Error Details" }),
  ).toBeVisible();
  await expect(
    page.getByText("release worker rejected the task"),
  ).toBeVisible();

  await page
    .getByRole("link", { name: "Process Instance release-failed" })
    .click();

  await expect(page).toHaveURL(
    /\/process-instances\/release-failed\?stepId=review$/,
  );
  await expect(
    page.getByLabel("Review, Failed marker, attempt 1"),
  ).toHaveAttribute("aria-pressed", "true");
});

test("loads Current Variables and snapshots only when expanded", async ({
  page,
}) => {
  await page.goto("/process-instances/release-failed");
  await page.getByRole("tab", { name: "Variables" }).click();

  await expect(
    page.getByRole("heading", { name: "Current Variables" }),
  ).toBeVisible();
  await expect(page.getByText('"release-sensitive-card"')).toBeVisible();

  const snapshotRequest = page.waitForResponse((response) =>
    response
      .url()
      .endsWith(
        "/api/v1/process-instances/release-failed/step-executions/release-failed-execution/variables",
      ),
  );
  await page
    .getByRole("button", {
      name: "Expand snapshots for Review (attempt 1)",
    })
    .click();
  await snapshotRequest;

  await expect(
    page.getByRole("heading", { name: "Recorded Input" }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("heading", { name: "Recorded Input", exact: true })
      .locator("..")
      .getByText('"before-release"'),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Recorded Output" }),
  ).toBeVisible();
  await expect(
    page
      .getByRole("heading", { name: "Recorded Output", exact: true })
      .locator("..")
      .getByText('"after-release"'),
  ).toBeVisible();
});

test("navigates from a failed Step Execution to its Incident", async ({
  page,
}) => {
  await page.goto("/process-instances/release-failed");
  await page.getByRole("link", { name: "View Incident", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Incident release-failed-execution" }),
  ).toBeVisible();
});

test("searches workflows by name or ID and restores the search after reload", async ({
  page,
}, testInfo) => {
  await page.goto("/overview");
  const input = page.getByRole("searchbox", { name: "Search workflows" });
  const parallel = page.getByRole("button", {
    name: "Inspect parallel-release-flow v1",
  });
  const release = page.getByRole("button", {
    name: "Inspect release-flow v1",
    exact: true,
  });
  await expect(release).toBeVisible();
  await input.fill("  PARALLEL RELEASE  ");
  await input.press("Enter");
  await expect(page).toHaveURL(/workflow=PARALLEL\+RELEASE$/);
  await expect(parallel).toBeVisible();
  await expect(release).toHaveCount(0);
  await page.reload();
  await expect(input).toHaveValue("PARALLEL RELEASE");
  await expect(parallel).toBeVisible();
  await expect(release).toHaveCount(0);
  await input.fill("parallel-release");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(parallel).toBeVisible();
  await expect(release).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath("overview-search.png"),
    fullPage: true,
  });
  await input.fill("missing-workflow");
  await input.press("Enter");
  await expect(page.getByText("No workflows match your search.")).toBeVisible();
  await page.getByRole("button", { name: "Clear search" }).click();
  await expect(input).toHaveValue("");
  await expect(parallel).toBeVisible();
  await expect(release).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Collapse sidebar" }).click();
  await expect(
    page.getByRole("navigation", { name: "Monitor sections" }),
  ).not.toBeInViewport();
  await expect(input).toBeInViewport();
  await expect(
    page.getByRole("button", { name: "Search", exact: true }),
  ).toBeInViewport();
  await page.screenshot({
    path: testInfo.outputPath("overview-search-mobile.png"),
    fullPage: true,
  });
});

test("drills down from workflow and step counts using the observed age cutoff", async ({
  page,
}, testInfo) => {
  await page.goto("/overview");
  await page
    .getByRole("button", { name: "Inspect parallel-release-flow v1" })
    .click();
  const row = page.getByRole("row", { name: /^review-a 1 1 0$/ });
  await expect(row).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("overview.png"),
    fullPage: true,
  });
  await row.getByRole("link", { name: "1", exact: true }).nth(1).click();
  await expect(page).toHaveURL(/stepStartedBefore=/);
  await expect(
    page.getByRole("cell", { name: "release-parallel", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("Definition Version")).toHaveValue("1");
  await expect(page.getByLabel("Current Step ID")).toHaveValue("review-a");
  await page
    .getByRole("button", { name: "Apply Filters", exact: true })
    .click();
  await expect(
    page.getByRole("cell", { name: "release-parallel", exact: true }),
  ).toBeVisible();
});

test("labels historical failures and inspects timeline snapshots on demand", async ({
  page,
}, testInfo) => {
  await page.goto("/incidents/parallel-a-attempt-1");
  await expect(
    page.getByText("Historical failure", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Latest attempt 2: RUNNING")).toBeVisible();
  const snapshots: string[] = [];
  page.on("request", (request) => {
    if (
      request.url().includes("/step-executions/") &&
      request.url().endsWith("/variables")
    )
      snapshots.push(request.url());
  });
  await page.goto("/process-instances/release-failed");
  await page.getByRole("button", { name: "Timeline", exact: true }).click();
  await expect(
    page.getByText("All recorded step attempts are loaded."),
  ).toBeVisible();
  expect(snapshots).toHaveLength(0);
  await page
    .getByRole("button", { name: "Inspect attempt release-failed-execution" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Returned-variable changes" }),
  ).toBeVisible();
  await expect(
    page.getByRole("row", {
      name: 'phase changed "before-release" "after-release"',
    }),
  ).toBeVisible();
  expect(snapshots).toHaveLength(1);
  await expect(
    page.getByLabel("Review, Failed marker, attempt 1"),
  ).toHaveAttribute("aria-pressed", "true");
  await page.screenshot({
    path: testInfo.outputPath("timeline-comparison.png"),
    fullPage: true,
  });
});

test("opens pasted IDs, handles missing instances, and restores browser navigation", async ({
  page,
}) => {
  await page.goto("/");
  await page
    .getByLabel("Instance ID", { exact: true })
    .fill("  release-parallel  ");
  await page.getByRole("button", { name: "Open by Instance ID" }).click();
  await expect(
    page.getByRole("heading", { name: "Process Instance release-parallel" }),
  ).toBeVisible();
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "Process Instances", exact: true }),
  ).toBeVisible();
  await page.goForward();
  await expect(
    page.getByRole("heading", { name: "Process Instance release-parallel" }),
  ).toBeVisible();
  await page.goto("/");
  await page.getByLabel("Instance ID", { exact: true }).fill("missing/with ?#");
  await page.getByRole("button", { name: "Open by Instance ID" }).click();
  await expect(page).toHaveURL(/missing%2Fwith%20%3F%23$/);
  await expect(
    page.getByRole("heading", { name: "Process Instance not found" }),
  ).toBeVisible();
});

test("persists named filters across reload and supports apply, rename, and delete", async ({
  page,
}, testInfo) => {
  await page.goto("/");
  await page.getByLabel("Business Key", { exact: true }).fill("release-001");
  await page
    .getByRole("button", { name: "Apply Filters", exact: true })
    .click();
  await expect(
    page.getByRole("cell", { name: "release-instance-001" }),
  ).toBeVisible();
  await page.getByLabel("Filter name").fill("Release lookup");
  await page.getByRole("button", { name: "Save applied filters" }).click();
  await expect(
    page.getByRole("option", { name: "Release lookup" }),
  ).toBeAttached();
  await page.goto("/");
  await page.reload();
  await page
    .getByLabel("Saved filter", { exact: true })
    .selectOption({ label: "Release lookup" });
  await page.getByRole("button", { name: "Apply saved filter" }).click();
  await expect(page).toHaveURL(/\?businessKey=release-001$/);
  await expect(
    page.getByRole("cell", { name: "release-instance-001" }),
  ).toBeVisible();
  await page.getByLabel("Filter name").fill("Saved release");
  await page.getByRole("button", { name: "Rename saved filter" }).click();
  await expect(
    page.getByRole("option", { name: "Saved release" }),
  ).toBeAttached();
  await page.screenshot({
    path: testInfo.outputPath("saved-filters.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "Delete saved filter" }).click();
  await expect(page.getByRole("option", { name: "Saved release" })).toHaveCount(
    0,
  );
});

test("keeps cached data stale during a PostgreSQL outage and recovers without logging secrets", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("cell", { name: "release-instance-052" }),
  ).toBeVisible();

  const postgresContainerId = process.env.MONITOR_E2E_POSTGRES_CONTAINER_ID;
  if (!postgresContainerId) {
    throw new Error("The release PostgreSQL fixture is unavailable");
  }
  execFileSync("docker", ["stop", postgresContainerId], { stdio: "ignore" });

  try {
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(page.getByText("Stale data")).toBeVisible({ timeout: 15_000 });
    await expect(
      page.getByRole("cell", { name: "release-instance-052" }),
    ).toBeVisible();

    const logs = execFileSync(
      "docker",
      ["logs", "rochallor-monitor-e2e-bff-1"],
      {
        encoding: "utf8",
      },
    );
    expect(logs).toContain('"event":"http_request"');
    expect(logs).toContain('"durationMs"');
    expect(logs).not.toContain("never-log-release-secret");
    expect(logs).not.toContain("release-sensitive-card");
    expect(logs).not.toContain("release worker rejected the task");

    const restartCount = execFileSync(
      "docker",
      [
        "inspect",
        "--format",
        "{{.RestartCount}}",
        "rochallor-monitor-e2e-bff-1",
      ],
      { encoding: "utf8" },
    ).trim();
    expect(restartCount).toBe("0");
  } finally {
    execFileSync("docker", ["start", postgresContainerId], { stdio: "ignore" });
  }
  await expect
    .poll(
      async () =>
        (await page.request.get("/api/v1/process-instances")).status(),
      { timeout: 15_000 },
    )
    .toBe(200);
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByText("Stale data", { exact: true })).toHaveCount(0, {
    timeout: 15_000,
  });
  await expect(
    page.getByRole("cell", { name: "release-instance-052" }),
  ).toBeVisible();
});
