import { useEffect, useRef, useState, type ReactNode } from 'react';
import { X } from 'lucide-react';
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
  const [values, setValues] = useState<Record<string, string | boolean>>(() =>
    Object.fromEntries(fields.map((f) => [f.key, f.kind === 'bool' ? Boolean(widget.options?.[f.key]) : toText(f, widget.options?.[f.key])])),
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

  const set = (key: string, value: string | boolean) => setValues((v) => ({ ...v, [key]: value }));

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
