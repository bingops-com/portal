import { useEffect, useMemo, useRef, useState } from 'react';
import { Search } from 'lucide-react';

export type Command = { label: string; hint: string; run: () => void };

const fold = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();

// Keyboard launcher (Ctrl+K): jump to a page, open an application, run an action.
export function Palette({ commands, onClose }: { commands: Command[]; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const matches = useMemo(() => {
    const words = fold(query).split(/\s+/).filter(Boolean);
    return commands.filter((c) => words.every((w) => fold(`${c.label} ${c.hint}`).includes(w))).slice(0, 12);
  }, [commands, query]);

  useEffect(() => {
    if (dialog.current && !dialog.current.open) dialog.current.showModal();
  }, []);
  useEffect(() => setActive(0), [query]);

  const run = (c?: Command) => {
    if (!c) return;
    onClose();
    c.run();
  };
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const step = e.key === 'ArrowDown' ? 1 : -1;
      setActive((a) => (matches.length ? (a + step + matches.length) % matches.length : 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      run(matches[active]);
    }
  };

  return (
    <dialog
      ref={dialog}
      className="palette"
      onClose={onClose}
      onClick={(e) => {
        if (e.target === dialog.current) onClose();
      }}
    >
      <label className="palette-input">
        <Search size={18} aria-hidden />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKey}
          placeholder="Aller à une page, ouvrir une application…"
          aria-label="Commande"
          role="combobox"
          aria-expanded
          aria-controls="palette-list"
          aria-activedescendant={matches[active] ? `palette-${active}` : undefined}
          autoComplete="off"
          spellCheck={false}
        />
      </label>
      <ul id="palette-list" role="listbox">
        {matches.map((c, i) => (
          <li key={c.hint + c.label} id={`palette-${i}`} role="option" aria-selected={i === active} onMouseMove={() => setActive(i)} onClick={() => run(c)}>
            <span>{c.label}</span>
            <small>{c.hint}</small>
          </li>
        ))}
        {matches.length === 0 && <li className="palette-empty">Aucune commande ne correspond à « {query} ».</li>}
      </ul>
    </dialog>
  );
}
