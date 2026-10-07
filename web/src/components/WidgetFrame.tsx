import { Component, createContext, useContext, type ReactNode } from 'react';
import { GripVertical, Maximize2, Pencil, RefreshCw, Trash2 } from 'lucide-react';
import type { Widget } from '../api';
import { clockTime } from '../format';
import { useWidgetData } from '../useData';
import { registry, type WidgetMeta } from '../widgets/registry';

class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? <p className="problem">Ce widget n’a pas pu afficher ses données. Rechargez la page.</p> : this.props.children;
  }
}

// Opens a widget in large. Null where that makes no sense (editing, or
// already inside the large view).
export const FocusContext = createContext<((widget: Widget) => void) | null>(null);

// Loading placeholders shaped like the content that is about to arrive.
const skeletons: Record<string, string> = {
  prometheus: 'stats', kubernetes: 'facts', gameserver: 'facts', incidents: 'facts', videos: 'grid', weather: 'facts', topology: 'grid',
};

function Skeleton({ type }: { type: string }) {
  const kind = skeletons[type] ?? 'rows';
  const count = kind === 'grid' ? 4 : kind === 'stats' ? 3 : kind === 'facts' ? 3 : 4;
  return (
    <div className={`skeleton skeleton-${kind}`} aria-label="Chargement">
      {Array.from({ length: count }, (_, i) => (
        <i key={i} />
      ))}
    </div>
  );
}

export type EditControls = {
  handle: Record<string, unknown>;
  onEdit: () => void;
  onRemove: () => void;
};

function Frame({ widget, meta, badge, edit, children }: { widget: Widget; meta?: WidgetMeta; badge?: string; edit?: EditControls; children: ReactNode }) {
  const title = widget.title || meta?.label || widget.type;
  const focus = useContext(FocusContext);
  return (
    <section className={`widget widget-${widget.type}`} aria-label={title}>
      <header className="widget-head">
        {edit && (
          <button className="icon-btn grip" aria-label={`Déplacer ${title}`} {...edit.handle}>
            <GripVertical size={16} aria-hidden />
          </button>
        )}
        <h2>{title}</h2>
        {badge && !edit && <span className="badge">{badge}</span>}
        {!edit && focus && meta && meta.refresh > 0 && (
          <button className="icon-btn widget-expand" onClick={() => focus(widget)} aria-label={`Agrandir ${title}`} title="Agrandir">
            <Maximize2 size={14} aria-hidden />
          </button>
        )}
        {edit && (
          <span className="widget-tools">
            <button className="icon-btn" onClick={edit.onEdit} aria-label={`Régler ${title}`} title="Régler">
              <Pencil size={15} aria-hidden />
            </button>
            <button className="icon-btn" onClick={edit.onRemove} aria-label={`Retirer ${title}`} title="Retirer">
              <Trash2 size={15} aria-hidden />
            </button>
          </span>
        )}
      </header>
      <div className="widget-body">
        <Boundary>{children}</Boundary>
      </div>
    </section>
  );
}

function DataWidget({ widget, meta, preview, edit }: { widget: Widget; meta: WidgetMeta; preview: boolean; edit?: EditControls }) {
  const state = useWidgetData<unknown>(widget, meta.refresh, preview);
  const View = meta.component;
  const has = state.data !== undefined && state.data !== null;
  return (
    <Frame widget={widget} meta={meta} edit={edit} badge={has && meta.badge ? meta.badge(state.data) : undefined}>
      {has && <View data={state.data} options={widget.options ?? {}} widgetId={widget.id} />}
      {!has && state.loading && <Skeleton type={widget.type} />}
      {state.error && (
        <p className={has ? 'note' : 'problem'}>
          {has ? `Actualisation impossible${state.fetchedAt ? `, données de ${clockTime(state.fetchedAt)}` : ''}. ` : ''}
          {state.error}
          <button className="retry" onClick={state.reload}>
            <RefreshCw size={13} aria-hidden /> Réessayer
          </button>
        </p>
      )}
    </Frame>
  );
}

export function WidgetView({ widget, preview, edit }: { widget: Widget; preview: boolean; edit?: EditControls }) {
  const meta = registry[widget.type];
  if (!meta) {
    return (
      <Frame widget={widget} edit={edit}>
        <p className="problem">Le type « {widget.type} » n’existe pas dans cette version du portail.</p>
      </Frame>
    );
  }
  if (meta.refresh === 0) {
    const View = meta.component;
    return (
      <Frame widget={widget} meta={meta} edit={edit}>
        <View data={undefined} options={widget.options ?? {}} widgetId={widget.id} />
      </Frame>
    );
  }
  return <DataWidget widget={widget} meta={meta} preview={preview} edit={edit} />;
}
