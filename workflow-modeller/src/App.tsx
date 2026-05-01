import { Canvas } from '@/canvas/Canvas';
import type { StepId } from '@/domain/types';
import { Banner, type BannerTone } from '@/panels/Banner';
import { DeleteStepDialog } from '@/panels/DeleteStepDialog';
import { EngineBrowser } from '@/panels/EngineBrowser';
import { ExportDialog } from '@/panels/ExportDialog';
import { ImportDialog } from '@/panels/ImportDialog';
import { JsonEditor } from '@/panels/JsonEditor';
import { Palette } from '@/panels/Palette';
import { PropertyPanel } from '@/panels/PropertyPanel';
import { SettingsDialog } from '@/panels/SettingsDialog';
import { Toolbar } from '@/panels/Toolbar';
import { ValidationPanel } from '@/panels/ValidationPanel';
import { ReactFlowProvider } from '@xyflow/react';
import { type ReactNode, useRef, useState } from 'react';

interface BannerState {
  tone: BannerTone;
  messages: string[];
  floating?: boolean;
  autoDismissMs?: number;
}

export function App(): ReactNode {
  const [importOpen, setImportOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [engineBrowserOpen, setEngineBrowserOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<StepId | null>(null);
  const [banner, setBanner] = useState<BannerState | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [view, setView] = useState<'canvas' | 'json'>('canvas');
  const [inspectorWidth, setInspectorWidth] = useState(320);
  const dragRef = useRef<{ startX: number; startWidth: number } | null>(null);

  function handleResizeStart(e: React.MouseEvent): void {
    e.preventDefault();
    dragRef.current = { startX: e.clientX, startWidth: inspectorWidth };
    function onMove(ev: MouseEvent): void {
      if (!dragRef.current) return;
      const delta = dragRef.current.startX - ev.clientX;
      setInspectorWidth(Math.max(200, dragRef.current.startWidth + delta));
    }
    function onUp(): void {
      dragRef.current = null;
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  return (
    <ReactFlowProvider>
      <main className="wm-shell">
        <div className="wm-header-area">
          <header className="wm-topbar">
            <button
              type="button"
              className="wm-sidebar-toggle"
              onClick={() => setSidebarOpen((o) => !o)}
              aria-label={sidebarOpen ? 'Collapse sidebar' : 'Expand sidebar'}
              title={sidebarOpen ? 'Collapse sidebar' : 'Expand sidebar'}
            >
              ≡
            </button>
            <h1>Rochallor Workflow Modeller</h1>
          </header>
          {banner && (
            <Banner
              tone={banner.tone}
              messages={banner.messages}
              onDismiss={() => setBanner(null)}
              floating={banner.floating}
              autoDismissMs={banner.autoDismissMs}
            />
          )}
        </div>
        <div className="wm-layout">
          <aside className={`wm-sidebar${sidebarOpen ? '' : ' wm-sidebar--collapsed'}`}>
            <div className="wm-sidebar-section">
              <span className="wm-sidebar-section-label">Actions</span>
              <Toolbar
                onImport={() => setImportOpen(true)}
                onExport={() => setExportOpen(true)}
                onOpenSettings={() => setSettingsOpen(true)}
                onOpenEngineBrowser={() => setEngineBrowserOpen(true)}
                onUploadResult={(r) =>
                  setBanner({ tone: r.ok ? 'info' : 'error', messages: [r.message] })
                }
              />
            </div>
            <Palette />
          </aside>
          <section
            className="wm-body"
            style={{ gridTemplateColumns: `1fr 4px ${inspectorWidth}px` }}
          >
            <section className="wm-canvas" aria-label="Workflow editor">
              <div className="wm-view-tabs" role="tablist">
                <button
                  type="button"
                  role="tab"
                  aria-selected={view === 'canvas'}
                  className={`wm-view-tab${view === 'canvas' ? ' wm-view-tab--active' : ''}`}
                  onClick={() => setView('canvas')}
                >
                  Visual
                </button>
                <button
                  type="button"
                  role="tab"
                  aria-selected={view === 'json'}
                  className={`wm-view-tab${view === 'json' ? ' wm-view-tab--active' : ''}`}
                  onClick={() => setView('json')}
                >
                  JSON
                </button>
              </div>
              <div className="wm-canvas-content">
                {view === 'canvas' ? <Canvas /> : <JsonEditor />}
              </div>
            </section>
            <div className="wm-resize-handle" onMouseDown={handleResizeStart} />
            <PropertyPanel onRequestDelete={(id) => setDeleteTarget(id)} />
          </section>
        </div>
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
        onLoaded={() =>
          setBanner({
            tone: 'info',
            messages: ['Loaded definition from engine.'],
            floating: true,
            autoDismissMs: 3000,
          })
        }
      />
      <DeleteStepDialog stepId={deleteTarget} onClose={() => setDeleteTarget(null)} />
    </ReactFlowProvider>
  );
}
