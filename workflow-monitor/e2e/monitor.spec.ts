import { execFileSync } from "node:child_process";

import { expect, test } from "@playwright/test";

test.describe.configure({ mode: "serial" });

test("loads a Process Instance through the production relative API route", async ({
  page,
}) => {
  await page.goto("/");

  await expect(
    page.getByRole("cell", { name: "release-instance-052" }),
  ).toBeVisible();
  await expect(
    page.getByRole("row", { name: /release-instance-052.*WAITING/ }),
  ).toBeVisible();
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
}) => {
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
      name: "Expand snapshots for release-failed-execution",
    })
    .click();
  await snapshotRequest;

  await expect(
    page.getByRole("heading", { name: "Recorded Input" }),
  ).toBeVisible();
  await expect(page.getByText('"before-release"')).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Recorded Output" }),
  ).toBeVisible();
  await expect(page.getByText('"after-release"')).toBeVisible();
});

test("keeps cached data stale during a PostgreSQL outage without logging secrets", async ({
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

  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText("Stale data")).toBeVisible({ timeout: 15_000 });
  await expect(
    page.getByRole("cell", { name: "release-instance-052" }),
  ).toBeVisible();

  const logs = execFileSync("docker", ["logs", "rochallor-monitor-e2e-bff-1"], {
    encoding: "utf8",
  });
  expect(logs).toContain('"event":"http_request"');
  expect(logs).toContain('"durationMs"');
  expect(logs).not.toContain("never-log-release-secret");
  expect(logs).not.toContain("release-sensitive-card");
  expect(logs).not.toContain("release worker rejected the task");

  const restartCount = execFileSync(
    "docker",
    ["inspect", "--format", "{{.RestartCount}}", "rochallor-monitor-e2e-bff-1"],
    { encoding: "utf8" },
  ).trim();
  expect(restartCount).toBe("0");
});
