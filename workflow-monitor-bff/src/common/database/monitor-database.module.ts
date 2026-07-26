import { type DynamicModule, Global, Module } from "@nestjs/common";

import { MonitorDatabase } from "./monitor-database";

@Global()
@Module({})
class MonitorDatabaseModule {}

export function registerMonitorDatabase(
  postgresDsn: string | undefined,
): DynamicModule {
  return {
    module: MonitorDatabaseModule,
    providers: [
      {
        provide: MonitorDatabase,
        useFactory: () => new MonitorDatabase(postgresDsn),
      },
    ],
    exports: [MonitorDatabase],
  };
}
