import { Module } from "@nestjs/common";
import { OverviewController } from "./overview.controller";
import { OverviewQueries } from "./overview.queries";

@Module({ controllers: [OverviewController], providers: [OverviewQueries] })
export class OverviewModule {}
