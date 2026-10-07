import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ArrowDown, ArrowUp, Plus, Trash2, X } from 'lucide-react';
import { parse, stringify } from 'yaml';
import type { Options, Widget } from '../api';
import { catalog, registry, type Field } from '../widgets/registry';

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (dialog && !dialog.open) dialog.showModal();
  }, []);
  return (
    <dialog
      ref={ref}
      className="modal"
      onClose={onClose}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      <header>
        <h2>{title}</h2>
        <button className="icon-btn" onClick={onClose} aria-label="Fermer">
          <X size={18} aria-hidden />
        </button>
      </header>
      {children}
    </dialog>
  );
}

export function AddWidgetDialog({ onPick, onClose }: { onPick: (type: string) => void; onClose: () => void }) {
  const groups = ['Ops', 'Informations', 'Outils'] as const;
  return (
    <Modal title="Ajouter un widget" onClose={onClose}>
      <div className="modal-body">
        {groups.map((g) => (
          <div key={g} className="catalog-group">
            <h3>{g}</h3>
            <div className="catalog">
              {catalog
                .filter((m) => m.group === g)
                .map((m) => (
                  <button key={m.type} onClick={() => onPick(m.type)}>
                    <strong>{m.label}</strong>
                    <span>{m.description}</span>
                  </button>
                ))}
            </div>
          </div>
        ))}
      </div>
    </Modal>
  );
}

type Entry = Record<string, any>;

