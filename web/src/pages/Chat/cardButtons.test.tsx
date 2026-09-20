import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { CardBlock } from './markdownBlocks';

// The Web client renders card buttons with `btn.text` / `btn.type` (lowercase).
// The Go side puts them on the wire via a plain json.Marshal, so the field
// casing is a real cross-language contract: when ButtonOption lacked its JSON
// tags the payload said "Text"/"Data", every label came back undefined, and
// the permission prompt rendered as unlabelled pills — nothing distinguished
// 允许 from 拒绝. These tests pin the rendering half of that contract.
describe('card action buttons', () => {
  const card = (buttons: any[], layout?: string) => ({
    header: { title: '权限请求', color: 'orange' },
    elements: [{ type: 'actions', layout, buttons }],
  });

  it('renders each button label from the lowercase wire field', () => {
    const html = renderToStaticMarkup(
      <CardBlock
        card={card([
          { text: '允许', btn_type: 'primary', value: 'perm:allow' },
          { text: '拒绝', btn_type: 'danger', value: 'perm:deny' },
        ])}
        onAction={() => {}}
      />,
    );
    expect(html).toContain('允许');
    expect(html).toContain('拒绝');
  });

  it('does not render an empty button when labels are present', () => {
    const html = renderToStaticMarkup(
      <CardBlock
        card={card([{ text: '允许所有 (本次会话)', btn_type: 'default', value: 'perm:allow_all' }])}
        onAction={() => {}}
      />,
    );
    // A label-less button is the exact bug being guarded against.
    expect(html).not.toMatch(/<button[^>]*>\s*<\/button>/);
  });

  it('gives a distinct style per btn_type so allow and deny are distinguishable', () => {
    const html = renderToStaticMarkup(
      <CardBlock
        card={card([
          { text: 'allow', btn_type: 'primary', value: 'perm:allow' },
          { text: 'deny', btn_type: 'danger', value: 'perm:deny' },
        ])}
        onAction={() => {}}
      />,
    );
    const classes = [...html.matchAll(/<button class="([^"]*)"/g)].map((m) => m[1]);
    expect(classes).toHaveLength(2);
    expect(classes[0]).not.toBe(classes[1]);
  });

  it('splits equal_columns rows evenly across the available width', () => {
    const html = renderToStaticMarkup(
      <CardBlock
        card={card(
          [
            { text: '允许', btn_type: 'primary', value: 'perm:allow' },
            { text: '拒绝', btn_type: 'danger', value: 'perm:deny' },
          ],
          'equal_columns',
        )}
        onAction={() => {}}
      />,
    );
    // Every button in an equal_columns row grows to share the row width.
    const classes = [...html.matchAll(/<button class="([^"]*)"/g)].map((m) => m[1]);
    expect(classes).toHaveLength(2);
    for (const c of classes) expect(c).toContain('flex-1');
  });

  it('leaves row layout at natural width', () => {
    const html = renderToStaticMarkup(
      <CardBlock
        card={card([{ text: '允许所有', btn_type: 'default', value: 'perm:allow_all' }], 'row')}
        onAction={() => {}}
      />,
    );
    const classes = [...html.matchAll(/<button class="([^"]*)"/g)].map((m) => m[1]);
    expect(classes[0]).not.toContain('flex-1');
  });
});
