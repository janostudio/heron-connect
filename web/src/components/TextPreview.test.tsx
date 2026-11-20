/**
 * @vitest-environment jsdom
 */
// The windowed text viewer is the piece that keeps large files from freezing the
// tab, so its two load-bearing properties are tested here: slices stitch back
// into the exact original text (including multi-byte characters split across a
// slice boundary), and only the visible lines are ever mounted.

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import TextPreview, { type PreviewChunk } from './TextPreview';

const LABELS = { loading: 'Loading…', loadMore: 'Load more', download: 'Download', failed: 'Failed' };

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  const r = root;
  if (r) act(() => { r.unmount(); });
  container.remove();
  root = null;
  delete (globalThis as any).IS_REACT_ACT_ENVIRONMENT;
});

/** Serves a byte array slice by slice, the way the endpoint does. */
function serve(bytes: Uint8Array, chunkSize: number) {
  const offsets: number[] = [];
  const readChunk = async (offset: number, limit: number): Promise<PreviewChunk> => {
    offsets.push(offset);
    const size = Math.max(0, Math.min(limit, chunkSize, bytes.length - offset));
    const end = offset + size;
    return {
      bytes: bytes.slice(offset, end),
      totalSize: bytes.length,
      offset,
      returned: size,
      truncated: end < bytes.length,
    };
  };
  return { readChunk, offsets };
}

async function mount(readChunk: (offset: number, limit: number) => Promise<PreviewChunk>, totalSize: number) {
  root = createRoot(container);
  await act(async () => {
    root!.render(<TextPreview totalSize={totalSize} readChunk={readChunk} labels={LABELS} />);
    await new Promise((r) => setTimeout(r, 0));
  });
}

const mountedLines = () => Array.from(container.querySelectorAll('[data-testid="preview-line"]'));

async function loadAll() {
  for (let i = 0; i < 100; i++) {
    const more = Array.from(container.querySelectorAll('button'))
      .find((b) => b.textContent === LABELS.loadMore) as HTMLButtonElement | undefined;
    if (!more) return;
    await act(async () => {
      more.click();
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

describe('TextPreview', () => {
  it('stitches multi-byte characters split across slice boundaries', async () => {
    const original = '中文内容测试\n第二行内容\n第三行内容';
    const bytes = new TextEncoder().encode(original);
    // 4-byte slices cut every CJK character (3 bytes each) mid-sequence.
    const { readChunk, offsets } = serve(bytes, 4);

    await mount(readChunk, bytes.length);
    await loadAll();

    expect(offsets.length).toBeGreaterThan(2);
    expect(container.textContent).not.toContain('\uFFFD'); // no replacement chars
    expect(container.textContent).toContain('中文内容测试');
    expect(container.textContent).toContain('第三行内容');
  });

  it('keeps the trailing partial line out of the view until it is complete', async () => {
    const bytes = new TextEncoder().encode('first\nsecond');
    const { readChunk } = serve(bytes, 7); // splits inside "second"

    await mount(readChunk, bytes.length);
    expect(container.textContent).toContain('first');
    expect(container.textContent).not.toContain('second');

    await loadAll();
    expect(container.textContent).toContain('second');
  });

  it('mounts only the visible window of a long file', async () => {
    const total = 2000;
    const text = Array.from({ length: total }, (_, i) => `row-${String(i + 1).padStart(4, '0')}`).join('\n');
    const bytes = new TextEncoder().encode(text);
    const { readChunk } = serve(bytes, 1024 * 1024);

    await mount(readChunk, bytes.length);

    const rendered = mountedLines();
    expect(rendered.length).toBeGreaterThan(0);
    expect(rendered.length).toBeLessThan(150); // 480px viewport / 20px + overscan
    expect(container.textContent).toContain('row-0001');
    expect(container.textContent).not.toContain(`row-${String(total).padStart(4, '0')}`);
  });

  it('cuts a single pathologically long line instead of laying it out', async () => {
    const bytes = new TextEncoder().encode('x'.repeat(5000));
    const { readChunk } = serve(bytes, 1024 * 1024);

    await mount(readChunk, bytes.length);

    const line = mountedLines()[0];
    expect(line).toBeTruthy();
    expect(line.textContent!.length).toBeLessThan(2100);
    expect(line.textContent).toContain('…');
  });

  it('stops offering "load more" once everything is loaded', async () => {
    const bytes = new TextEncoder().encode('only line');
    const { readChunk } = serve(bytes, 4);

    await mount(readChunk, bytes.length);
    expect(container.textContent).toContain(LABELS.loadMore);

    await loadAll();
    expect(container.textContent).not.toContain(LABELS.loadMore);
  });

  it('reports a failed slice without discarding what is already shown', async () => {
    let call = 0;
    const readChunk = async (offset: number): Promise<PreviewChunk> => {
      call++;
      if (call === 1) {
        return { bytes: new TextEncoder().encode('kept line\n'), totalSize: 100, offset, returned: 10, truncated: true };
      }
      throw new Error('network down');
    };

    await mount(readChunk, 100);
    await loadAll();

    expect(container.textContent).toContain('kept line');
    expect(container.textContent).toContain(LABELS.failed);
    expect(container.textContent).toContain('network down');
  });
});
