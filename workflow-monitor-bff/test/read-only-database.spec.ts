import { Pool } from "pg";

import {
  type PostgresFixture,
  startPostgresFixture,
} from "./support/postgres-fixture";

describe("database-permission seam", () => {
  let postgres: PostgresFixture | undefined;
  let bffPool: Pool | undefined;

  beforeAll(async () => {
    postgres = await startPostgresFixture();
    bffPool = new Pool({ connectionString: postgres.readOnlyDsn });
  });

  afterAll(async () => {
    await bffPool?.end();
    await postgres?.stop();
  });

  it("allows reads and rejects mutations for the BFF role", async () => {
    if (!bffPool) {
      throw new Error("BFF database pool did not start");
    }

    await expect(
      bffPool.query("SELECT id FROM workflow_instance LIMIT 1"),
    ).resolves.toBeDefined();
    await expect(
      bffPool.query("DELETE FROM workflow_instance"),
    ).rejects.toMatchObject({ code: "42501" });
  });
});
