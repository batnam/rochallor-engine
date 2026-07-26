import { randomUUID } from "node:crypto";

import {
  Controller,
  type DynamicModule,
  Get,
  type INestApplication,
  Inject,
  Module,
  ServiceUnavailableException,
} from "@nestjs/common";
import { NestFactory } from "@nestjs/core";
import { DocumentBuilder, SwaggerModule } from "@nestjs/swagger";

import { MonitorDatabase } from "./common/database/monitor-database";
import { registerMonitorDatabase } from "./common/database/monitor-database.module";
import { IncidentsModule } from "./modules/incidents/incidents.module";
import { ProcessInstancesModule } from "./modules/process-instances/process-instances.module";
import { registerProcessVariables } from "./modules/process-variables/process-variables.module";
import { WorkflowDefinitionsModule } from "./modules/workflow-definitions/workflow-definitions.module";

export interface MonitorAppOptions {
  postgresDsn?: string;
  log?(record: MonitorLogRecord): void;
}

export interface MonitorLogRecord {
  event: "http_request";
  requestId: string;
  route: string;
  status: number;
  durationMs: number;
}

interface HttpRequest {
  headers: Record<string, string | string[] | undefined>;
  path: string;
  route?: { path?: string };
}

interface HttpResponse {
  statusCode: number;
  setHeader(name: string, value: string): void;
  once(event: "finish", listener: () => void): void;
}

const DEFAULT_MAX_JSON_DOCUMENT_BYTES = 5 * 1024 * 1024;

@Controller("health")
class HealthController {
  constructor(
    @Inject(MonitorDatabase) private readonly database: MonitorDatabase,
  ) {}

  @Get("live")
  live(): { status: "ok" } {
    return { status: "ok" };
  }

  @Get("ready")
  async ready(): Promise<{ status: "ok" }> {
    try {
      await this.database.assertReady();
      return { status: "ok" };
    } catch {
      throw new ServiceUnavailableException({ status: "unavailable" });
    }
  }
}

@Module({})
class AppModule {
  static register(options: MonitorAppOptions): DynamicModule {
    return {
      module: AppModule,
      imports: [
        registerMonitorDatabase(options.postgresDsn),
        IncidentsModule,
        ProcessInstancesModule,
        registerProcessVariables(maxJsonDocumentBytes()),
        WorkflowDefinitionsModule,
      ],
      controllers: [HealthController],
    };
  }
}

function maxJsonDocumentBytes(): number {
  const configured = process.env.MONITOR_MAX_JSON_DOCUMENT_BYTES;
  if (configured === undefined) {
    return DEFAULT_MAX_JSON_DOCUMENT_BYTES;
  }
  const parsed = Number(configured);
  if (!Number.isInteger(parsed) || parsed < 1) {
    throw new Error(
      "MONITOR_MAX_JSON_DOCUMENT_BYTES must be a positive integer",
    );
  }
  return parsed;
}

export async function createMonitorApp(
  options: MonitorAppOptions = {},
): Promise<INestApplication> {
  const app = await NestFactory.create(AppModule.register(options), {
    logger: false,
  });
  const log =
    options.log ??
    ((record: MonitorLogRecord) => {
      process.stdout.write(`${JSON.stringify(record)}\n`);
    });
  app.use(
    (request: HttpRequest, response: HttpResponse, next: () => void): void => {
      const header = request.headers["x-request-id"];
      const requestId =
        (Array.isArray(header) ? header[0] : header) || randomUUID();
      const startedAt = performance.now();
      response.setHeader("X-Request-ID", requestId);
      response.once("finish", () => {
        log({
          event: "http_request",
          requestId,
          route: request.route?.path ?? request.path,
          status: response.statusCode,
          durationMs: Math.round((performance.now() - startedAt) * 100) / 100,
        });
      });
      next();
    },
  );
  const openApiConfig = new DocumentBuilder()
    .setTitle("Rochallor Monitor BFF")
    .setVersion("1")
    .build();
  const openApiDocument = SwaggerModule.createDocument(app, openApiConfig);
  SwaggerModule.setup("api-docs", app, openApiDocument, {
    jsonDocumentUrl: "openapi.json",
  });
  return app;
}
