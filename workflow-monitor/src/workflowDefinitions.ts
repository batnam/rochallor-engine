export interface WorkflowDefinitionOption {
  id: string;
  name: string;
}

export async function listWorkflowDefinitions(): Promise<{
  items: WorkflowDefinitionOption[];
}> {
  const response = await fetch("/api/v1/workflow-definitions");
  if (!response.ok) {
    throw new Error("Unable to load Workflow Definitions");
  }
  return response.json() as Promise<{ items: WorkflowDefinitionOption[] }>;
}
