import { ApiProperty } from "@nestjs/swagger";

export class PresentVariableDocument {
  @ApiProperty({ enum: ["present"] })
  status!: "present";

  @ApiProperty({
    nullable: true,
    oneOf: [
      { type: "object", additionalProperties: true },
      { type: "array", items: {} },
      { type: "string" },
      { type: "number" },
      { type: "boolean" },
    ],
  })
  value!: unknown;

  @ApiProperty({ type: "integer" })
  sizeBytes!: number;
}

export class NotRecordedVariableDocument {
  @ApiProperty({ enum: ["notRecorded"] })
  status!: "notRecorded";
}

export class ContentTooLargeVariableDocument {
  @ApiProperty({ enum: ["contentTooLarge"] })
  status!: "contentTooLarge";

  @ApiProperty({ type: "integer" })
  sizeBytes!: number;
}

export type VariableDocument =
  | PresentVariableDocument
  | NotRecordedVariableDocument
  | ContentTooLargeVariableDocument;
