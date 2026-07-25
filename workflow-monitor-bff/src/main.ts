import "reflect-metadata";

import { createMonitorApp } from "./app";

async function bootstrap(): Promise<void> {
  const app = await createMonitorApp({
    postgresDsn: process.env.MONITOR_POSTGRES_DSN,
  });
  const port = Number(process.env.PORT ?? "3000");
  await app.listen(port);
}

void bootstrap();
