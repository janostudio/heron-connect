import { describe, it, expect, vi, beforeEach } from 'vitest';

// The mermaid package is stubbed: it pulls in a DOM-heavy renderer we do not
// want (or need) in the node test environment, and the behaviour under test is
// our own scheduling — caching, theme re-initialisation, error handling, id
// uniqueness — not mermaid's drawing.
const initialize = vi.fn();
const parse = vi.fn(async (_code: string) => {});
const render = vi.fn(async (id: string, _code: string) => ({ svg: `<svg id="${id}"/>`, bindFunctions: undefined }));

vi.mock('mermaid', () => ({
  default: { initialize, parse, render },
}));

// Imported after the mock so the module picks up the stub.
const { renderMermaid, clearMermaidCache, } = await import('./mermaidRenderer');

const CODE = 'graph TD; A-->B;';

describe('renderMermaid', () => {
  beforeEach(() => {
    clearMermaidCache();
    initialize.mockClear();
    parse.mockClear();
    render.mockClear();
    parse.mockImplementation(async () => {});
    render.mockImplementation(async (id: string) => ({ svg: `<svg id="${id}"/>`, bindFunctions: undefined }));
  });

  it('renders and returns the svg', async () => {
    const r = await renderMermaid(CODE, 'light');
    expect(r).toEqual({ svg: expect.stringContaining('<svg') });
    expect(render).toHaveBeenCalledTimes(1);
  });

  it('serves a repeated identical diagram from cache without re-rendering', async () => {
    await renderMermaid(CODE, 'light');
    await renderMermaid(CODE, 'light');
    expect(render).toHaveBeenCalledTimes(1);
  });

  it('treats the same source under a different theme as a separate entry', async () => {
    await renderMermaid(CODE, 'light');
    await renderMermaid(CODE, 'dark');
    expect(render).toHaveBeenCalledTimes(2);
  });

  it('re-initialises once per theme and clears the cache on a theme switch', async () => {
    await renderMermaid(CODE, 'light');
    expect(initialize).toHaveBeenCalledTimes(1);
    await renderMermaid(CODE, 'dark');       // theme change → re-initialise
    expect(initialize).toHaveBeenCalledTimes(2);
    await renderMermaid(CODE, 'dark');       // same theme → no re-init
    expect(initialize).toHaveBeenCalledTimes(2);

    // Back to light: its cache entry was dropped by the dark switch, so this
    // must render again rather than serve a stale light entry.
    const before = render.mock.calls.length;
    await renderMermaid(CODE, 'light');
    expect(render.mock.calls.length).toBe(before + 1);
  });

  it('returns an error and never renders when the source fails to parse', async () => {
    parse.mockRejectedValueOnce(new Error('Parse error on line 1'));
    const r = await renderMermaid('not a diagram', 'light');
    expect(r).toEqual({ error: expect.stringContaining('Parse error') });
    expect(render).not.toHaveBeenCalled();
  });

  it('returns an error instead of throwing when rendering fails', async () => {
    render.mockRejectedValueOnce(new Error('boom'));
    const r = await renderMermaid(CODE, 'light');
    expect(r).toEqual({ error: 'boom' });
  });

  it('uses a unique id per render call', async () => {
    await renderMermaid('graph TD; A-->B;', 'light');
    await renderMermaid('graph TD; A-->C;', 'light');
    const [first] = render.mock.calls[0];
    const [second] = render.mock.calls[1];
    expect(first).not.toBe(second);
  });
});