// Editor for a list of entries (feeds, links, metrics…): one card per entry
// with its fields, reorder and remove controls. Lists nest.
function ListField({ field, value, onChange }: { field: Field; value: Entry[]; onChange: (next: Entry[]) => void }) {
  const items = field.item ?? [];
  const set = (i: number, key: string, v: unknown) => onChange(value.map((e, j) => (j === i ? { ...e, [key]: v } : e)));
  const move = (i: number, delta: number) => {
    const next = [...value];
    [next[i], next[i + delta]] = [next[i + delta], next[i]];
    onChange(next);
  };
  return (
    <div className="list-field">
      {value.map((entry, i) => (
        <fieldset key={i} className="list-entry">
          <legend className="sr-only">
            {field.label}, entrée {i + 1}
          </legend>
          <div className="list-grid">
            {items.map((f) =>
              f.kind === 'list' ? (
                <div key={f.key} className="list-wide list-nested">
                  <span className="list-label">{f.label}</span>
                  <ListField field={f} value={Array.isArray(entry[f.key]) ? entry[f.key] : []} onChange={(v) => set(i, f.key, v)} />
                </div>
              ) : (
                <label key={f.key} className={f.wide ? 'list-wide' : undefined}>
                  <span>{f.label}</span>
                  {f.kind === 'select' ? (
                    <select value={String(entry[f.key] ?? '')} onChange={(e) => set(i, f.key, e.target.value)}>
                      <option value="">Par défaut</option>
                      {f.choices?.map(([v, label]) => (
                        <option key={v} value={v}>
                          {label}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      value={String(entry[f.key] ?? '')}
                      inputMode={f.kind === 'number' ? 'decimal' : undefined}
                      placeholder={f.placeholder}
                      onChange={(e) => set(i, f.key, e.target.value)}
                      spellCheck={false}
                    />
                  )}
                  {f.help && <small>{f.help}</small>}
                </label>
              ),
            )}
          </div>
          <div className="list-tools">
            <button type="button" className="icon-btn" disabled={i === 0} onClick={() => move(i, -1)} aria-label="Monter">
              <ArrowUp size={15} aria-hidden />
            </button>
            <button type="button" className="icon-btn" disabled={i === value.length - 1} onClick={() => move(i, 1)} aria-label="Descendre">
              <ArrowDown size={15} aria-hidden />
            </button>
            <button type="button" className="icon-btn" onClick={() => onChange(value.filter((_, j) => j !== i))} aria-label="Supprimer">
              <Trash2 size={15} aria-hidden />
            </button>
          </div>
        </fieldset>
      ))}
      <button type="button" className="btn btn-dashed" onClick={() => onChange([...value, {}])}>
        <Plus size={15} aria-hidden /> Ajouter {field.itemName ?? 'une entrée'}
      </button>
    </div>
  );
}

// Entries as stored in the options: a bare string (a feed URL) becomes {url}.
const toEntries = (value: unknown): Entry[] => (Array.isArray(value) ? value.map((e) => (typeof e === 'string' ? { url: e } : { ...(e as Entry) })) : []);

// Drops empty entries and empty keys, and turns number fields into numbers.
// Returns null when a number field does not hold a number.
function cleanEntries(field: Field, entries: Entry[]): Entry[] | null {
  const out: Entry[] = [];
  for (const entry of entries) {
    const next: Entry = { ...entry };
    for (const f of field.item ?? []) {
      const raw = next[f.key];
      if (f.kind === 'list') {
        const nested = cleanEntries(f, Array.isArray(raw) ? raw : []);
        if (nested === null) return null;
        next[f.key] = nested;
      } else if (raw === undefined || raw === null || String(raw).trim() === '') {
        delete next[f.key];
      } else if (f.kind === 'number') {
        const n = Number(String(raw).replace(',', '.'));
        if (!Number.isFinite(n)) return null;
        next[f.key] = n;
      } else {
        next[f.key] = String(raw).trim();
      }
    }
    const filled = Object.entries(next).some(([, v]) => (Array.isArray(v) ? v.length > 0 : v !== undefined));
    if (filled) out.push(next);
  }
  return out;
}

const toText = (field: Field, value: unknown): string => {
  if (value === undefined || value === null) return '';
  if (field.kind === 'lines') return Array.isArray(value) ? value.join('\n') : String(value);
  if (field.kind === 'yaml') return stringify(value, { lineWidth: 0 }).trimEnd();
  return String(value);
};

export function EditWidgetDialog({ widget, onSave, onClose }: { widget: Widget; onSave: (w: Widget) => void; onClose: () => void }) {
  const meta = registry[widget.type];
  const fields = meta?.fields ?? [];
  const [title, setTitle] = useState(widget.title ?? '');
  const [values, setValues] = useState<Record<string, any>>(() =>
    Object.fromEntries(
      fields.map((f) => [f.key, f.kind === 'bool' ? Boolean(widget.options?.[f.key]) : f.kind === 'list' ? toEntries(widget.options?.[f.key]) : toText(f, widget.options?.[f.key])]),
    ),
  );
  const [errors, setErrors] = useState<Record<string, string>>({});

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    // Options without a form field (set by hand in YAML) are kept as they are.
    const options: Options = { ...(widget.options ?? {}) };
    const problems: Record<string, string> = {};
    for (const f of fields) {
      const raw = values[f.key];
      if (f.kind === 'bool') {
        options[f.key] = Boolean(raw);
        continue;
      }
      if (f.kind === 'list') {
        const entries = cleanEntries(f, raw);
        if (entries === null) problems[f.key] = 'Un seuil ou un nombre est mal saisi.';
        else options[f.key] = entries;
        continue;
      }
      const text = String(raw).trim();
      if (text === '') {
        delete options[f.key];
      } else if (f.kind === 'number') {
        const n = Number(text);
        if (Number.isFinite(n)) options[f.key] = n;
        else problems[f.key] = 'Saisissez un nombre.';
      } else if (f.kind === 'lines') {
        options[f.key] = text.split('\n').map((l) => l.trim()).filter(Boolean);
      } else if (f.kind === 'yaml') {
        try {
          options[f.key] = parse(text);
        } catch (err) {
          problems[f.key] = `YAML invalide : ${(err as Error).message.split('\n')[0]}`;
        }
      } else {
        options[f.key] = text;
      }
    }
    setErrors(problems);
    if (Object.keys(problems).length === 0) onSave({ ...widget, title: title.trim() || undefined, options });
  };

  const set = (key: string, value: unknown) => setValues((v) => ({ ...v, [key]: value }));

  return (
    <Modal title={`Régler « ${meta?.label ?? widget.type} »`} onClose={onClose}>
      <form onSubmit={submit}>
        <div className="modal-body form">
          <label>
            <span>Titre</span>
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={meta?.label} />
          </label>
          {fields.map((f) => {
            const id = `field-${f.key}`;
            const value = values[f.key];
            if (f.kind === 'list') {
              return (
                <div key={f.key} className="form-group">
                  <span className="form-label">{f.label}</span>
                  <ListField field={f} value={value} onChange={(v) => set(f.key, v)} />
                  {f.help && <small>{f.help}</small>}
                  {errors[f.key] && <small className="field-error">{errors[f.key]}</small>}
                </div>
              );
            }
            return (
              <label key={f.key} htmlFor={id} className={f.kind === 'bool' ? 'check' : undefined}>
                <span>{f.label}</span>
                {f.kind === 'bool' && <input id={id} type="checkbox" checked={Boolean(value)} onChange={(e) => set(f.key, e.target.checked)} />}
                {(f.kind === 'text' || f.kind === 'number') && (
                  <input id={id} value={String(value)} inputMode={f.kind === 'number' ? 'numeric' : undefined} placeholder={f.placeholder} onChange={(e) => set(f.key, e.target.value)} spellCheck={false} />
                )}
                {f.kind === 'select' && (
                  <select id={id} value={String(value)} onChange={(e) => set(f.key, e.target.value)}>
                    {!f.choices?.some(([v]) => v === value) && <option value={String(value)}>{String(value) || 'Par défaut'}</option>}
                    {f.choices?.map(([v, label]) => (
                      <option key={v} value={v}>
                        {label}
                      </option>
                    ))}
                  </select>
                )}
                {(f.kind === 'lines' || f.kind === 'yaml') && (
                  <textarea id={id} className={f.kind === 'yaml' ? 'code' : undefined} value={String(value)} rows={f.kind === 'yaml' ? 9 : 4} onChange={(e) => set(f.key, e.target.value)} spellCheck={false} />
                )}
                {f.help && <small>{f.help}</small>}
                {errors[f.key] && <small className="field-error">{errors[f.key]}</small>}
              </label>
            );
          })}
        </div>
        <footer>
          <button type="button" className="btn" onClick={onClose}>
            Annuler
          </button>
          <button type="submit" className="btn btn-primary">
            Appliquer
          </button>
        </footer>
      </form>
    </Modal>
  );
}
