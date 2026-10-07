import { useEffect, useState } from 'react';
import { DndContext, DragOverlay, KeyboardSensor, PointerSensor, closestCorners, useDroppable, useSensor, useSensors, type DragEndEvent, type DragOverEvent, type DragStartEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { ArrowLeft, ArrowRight, ChevronDown, Plus, Trash2, Undo2 } from 'lucide-react';
import type { Column, Page, Widget } from '../api';
import { newId } from '../format';
import { registry } from '../widgets/registry';
import { AddWidgetDialog, EditWidgetDialog } from './Dialogs';
import { WidgetView } from './WidgetFrame';

const template = (columns: Column[]) => columns.map((c) => (c.size === 'small' ? 'minmax(0, var(--narrow))' : 'minmax(0, 1fr)')).join(' ');

// A `section` widget titles the widgets that follow it in its column, up to
// the next section, and lets the person fold them away.
function Section({ section, widgets }: { section: Widget; widgets: Widget[] }) {
  const key = `portal-section-${section.id}`;
  const [open, setOpen] = useState(() => {
    try {
      const saved = localStorage.getItem(key);
      if (saved) return saved === 'open';
    } catch {
      /* fall back to the configured default */
    }
    return !section.options?.collapsed;
  });
  const toggle = () => {
    setOpen(!open);
    try {
      localStorage.setItem(key, open ? 'closed' : 'open');
    } catch {
      /* the choice lasts for this visit */
    }
  };
  return (
    <section className="section" aria-label={section.title || 'Section'}>
      <h2 className="section-head">
        <button onClick={toggle} aria-expanded={open}>
          <ChevronDown size={16} aria-hidden className={open ? undefined : 'section-closed'} />
          {section.title || 'Section'}
          {!open && <span className="section-count">{widgets.length > 1 ? `${widgets.length} widgets` : widgets.length === 1 ? '1 widget' : 'vide'}</span>}
        </button>
      </h2>
      {open && widgets.map((w) => <WidgetView key={w.id} widget={w} preview={false} />)}
    </section>
  );
}

export function PageView({ page }: { page: Page }) {
  return (
    <div className="columns" style={{ gridTemplateColumns: template(page.columns) }}>
      {page.columns.map((col, i) => {
        const groups: { section?: Widget; widgets: Widget[] }[] = [{ widgets: [] }];
        for (const w of col.widgets) {
          if (w.type === 'section') groups.push({ section: w, widgets: [] });
          else groups[groups.length - 1].widgets.push(w);
        }
        return (
          <div key={i} className={`column column-${col.size}`}>
            {groups.map((g) =>
              g.section ? <Section key={g.section.id} section={g.section} widgets={g.widgets} /> : g.widgets.map((w) => <WidgetView key={w.id} widget={w} preview={false} />),
            )}
          </div>
        );
      })}
    </div>
  );
}

function SortableWidget({ widget, onEdit, onRemove }: { widget: Widget; onEdit: () => void; onRemove: () => void }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: widget.id });
  return (
    <div ref={setNodeRef} style={{ transform: CSS.Translate.toString(transform), transition }} className={isDragging ? 'dragging' : undefined}>
      <WidgetView widget={widget} preview edit={{ handle: { ...attributes, ...listeners }, onEdit, onRemove }} />
    </div>
  );
}

function EditColumn({ index, column, count, children, onChange, onMove, onRemove, onAdd }: {
  index: number;
  column: Column;
  count: number;
  children: React.ReactNode;
  onChange: (size: Column['size']) => void;
  onMove: (delta: number) => void;
  onRemove: () => void;
  onAdd: () => void;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: `col:${index}` });
  return (
    <div className={`column column-${column.size} column-edit${isOver ? ' column-over' : ''}`}>
      <div className="column-bar">
        <select value={column.size} onChange={(e) => onChange(e.target.value as Column['size'])} aria-label={`Largeur de la colonne ${index + 1}`}>
          <option value="small">Colonne étroite</option>
          <option value="full">Colonne large</option>
        </select>
        <button className="icon-btn" disabled={index === 0} onClick={() => onMove(-1)} aria-label="Déplacer la colonne vers la gauche">
          <ArrowLeft size={15} aria-hidden />
        </button>
        <button className="icon-btn" disabled={index === count - 1} onClick={() => onMove(1)} aria-label="Déplacer la colonne vers la droite">
          <ArrowRight size={15} aria-hidden />
        </button>
        <button
          className="icon-btn"
          disabled={count === 1 || column.widgets.length > 0}
          onClick={onRemove}
          aria-label="Supprimer la colonne"
          title={column.widgets.length > 0 ? 'Videz la colonne pour la supprimer' : 'Supprimer la colonne'}
        >
          <Trash2 size={15} aria-hidden />
        </button>
      </div>
      <div ref={setNodeRef} className="column-drop">
        {children}
        {column.widgets.length === 0 && <p className="empty">Déposez un widget ici.</p>}
      </div>
      <button className="btn btn-dashed" onClick={onAdd}>
        <Plus size={15} aria-hidden /> Ajouter un widget
      </button>
    </div>
  );
}

