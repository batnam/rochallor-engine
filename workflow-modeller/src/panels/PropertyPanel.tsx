import type { Step, StepId, WorkflowDefinition } from '@/domain/types';
import { useSelection } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import type { ReactNode } from 'react';
import { DecisionForm } from './property-forms/DecisionForm';
import { Field, StepPicker, TextInput } from './property-forms/FormPrimitives';
import { JoinGatewayForm } from './property-forms/JoinGatewayForm';
import { ParallelGatewayForm } from './property-forms/ParallelGatewayForm';
import { ServiceTaskForm } from './property-forms/ServiceTaskForm';
import { TransformationForm } from './property-forms/TransformationForm';
import { UserTaskForm } from './property-forms/UserTaskForm';
import { WaitForm } from './property-forms/WaitForm';

interface PropertyPanelProps {
  onRequestDelete: (id: StepId) => void;
}

export function PropertyPanel({ onRequestDelete }: PropertyPanelProps): ReactNode {
  const selection = useSelection();
  const step = useWorkflowStore((s) =>
    selection.kind === 'step' ? s.definition.steps.find((st) => st.id === selection.id) : undefined,
  );

  return (
    <aside className="wm-inspector" aria-label="Property panel">
      <h2 className="wm-panel-heading">{step ? `Step · ${step.type}` : 'Workflow'}</h2>
      {step ? (
        <>
          <StepForm step={step} />
          <div className="wm-inspector-footer">
            <button
              type="button"
              className="wm-button-danger"
              onClick={() => onRequestDelete(step.id)}
            >
              Delete step
            </button>
          </div>
        </>
      ) : (
        <DefinitionFields />
      )}
    </aside>
  );
}

function StepForm({ step }: { step: Step }): ReactNode {
  switch (step.type) {
    case 'SERVICE_TASK':
      return <ServiceTaskForm step={step} />;
    case 'USER_TASK':
      return <UserTaskForm step={step} />;
    case 'DECISION':
      return <DecisionForm step={step} />;
    case 'TRANSFORMATION':
      return <TransformationForm step={step} />;
    case 'WAIT':
      return <WaitForm step={step} />;
    case 'PARALLEL_GATEWAY':
      return <ParallelGatewayForm step={step} />;
    case 'JOIN_GATEWAY':
      return <JoinGatewayForm step={step} />;
    case 'END':
      return (
        <>
          <Field label="Step id">
            <TextInput
              value={step.id}
              onCommit={(v) => {
                if (v !== step.id) useWorkflowStore.getState().renameStepId(step.id, v);
              }}
            />
          </Field>
          <Field label="Name">
            <TextInput
              value={step.name}
              onCommit={(v) => useWorkflowStore.getState().updateStepProperty(step.id, 'name', v)}
            />
          </Field>
        </>
      );
  }
}

function DefinitionFields(): ReactNode {
  const def = useWorkflowStore((s) => s.definition);
  const setDefinitionMeta = useWorkflowStore((s) => s.setDefinitionMeta);

  function patch<K extends keyof WorkflowDefinition>(key: K, value: WorkflowDefinition[K]): void {
    setDefinitionMeta({ [key]: value } as Partial<WorkflowDefinition>);
  }

  return (
    <>
      <Field label="Workflow id">
        <TextInput value={def.id} onCommit={(v) => patch('id', v)} />
      </Field>
      <Field label="Name">
        <TextInput value={def.name} onCommit={(v) => patch('name', v)} />
      </Field>
      <Field label="Description">
        <TextInput
          value={def.description ?? ''}
          onCommit={(v) => patch('description', v || undefined)}
        />
      </Field>
      <Field label="Auto-start next workflow">
        <input
          type="checkbox"
          checked={def.autoStartNextWorkflow ?? false}
          onChange={(e) => patch('autoStartNextWorkflow', e.target.checked || undefined)}
        />
      </Field>
      <Field label="Next workflow id">
        <TextInput
          value={def.nextWorkflowId ?? ''}
          onCommit={(v) => patch('nextWorkflowId', v || undefined)}
        />
      </Field>
      {def.steps.length > 0 && (
        <Field label="Entry step">
          <StepPicker value={def.steps[0]?.id ?? ''} onCommit={() => undefined} allowEmpty />
        </Field>
      )}
    </>
  );
}
