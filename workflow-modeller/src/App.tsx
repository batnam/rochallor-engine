import { Canvas } from '@/canvas/Canvas';
import type { StepId } from '@/domain/types';
import { Banner, type BannerTone } from '@/panels/Banner';
import { DeleteStepDialog } from '@/panels/DeleteStepDialog';
import { EngineBrowser } from '@/panels/EngineBrowser';
import { ExportDialog } from '@/panels/ExportDialog';
import { ImportDialog } from '@/panels/ImportDialog';
import { Palette } from '@/panels/Palette';
import { PropertyPanel } from '@/panels/PropertyPanel';
import { SettingsDialog } from '@/panels/SettingsDialog';
import { Toolbar } from '@/panels/Toolbar';
import { ValidationPanel } from '@/panels/ValidationPanel';
import { useSelection } from '@/store/selectors';
import { useWorkflowStore } from '@/store/workflowStore';
import { ReactFlowProvider } from '@xyflow/react';
import { type ReactNode, useEffect, useState } from 'react';

function isEditableTarget(t: EventTarget | null): boolean {
  if (!(t instanceof HTMLElement)) return false;
  return t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable;
}

function useKeyboardShortcuts(handlers: {
  onDelete?: () => void;
  onUndo: () => void;
  onRedo: () => void;
}): void {
  const { onDelete, onUndo, onRedo } = handlers;
  useEffect(() => {
    function handleKey(e: KeyboardEvent): void {
      if (isEditableTarget(e.target)) return;
      const mod = e.ctrlKey || e.metaKey;
      const key = e.key.toLowerCase();
      if (mod && key === 'z' && e.shiftKey) {
        e.preventDefault();
        onRedo();
        return;
      }
      if (mod && key === 'z') {
        e.preventDefault();
        onUndo();
        return;
      }
      if (mod && key === 'y') {
        e.preventDefault();
        onRedo();
        return;
      }
      if ((e.key === 'Delete' || e.key === 'Backspace') && onDelete) {
        onDelete();
        return;
      }
    }
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, [onDelete, onUndo, onRedo]);
}

interface BannerState {
  tone: BannerTone;
  messages: string[];
}

export function App(): ReactNode {
  const [importOpen, setImportOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [engineBrowserOpen, setEngineBrowserOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<StepId | null>(null);
  const [banner, setBanner] = useState<BannerState | null>(null);
  const selection = useSelection();

  useKeyboardShortcuts({
    onDelete: () => {
      if (selection.kind === 'step') setDeleteTarget(selection.id);
    },
    onUndo: () => {
      (
        useWorkflowStore as unknown as { temporal: { getState: () => { undo: () => void } } }
      ).temporal
        .getState()
        .undo();
    },
    onRedo: () => {
      (
        useWorkflowStore as unknown as { temporal: { getState: () => { redo: () => void } } }
      ).temporal
        .getState()
        .redo();
    },
  });

  return (
    <ReactFlowProvider>
      <main className="wm-shell">
        <header className="wm-toolbar">
          <h1>Rochallor Workflow Modeller</h1>
          <Toolbar
            onImport={() => setImportOpen(true)}
            onExport={() => setExportOpen(true)}
            onOpenSettings={() => setSettingsOpen(true)}
            onOpenEngineBrowser={() => setEngineBrowserOpen(true)}
            onUploadResult={(r) =>
              setBanner({ tone: r.ok ? 'info' : 'error', messages: [r.message] })
            }
          />
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
      <SettingsDialog open={settingsOpen} onClose={() => setSettingsOpen(false)} />
      <EngineBrowser
        open={engineBrowserOpen}
        onClose={() => setEngineBrowserOpen(false)}
        onLoaded={() => setBanner({ tone: 'info', messages: ['Loaded definition from engine.'] })}
      />
      <DeleteStepDialog stepId={deleteTarget} onClose={() => setDeleteTarget(null)} />
    </ReactFlowProvider>
  );
}