// Editable variant of a page: widgets can be dragged within and across
// columns; every change is reported through onChange and saved by the caller.
export function PageEditor({ page, onChange }: { page: Page; onChange: (page: Page) => void }) {
  const [dragging, setDragging] = useState<Widget | null>(null);
  const [adding, setAdding] = useState<number | null>(null);
  const [editing, setEditing] = useState<Widget | null>(null);
  // The last removed widget can be put back for a few seconds.
  const [removed, setRemoved] = useState<{ widget: Widget; column: number; at: number } | null>(null);
  useEffect(() => {
    if (!removed) return;
    const timer = setTimeout(() => setRemoved(null), 7000);
    return () => clearTimeout(timer);
  }, [removed]);
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }));

  const columns = page.columns;
  const setColumns = (next: Column[]) => onChange({ ...page, columns: next });
  const columnOf = (id: string) => (id.startsWith('col:') ? Number(id.slice(4)) : columns.findIndex((c) => c.widgets.some((w) => w.id === id)));
  const mapWidgets = (fn: (widgets: Widget[], col: number) => Widget[]) => setColumns(columns.map((c, i) => ({ ...c, widgets: fn(c.widgets, i) })));

  const onDragStart = (e: DragStartEvent) => {
    const id = String(e.active.id);
    setDragging(columns.flatMap((c) => c.widgets).find((w) => w.id === id) ?? null);
  };

  const onDragOver = (e: DragOverEvent) => {
    if (!e.over) return;
    const activeId = String(e.active.id);
    const overId = String(e.over.id);
    const from = columnOf(activeId);
    const to = columnOf(overId);
    if (from < 0 || to < 0 || from === to) return;
    const moved = columns[from].widgets.find((w) => w.id === activeId)!;
    const overIndex = columns[to].widgets.findIndex((w) => w.id === overId);
    mapWidgets((widgets, i) => {
      if (i === from) return widgets.filter((w) => w.id !== activeId);
      if (i === to) {
        const at = overIndex < 0 ? widgets.length : overIndex;
        return [...widgets.slice(0, at), moved, ...widgets.slice(at)];
      }
      return widgets;
    });
  };

  const onDragEnd = (e: DragEndEvent) => {
    setDragging(null);
    if (!e.over) return;
    const activeId = String(e.active.id);
    const overId = String(e.over.id);
    const col = columnOf(activeId);
    if (col < 0 || col !== columnOf(overId) || overId.startsWith('col:')) return;
    const from = columns[col].widgets.findIndex((w) => w.id === activeId);
    const to = columns[col].widgets.findIndex((w) => w.id === overId);
    if (from !== to) mapWidgets((widgets, i) => (i === col ? arrayMove(widgets, from, to) : widgets));
  };

  return (
    <>
      <DndContext sensors={sensors} collisionDetection={closestCorners} onDragStart={onDragStart} onDragOver={onDragOver} onDragEnd={onDragEnd} onDragCancel={() => setDragging(null)}>
        <div className="columns" style={{ gridTemplateColumns: template(columns) }}>
          {columns.map((col, i) => (
            <SortableContext key={i} items={col.widgets.map((w) => w.id)} strategy={verticalListSortingStrategy}>
              <EditColumn
                index={i}
                column={col}
                count={columns.length}
                onChange={(size) => setColumns(columns.map((c, j) => (j === i ? { ...c, size } : c)))}
                onMove={(delta) => setColumns(arrayMove(columns, i, i + delta))}
                onRemove={() => setColumns(columns.filter((_, j) => j !== i))}
                onAdd={() => setAdding(i)}
              >
                {col.widgets.map((w) => (
                  <SortableWidget key={w.id} widget={w} onEdit={() => setEditing(w)} onRemove={() => {
                      setRemoved({ widget: w, column: i, at: col.widgets.indexOf(w) });
                      mapWidgets((widgets) => widgets.filter((x) => x.id !== w.id));
                    }}
                  />
                ))}
              </EditColumn>
            </SortableContext>
          ))}
        </div>
        <DragOverlay dropAnimation={{ duration: 280, easing: 'cubic-bezier(0.34, 1.56, 0.64, 1)' }}>{dragging && <div className="drag-ghost">{dragging.title || registry[dragging.type]?.label || dragging.type}</div>}</DragOverlay>
      </DndContext>

      {removed && (
        <p className="toast toast-fixed" role="status">
          « {removed.widget.title || registry[removed.widget.type]?.label || removed.widget.type} » retiré.
          <button
            className="btn"
            onClick={() => {
              mapWidgets((widgets, i) => (i === Math.min(removed.column, columns.length - 1) ? [...widgets.slice(0, removed.at), removed.widget, ...widgets.slice(removed.at)] : widgets));
              setRemoved(null);
            }}
          >
            <Undo2 size={15} aria-hidden /> Annuler
          </button>
        </p>
      )}

      {adding !== null && (
        <AddWidgetDialog
          onClose={() => setAdding(null)}
          onPick={(type) => {
            const widget: Widget = { id: newId(type), type, options: structuredClone(registry[type].defaults) };
            mapWidgets((widgets, i) => (i === adding ? [...widgets, widget] : widgets));
            setAdding(null);
            if (registry[type].fields.length > 0) setEditing(widget);
          }}
        />
      )}
      {editing && (
        <EditWidgetDialog
          widget={editing}
          onClose={() => setEditing(null)}
          onSave={(next) => {
            mapWidgets((widgets) => widgets.map((w) => (w.id === next.id ? next : w)));
            setEditing(null);
          }}
        />
      )}
    </>
  );
}
