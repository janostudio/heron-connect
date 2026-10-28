import { describe, it, expect } from 'vitest';
import { emptySlice, applyFrame, type SessionSlice } from './chatSessionsCore';
import type { ChatMsg } from './chatMessage';
import type { BridgeIncoming } from '@/hooks/useBridgeSocket';

// ── Heron 的工具帧 → 工具卡片 ─────────────────────────────────
//
// heron 适配器把 Team 内部的工具调用以独立的 bridge 帧发出来（type=tool_use /
// tool_result），而它的预览正文是纯文本、不带结构化卡片。web 端以前没有这两个
// 分支，帧被静默丢弃，用户只看到"已转给执行侧…"这类话术，看不到工具。
// 这里固定的是：帧到达时补出卡片；后端已经给了卡片时不重复追加。

const streaming = (): SessionSlice => ({
  ...emptySlice(),
  messages: [
    { id: 'u1', role: 'user', content: 'heron-ai 有多少 commit' },
    { id: 'p1', role: 'assistant', content: '已转给执行侧统计…', format: 'markdown', streaming: true, previewHandle: 'h1' } as ChatMsg,
  ],
});

const frame = (f: Record<string, unknown>): BridgeIncoming => f as BridgeIncoming;

describe('tool frames become progress-card entries', () => {
  it('creates a card on the streaming row when the preview had none', () => {
    let slice = streaming();
    slice = applyFrame(slice, frame({ type: 'tool_use', name: 'Bash', tool_use_id: 'tc-1', input: 'git rev-list --count HEAD' }));

    const row = slice.messages[1];
    expect(row.progressCard?.items).toHaveLength(1);
    expect(row.progressCard?.items[0]).toMatchObject({
      kind: 'tool_use',
      tool: 'Bash',
      id: 'tc-1',
      text: 'git rev-list --count HEAD',
    });
    // 预览正文仍在，卡片是附加信息而不是替换。
    expect(row.content).toBe('已转给执行侧统计…');
  });

  it('appends the matching tool_result to the same card', () => {
    let slice = streaming();
    slice = applyFrame(slice, frame({ type: 'tool_use', name: 'Bash', tool_use_id: 'tc-1', input: 'git rev-list --count HEAD' }));
    slice = applyFrame(slice, frame({
      type: 'tool_result', name: 'Bash', tool_use_id: 'tc-1', content: '299', status: 'completed', exit_code: 0,
    }));

    const items = slice.messages[1].progressCard?.items ?? [];
    expect(items).toHaveLength(2);
    expect(items[1]).toMatchObject({ kind: 'tool_result', tool: 'Bash', id: 'tc-1', text: '299' });
  });

  it('ignores the frame when the backend already shipped a structured card', () => {
    const slice = streaming();
    slice.messages[1] = {
      ...slice.messages[1],
      progressCard: { items: [{ kind: 'tool_use', tool: 'Bash', id: 'tc-1', text: 'git log' }] },
    };

    const next = applyFrame(slice, frame({ type: 'tool_use', name: 'Bash', tool_use_id: 'tc-1', input: 'git log' }));

    expect(next.messages[1].progressCard?.items).toHaveLength(1);
  });

  it('creates a row when no streaming message exists yet', () => {
    const slice: SessionSlice = { ...emptySlice(), messages: [{ id: 'u1', role: 'user', content: 'hi' }] };
    const next = applyFrame(slice, frame({ type: 'tool_use', name: 'Read', tool_use_id: 'tc-9', input: 'README.md' }));

    expect(next.messages).toHaveLength(2);
    const row = next.messages[1];
    expect(row.role).toBe('assistant');
    expect(row.progressCard?.items[0]).toMatchObject({ kind: 'tool_use', tool: 'Read' });
  });

  it('drops frames that carry neither a tool name nor text', () => {
    const slice = streaming();
    expect(applyFrame(slice, frame({ type: 'tool_use' }))).toBe(slice);
  });
});
