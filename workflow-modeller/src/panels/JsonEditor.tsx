import { exportJson } from '@/io/export';
import { useWorkflowStore } from '@/store/workflowStore';
import { type ReactNode, useEffect, useMemo, useRef, useState } from 'react';

export function JsonEditor(): ReactNode {
  const applyDefinitionFromJson = useWorkflowStore((s) => s.applyDefinitionFromJson);
  const definition = useWorkflowStore((s) => s.definition);
  const layout = useWorkflowStore((s) => s.layout);
  const edgeHandles = useWorkflowStore((s) => s.edgeHandles);

  const currentJson = useMemo(
    () => exportJson(definition, { includeLayout: true, layout, edgeHandles }),
    [definition, layout, edgeHandles],
  );

  const [text, setText] = useState(currentJson);
  const [parseError, setParseError] = useState<string | null>(null);
  const [schemaErrors, setSchemaErrors] = useState<string[]>([]);
  const focusedRef = useRef(false);
  const lastSyncedRef = useRef(currentJson);

  // Sync from store when definition/layout/edgeHandles change externally.
  // Skip sync while user is actively editing to avoid clobbering in-progress changes.
  useEffect(() => {
    if (!focusedRef.current) {
      setText(currentJson);
      lastSyncedRef.current = currentJson;
      setParseError(null);
      setSchemaErrors([]);
    }
  }, [currentJson]);

  function tryApply(value: string): void {
    try {
      JSON.parse(value);
    } catch (e) {
      setParseError((e as Error).message);
      return;
    }
    setParseError(null);
    const result = applyDefinitionFromJson(value);
    if (!result.ok) {
      setSchemaErrors(result.errors ?? []);
    } else {
      setSchemaErrors([]);
    }
  }

  function handleChange(e: React.ChangeEvent<HTMLTextAreaElement>): void {
    const value = e.target.value;
    setText(value);
    // Live syntax check only — apply deferred to blur to avoid thrashing the store
    try {
      JSON.parse(value);
      setParseError(null);
    } catch (err) {
      setParseError((err as Error).message);
    }
  }

  function handleBlur(): void {
    focusedRef.current = false;
    if (text === lastSyncedRef.current) return;
    tryApply(text);
  }

  const allErrors = [...(parseError ? [parseError] : []), ...schemaErrors];

  return (
    <div className="wm-json-editor">
      {allErrors.length > 0 && (
        <div className="wm-json-editor-errors" role="alert">
          {allErrors.map((e, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: static error list rendered once per change
            <span key={i} className="wm-json-editor-error-item">
              {e}
            </span>
          ))}
        </div>
      )}
      <textarea
        className="wm-json-editor-textarea"
        value={text}
        onChange={handleChange}
        onFocus={() => {
          focusedRef.current = true;
        }}
        onBlur={handleBlur}
        spellCheck={false}
        aria-label="Workflow definition JSON"
      />
    </div>
  );
}
