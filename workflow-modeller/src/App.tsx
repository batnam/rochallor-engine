import { Canvas } from '@/canvas/Canvas';
import type { StepId } from '@/domain/types';
import { Banner, type BannerTone } from '@/panels/Banner';
import { DeleteStepDialog } from '@/panels/DeleteStepDialog';
import { ExportDialog } from '@/panels/ExportDialog';
import { ImportDialog } from '@/panels/ImportDialog';
import { Palette } from '@/panels/Palette';
import { PropertyPanel } from '@/panels/PropertyPanel';
import { Toolbar } from '@/panels/Toolbar';
import { useSelection, useValidationSummary } from '@/store/selectors';
import { ReactFlowProvider } from '@xyflow/react';
import { type ReactNode, useEffect, useState } from 'react';

function ValidationPanel(): ReactNode {
  const { errors, warnings, diagnostics, lastRunAt } = useValidationSummary();
  return (
    <footer className="wm-validation" aria-label="Validation panel">
      <div className="wm-validation-summary">
        <strong>
          {errors} error(s), {warnings} warning(s)
        </strong>
        <span className="wm-validation-timestamp">
          {lastRunAt ? `Checked ${new Date(lastRunAt).toLocaleTimeString()}` : 'Not run yet'}
        </span>
      </div>
      {diagnostics.length > 0 && (
        <ul className="wm-diagnostic-list">
          {diagnostics.slice(0, 20).map((d, i) => (
            <li
              key={`${d.code}-${d.nodeId ?? 'root'}-${d.field ?? ''}-${i}`}
              className={`wm-diagnostic wm-diagnostic--${d.severity}`}
            >
              <code>{d.code}</code> {d.nodeId ? `[${d.nodeId}] ` : ''}
              {d.message}
            </li>
          ))}
        </ul>
      )}
    </footer>
  );
}

interface BannerState {
  tone: BannerTone;
  messages: string[];
}

export function App(): ReactNode {
  const [importOpen, setImportOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<StepId | null>(null);
  const [banner, setBanner] = useState<BannerState | null>(null);
  const selection = useSelection();

  // Delete key removes the selected step (via confirmation dialog if it has refs).
  useEffect(() => {
    function handleKey(e: KeyboardEvent): void {
      if (e.key !== 'Delete' && e.key !== 'Backspace') return;
      // ignore when focused in a text field
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
      if (selection.kind === 'step') {
        setDeleteTarget(selection.id);
      }
    }
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [selection]);

  return (
    <ReactFlowProvider>
      <main className="wm-shell">
        <header className="wm-toolbar">
          <h1>Rochallor Workflow Modeller</h1>
          <Toolbar onImport={() => setImportOpen(true)} onExport={() => setExportOpen(true)} />
        </header>
        {banner && (
          <Banner tone={banner.tone} messages={banner.messages} onDismiss={() => setBanner(null)} />
        )}
        <section className="wm-body">
          <Palette />
          <section className="wm-canvas" aria-label="Canvas">
            <Canvas />
          </section>
          <PropertyPanel onRequestDelete={(id) => setDeleteTarget(id)} />
        </section>
        <ValidationPanel />
      </main>
      <ImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onImported={(warnings) => {
          if (warnings.length > 0) setBanner({ tone: 'warning', messages: warnings });
          else setBanner(null);
        }}
        onError={(errors) => setBanner({ tone: 'error', messages: errors })}
      />
      <ExportDialog open={exportOpen} onClose={() => setExportOpen(false)} />
      <DeleteStepDialog stepId={deleteTarget} onClose={() => setDeleteTarget(null)} />
    </ReactFlowProvider>
  );
}
