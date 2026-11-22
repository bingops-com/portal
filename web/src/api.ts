export type Options = Record<string, any>;
export type Widget = { id: string; type: string; title?: string; options?: Options };
export type Column = { size: 'small' | 'full'; widgets: Widget[] };
export type Page = { name: string; slug: string; group?: string; icon?: string; columns: Column[] };
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
export type GameChange = { expectedRevision: string; requestId: string; state?: 'Running' | 'Suspended'; version?: string };
export type GameResult = { kind: 'preview' | 'commit' | 'pull_request' | 'unchanged'; revision: string; url?: string; diff: string };
export type GamesResponse = { desired: { revision: string; writable: boolean; versions: string[]; rooms: { name: string; state: 'Running' | 'Suspended'; version: string; endpoint?: string }[] }; observed: { rooms: Record<string,{ generation: number; observedGeneration: number; state: string; version: string; phase: string; message?: string; backupVerified: boolean }>; argoRevision: string; argoSync: string }; canWrite: boolean; observationError?: string };

export class APIError extends Error { constructor(message: string, public status: number) { super(message); } }
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch (err) {
    if ((err as Error).name === 'AbortError') throw err;
    throw new Error('Le serveur du portail ne répond pas.');
  }
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new APIError(body?.error ?? `Le serveur a répondu ${res.status}.`,res.status);
  return body as T;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
});

export const api = {
  games: () => request<GamesResponse>('/api/games'),
  previewGame: (name: string, change: GameChange) => request<GameResult>(`/api/games/${encodeURIComponent(name)}/preview`,json('POST',change)),
  changeGame: (name: string, change: GameChange) => request<GameResult>(`/api/games/${encodeURIComponent(name)}/changes`,json('POST',change)),
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
