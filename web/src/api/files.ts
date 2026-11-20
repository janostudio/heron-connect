import api from './client';
import type { PreviewChunk } from '@/components/TextPreview';

// File preview transport.
//
// Two things the generic client cannot express: a HEAD probe (the size has to be
// known *before* deciding whether to download) and a sliced read (the preview
// never wants the whole file). Both are small enough to live here rather than
// widening the client's API.

const API_BASE = '/api/v1';

export interface FileHead {
  size: number;
  contentType: string;
}

function authHeaders(): Record<string, string> {
  const token = api.getToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function send(path: string, init: RequestInit, withAuth: boolean): Promise<Response> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: { ...(init.headers as Record<string, string> | undefined), ...(withAuth ? authHeaders() : {}) },
  });
  if (withAuth && res.status === 401) {
    // Reuse the client's 401 handling (it marks the token refused and drops the
    // session) instead of duplicating that state here.
    void api.get('/status').catch(() => {});
    throw new Error('Unauthorized');
  }
  return res;
}

/** Cheap probe: how big is this file, and what type does the server call it. */
export async function headFile(path: string): Promise<FileHead> {
  const res = await send(path, { method: 'HEAD', cache: 'no-store' }, true);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const size = Number(res.headers.get('Content-Length') || '0');
  return {
    size: Number.isFinite(size) ? size : 0,
    contentType: res.headers.get('Content-Type') || 'application/octet-stream',
  };
}

function chunkPath(path: string, offset: number, limit: number): string {
  const sep = path.includes('?') ? '&' : '?';
  return `${path}${sep}preview=1&offset=${offset}&limit=${limit}`;
}

function toChunk(res: Response, bytes: Uint8Array): PreviewChunk {
  const num = (name: string, fallback: number) => {
    const raw = res.headers.get(name);
    const parsed = raw === null ? NaN : Number(raw);
    return Number.isFinite(parsed) ? parsed : fallback;
  };
  return {
    bytes,
    totalSize: num('X-Preview-Total-Size', 0),
    offset: num('X-Preview-Offset', 0),
    returned: num('X-Preview-Returned', bytes.length),
    truncated: res.headers.get('X-Preview-Truncated') === '1',
  };
}

/** Read one slice of a project file (authenticated). */
export async function readPreviewChunk(
  path: string,
  offset: number,
  limit: number,
  signal?: AbortSignal,
): Promise<PreviewChunk> {
  const res = await send(chunkPath(path, offset, limit), { method: 'GET', cache: 'no-store', signal }, true);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return toChunk(res, new Uint8Array(await res.arrayBuffer()));
}

/** Read one slice of a shared file (anonymous, token in the path). */
export async function readSharedPreviewChunk(
  sharePath: string,
  offset: number,
  limit: number,
  signal?: AbortSignal,
): Promise<PreviewChunk> {
  const res = await send(chunkPath(sharePath, offset, limit), { method: 'GET', cache: 'no-store', signal }, false);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return toChunk(res, new Uint8Array(await res.arrayBuffer()));
}
