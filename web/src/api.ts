export type Options = Record<string, any>;
export type Widget = { id: string; type: string; title?: string; options?: Options };
export type Column = { size: 'small' | 'full'; widgets: Widget[] };
export type Page = { name: string; slug: string; group?: string; columns: Column[] };
export type PortalConfig = {
  title: string;
  theme?: string;
  pages: Page[];
  source: 'yaml' | 'custom';
  readOnly: boolean;
  // When true, editing needs a login; `user` is set once logged in.
  loginRequired: boolean;
  user?: string;
};
export type DataResponse<T> = { data?: T; error?: string; fetchedAt?: string; stale?: boolean };
export type SummaryItem = { label: string; state: 'ok' | 'warn' | 'down' | 'unknown'; detail: string; type?: string; since?: string };

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch (err) {
    if ((err as Error).name === 'AbortError') throw err;
    throw new Error('Le serveur du portail ne répond pas.');
  }
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new Error(body?.error ?? `Le serveur a répondu ${res.status}.`);
  return body as T;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
});

export const api = {
  config: () => request<PortalConfig>('/api/config'),
  saveLayout: (pages: Page[]) => request<PortalConfig>('/api/layout', json('PUT', { pages })),
  resetLayout: () => request<PortalConfig>('/api/layout', { method: 'DELETE' }),
  summary: (signal?: AbortSignal) => request<{ items: SummaryItem[] }>('/api/summary', { signal }),
  data: <T,>(id: string, signal?: AbortSignal) =>
    request<DataResponse<T>>(`/api/data/${encodeURIComponent(id)}`, { signal }),
  detail: <T,>(id: string, params: Record<string, string>) => request<T>(`/api/detail/${encodeURIComponent(id)}?${new URLSearchParams(params)}`),
  preview: <T,>(type: string, options: Options | undefined, signal?: AbortSignal) =>
    request<DataResponse<T>>('/api/preview', { ...json('POST', { type, options: options ?? {} }), signal }),
};
