import { useCallback, useEffect, useRef, useState } from 'react';
import { api, type DataResponse, type Widget } from './api';

export type WidgetData<T> = DataResponse<T> & { loading: boolean; reload: () => void };

// Loads a widget's server data and keeps it fresh. In preview mode (while the
// layout is being edited) the unsaved options are sent instead of the id.
export function useWidgetData<T>(widget: Widget, refreshSeconds: number, preview: boolean): WidgetData<T> {
  const [state, setState] = useState<DataResponse<T> & { loading: boolean }>({ loading: true });
  const key = `${preview}|${widget.id}|${widget.type}|${JSON.stringify(widget.options ?? {})}`;
  const latest = useRef(widget);
  latest.current = widget;
  const lastLoad = useRef(0);
  const controller = useRef<AbortController | null>(null);

  const load = useCallback(() => {
    controller.current?.abort();
    const ctl = new AbortController();
    controller.current = ctl;
    lastLoad.current = Date.now();
    const w = latest.current;
    const call = preview ? api.preview<T>(w.type, w.options, ctl.signal) : api.data<T>(w.id, ctl.signal);
    call
      .then((res) => setState((prev) => ({ ...res, data: res.data ?? (res.error ? prev.data : undefined), loading: false })))
      .catch((err: Error) => {
        if (err.name === 'AbortError') return;
        setState((prev) => ({ ...prev, error: err.message, stale: prev.data !== undefined, loading: false }));
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  useEffect(() => {
    setState({ loading: true });
    load();
    const every = Math.max(refreshSeconds, 15) * 1000;
    const timer = setInterval(() => {
      if (!document.hidden) load();
    }, every);
    const onVisible = () => {
      if (!document.hidden && Date.now() - lastLoad.current > every) load();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
      controller.current?.abort();
    };
  }, [load, refreshSeconds]);

  return { ...state, reload: load };
}
