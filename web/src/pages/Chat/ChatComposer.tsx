import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { FileText, Paperclip, Send, Slash, Square, WifiOff, Loader2, X, Zap } from 'lucide-react';
import { cn } from '@/lib/utils';
import CommandPalette, { type SlashCommand } from './CommandPalette';
import type { PickItem } from './chatMessage';
import type { BridgeStatus } from '@/hooks/useBridgeSocket';
import { attachmentAccept } from './chatHelpers';

// The chat composer owns the in-progress text.
//
// This is the whole point of the component: `draft` lives HERE, not in
// ChatView. A keystroke therefore re-renders only the composer, instead of the
// entire page (header + transcript + drawers + modals). Before this split,
// every keystroke re-rendered ChatView, which re-rendered the transcript and
// made react-markdown re-parse every message — ~130ms of main-thread work per
// character on a long conversation (see the 2026-09-16 perf trace).
//
// The parent still needs to seed the text (the project file browser inserts a
// path), so that goes through the imperative `insert` handle rather than a
// controlled `value` prop. A controlled prop would force the draft back up
// into the parent and reintroduce the re-render we just removed.

export interface ChatComposerHandle {
  /** Append text to the draft (space-separated if non-empty). */
  insert: (text: string) => void;
  /** Clear the draft. */
  clear: () => void;
  /** Current draft, for callers that need to read it imperatively. */
  getValue: () => string;
}

interface Props {
  /** Send a message with the given text. Called with the trimmed draft. */
  onSend: (text: string) => void;
  /** Attachment queue, owned by the parent (it is sent along with the message). */
  pickedFiles: PickItem[];
  onRemoveFile: (id: string) => void;
  onAddFiles: (files: FileList | File[]) => void;
  /**
   * Bridge is connected. A dropped connection is temporary, so the composer
   * stays mounted and editable — losing the socket must not throw away a draft
   * — and the send button alone reports the state (amber, spinning, disabled).
   * A missing bridge config is not temporary; that state still replaces the row.
   */
  canSend: boolean;
  /** False once the bridge config has loaded but is unusable. */
  bridgeCfgLoaded: boolean;
  bridgeStatus: BridgeStatus;
  isRunning: boolean;
  /**
   * Whether a message sent mid-turn interrupts the running turn (true) or is
   * queued until it ends (false). Sending is allowed either way — the backend
   * decides (core/engine.go: interrupt-and-inject vs. queue) — so this only
   * picks the tooltip wording. The stop button stays clickable in both states,
   * because /stop must always be able to cancel a long-running turn.
   */
  interruptible: boolean;
  onStop: () => void;
  cmdOpen: boolean;
  onCmdOpenChange: (open: boolean) => void;
  onCmdSelect: (cmd: SlashCommand) => void;
  /**
   * Renders the quick-prompt panel above the composer. It is a render prop
   * rather than a node because the panel needs `close()`: sending a prompt has
   * to collapse the panel, and only the composer knows whether it is open.
   *
   * Omitted entirely when the caller has no prompt feature (no project yet).
   */
  promptsSlot?: (controls: { close: () => void }) => ReactNode;
  /**
   * The project has at least one saved prompt. With none, focus does not open
   * the panel: an empty "no prompts yet" card over the composer is noise for
   * someone who only wants to type. The prompt button still opens it, which is
   * where the first prompt gets added.
   *
   * Defaults to true so a caller that omits it keeps the focus behaviour.
   */
  promptsAvailable?: boolean;
  /** Notifies the parent so it can hide the "jump to latest" button. */
  onPromptsOpenChange?: (open: boolean) => void;
}

// Built from chatHelpers.TEXT_EXTS rather than hand-listed: the picker offered
// fewer formats than the previewer supported, so `.html` (and the rest of the
// text family) could be rendered but never attached. See attachmentAccept.
const ACCEPT = attachmentAccept();

const ChatComposer = forwardRef<ChatComposerHandle, Props>(function ChatComposer({
  onSend, pickedFiles, onRemoveFile, onAddFiles,
  canSend, bridgeCfgLoaded, bridgeStatus, isRunning, interruptible, onStop,
  cmdOpen, onCmdOpenChange, onCmdSelect,
  promptsSlot, promptsAvailable = true, onPromptsOpenChange,
}, ref) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState('');
  const hasContent = draft.trim().length > 0 || pickedFiles.length > 0;
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const cmdBtnRef = useRef<HTMLButtonElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  // Quick-prompt panel visibility.
  //
  // Two ways in, one rule for staying out of the way:
  //
  //   * focus (or a click in the input) opens it, but only when there is a list
  //     to show and nothing has been typed or attached yet;
  //   * the prompt button opens it unconditionally, which is the only way in
  //     for a project with no prompts (adding the first one lives in there).
  //
  // It closes when focus leaves the composer, when the draft or the attachment
  // queue stops being empty, when the command palette takes the space, and when
  // the bridge drops. Two details keep focus handling from flickering:
  //
  //   * focusin/focusout on the container, not focus/blur on the textarea —
  //     clicking the attach or slash button would otherwise blur the textarea
  //     and unmount the panel before the click lands.
  //   * a short close delay, so moving the pointer from the textarea to a row
  //     does not cross a frame with no panel.
  const [promptsOpen, setPromptsOpen] = useState(false);
  const promptsCloseTimer = useRef<number | null>(null);

  const cancelPromptsClose = useCallback(() => {
    if (promptsCloseTimer.current !== null) {
      window.clearTimeout(promptsCloseTimer.current);
      promptsCloseTimer.current = null;
    }
  }, []);

  // Opening from focus (or from a click in the input) is a convenience, so it
  // stays out of the way: no prompt list to show, or a message already being
  // written, and the panel would only cover the composer.
  const openPrompts = useCallback(() => {
    if (!promptsSlot || cmdOpen) return; // the command palette owns this space
    if (!promptsAvailable || hasContent) return;
    cancelPromptsClose();
    setPromptsOpen(true);
  }, [promptsSlot, cmdOpen, promptsAvailable, hasContent, cancelPromptsClose]);

  const closePrompts = useCallback(() => {
    cancelPromptsClose();
    setPromptsOpen(false);
  }, [cancelPromptsClose]);

  // The prompt button is the explicit way in, and the only one when the project
  // has no prompts yet — that empty panel is where the first one gets added.
  const togglePrompts = useCallback(() => {
    if (!promptsSlot || cmdOpen) return;
    if (promptsOpen) {
      closePrompts();
      return;
    }
    cancelPromptsClose();
    setPromptsOpen(true);
  }, [promptsSlot, cmdOpen, promptsOpen, closePrompts, cancelPromptsClose]);

  const schedulePromptsClose = useCallback(() => {
    cancelPromptsClose();
    promptsCloseTimer.current = window.setTimeout(() => {
      promptsCloseTimer.current = null;
      setPromptsOpen(false);
    }, 150);
  }, [cancelPromptsClose]);

  useEffect(() => cancelPromptsClose, [cancelPromptsClose]);
  useEffect(() => {
    if (cmdOpen) closePrompts();
  }, [cmdOpen, closePrompts]);
  // Losing the bridge collapses the prompt panel: its entries send messages,
  // and a panel left hovering over a transcript it cannot affect is noise. The
  // composer row itself stays mounted (see the send button below).
  useEffect(() => {
    if (!canSend) closePrompts();
  }, [canSend, closePrompts]);
  // Typing or attaching collapses the panel: shortcuts are for an empty
  // composer, and once the message has content they sit on top of it.
  useEffect(() => {
    if (hasContent) closePrompts();
  }, [hasContent, closePrompts]);
  useEffect(() => {
    onPromptsOpenChange?.(promptsOpen);
  }, [promptsOpen, onPromptsOpenChange]);

  const handleContainerBlur = useCallback((e: React.FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as HTMLElement | null;
    if (next && e.currentTarget.contains(next)) return;
    // The prompt editor renders in a portal, so editing a prompt moves focus
    // out of this subtree; collapsing the panel would unmount the editor with
    // it. The panel stays mounted for as long as the edit lasts.
    if (next && next.closest('[data-prompt-editor]')) return;
    schedulePromptsClose();
  }, [schedulePromptsClose]);

  const handleContainerFocus = useCallback((e: React.FocusEvent<HTMLDivElement>) => {
    // The prompt button runs its own toggle: letting the focus handler open the
    // panel first would make the click that follows close it again.
    if ((e.target as HTMLElement).closest('[data-prompts-trigger]')) return;
    openPrompts();
  }, [openPrompts]);

  // Auto-grow helper. Kept out of the onChange inline body so the two callers
  // (typing and programmatic insert) behave identically.
  const autoGrow = useCallback(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 160) + 'px';
  }, []);

  useImperativeHandle(ref, () => ({
    insert: (text: string) => {
      if (!text) return;
      setDraft(prev => (prev.trim() ? `${prev.trim()} ${text}` : text));
      // Re-measure after React commits the new value, and put the caret at the
      // end so the user can keep typing where they left off.
      requestAnimationFrame(() => {
        autoGrow();
        const el = textareaRef.current;
        if (el) {
          el.selectionStart = el.selectionEnd = el.value.length;
          el.focus();
        }
      });
    },
    clear: () => {
      setDraft('');
      requestAnimationFrame(autoGrow);
    },
    getValue: () => draft,
  }), [draft, autoGrow]);

  // Sending is NOT blocked while a turn is running: the backend either
  // interrupts the turn and runs this message first, or queues it. Blocking it
  // here would hide the interrupt path that IM platforms already have.
  //
  // A disconnected bridge does block the send — but only the send: the guard
  // returns before `setDraft('')`, so the typed text survives until reconnect.
  const submit = useCallback(() => {
    if (!draft.trim() || !canSend) return;
    onSend(draft.trim());
    setDraft('');
    // Collapse the textarea back to one row. Runs after the value commits.
    requestAnimationFrame(autoGrow);
  }, [draft, canSend, onSend, autoGrow]);

  // Paste handler: turn clipboard images into queued attachments (sent with
  // the message, not immediately). Plain text pastes fall through to the
  // textarea's default behaviour.
  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imageFiles: File[] = [];
    for (let i = 0; i < items.length; i++) {
      const item = items[i];
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        const f = item.getAsFile();
        if (f) imageFiles.push(f);
      }
    }
    if (imageFiles.length > 0) {
      e.preventDefault();
      onAddFiles(imageFiles);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      // 输入法组合中（如中文拼音选词/确认字母）的回车不触发发送，
      // 否则会发送半成品。组合结束后的回车才真正发送。
      if (e.nativeEvent.isComposing) return;
      e.preventDefault();
      submit();
    }
    if (e.key === '/' && !draft) {
      e.preventDefault();
      onCmdOpenChange(true);
    }
  };

  // No bridge config at all is a configuration error, not a flaky connection:
  // the user has to edit config.toml, and typing in the meantime would only
  // produce a draft that can never be sent — so this state keeps the warning
  // row that says where the fix is.
  if (!bridgeCfgLoaded) {
    return (
      <div className="flex items-center gap-2 px-4 py-3 text-sm text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 rounded-xl">
        <WifiOff size={14} />
        <span>{t('sessions.bridgeNotAvailable')}</span>
      </div>
    );
  }

  // A dropped or not-yet-established connection is different: it comes back on
  // its own. The row stays mounted so the draft survives it, and the send
  // button alone carries the state.
  const bridgeBlocked = !canSend;
  // A replaced connection is terminal, not "still connecting": another tab owns
  // the client_id, so waiting will not bring it back — the page must be
  // reloaded. Say that instead of showing the connecting wording.
  const blockedTitle = bridgeStatus === 'replaced'
    ? t('sessions.bridgeReplacedHint')
    : bridgeStatus === 'disconnected' || bridgeStatus === 'error'
      ? t('chat.sendBlocked')
      : t('sessions.bridgeConnecting');
  // Also the accessible name: the button is icon-only, and a disabled button's
  // `title` is not reliably announced.
  const sendHint = bridgeBlocked
    ? blockedTitle
    : isRunning
      ? t(interruptible ? 'chat.sendInterrupt' : 'chat.sendQueued')
      : t('sessions.send');

  return (
    <div
      className="relative flex items-end gap-2"
      onFocus={handleContainerFocus}
      onBlur={handleContainerBlur}
    >
      {/* Quick prompts drop out of the top edge of the composer. Positioned
          against the row itself, so it spans the full input width. */}
      {promptsSlot && promptsOpen && (
        <div className="absolute bottom-full left-0 right-0 z-40 mb-2">
          {promptsSlot({ close: closePrompts })}
        </div>
      )}

      {/* Attachment trigger */}
      <button
        type="button"
        onClick={() => fileInputRef.current?.click()}
        className="p-3 rounded-xl text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.06] transition-colors"
        title={t('chat.attach', 'Attach image/file')}
      >
        <Paperclip size={18} />
      </button>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        accept={ACCEPT}
        className="hidden"
        onChange={(e) => {
          if (e.target.files) onAddFiles(e.target.files);
        }}
      />

      {/* Command palette trigger */}
      <div className="relative">
        <button
          ref={cmdBtnRef}
          type="button"
          onClick={() => onCmdOpenChange(!cmdOpen)}
          className={cn(
            'p-3 rounded-xl transition-all duration-200',
            cmdOpen
              ? 'bg-accent/15 text-accent ring-1 ring-accent/30'
              : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.06]',
          )}
          title={t('chat.commands')}
        >
          <Slash size={18} />
        </button>
        <CommandPalette
          open={cmdOpen}
          onClose={() => onCmdOpenChange(false)}
          onSelect={onCmdSelect}
          anchorRef={cmdBtnRef}
        />
      </div>

      {/* Prompt list trigger. Focus opens the panel on its own once the project
          has prompts; this button is the way in for an empty list, which is
          where the first prompt gets added. */}
      {promptsSlot && (
        <button
          type="button"
          data-testid="composer-prompts"
          data-prompts-trigger="1"
          onClick={togglePrompts}
          aria-expanded={promptsOpen}
          className={cn(
            'p-3 rounded-xl transition-all duration-200',
            promptsOpen
              ? 'bg-accent/15 text-accent ring-1 ring-accent/30'
              : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-white/[0.06]',
          )}
          title={t('chat.prompts.open', 'Quick prompts')}
        >
          <Zap size={18} />
        </button>
      )}

      {/* Text input */}
      {/* `min-w-0` lets the row shrink below the button + textarea intrinsic
          width on narrow phones (textarea defaults to ~20 cols which alone
          exceeds 320px). */}
      <div className="flex-1 min-w-0 relative">
        {pickedFiles.length > 0 && (
          <div className="flex flex-wrap gap-1.5 mb-1.5">
            {pickedFiles.map((p) => (
              <div
                key={p.id}
                className="flex items-center gap-1.5 max-w-[220px] pl-1 pr-1.5 py-1 rounded-lg bg-gray-100 dark:bg-white/[0.06] border border-gray-200 dark:border-gray-700"
              >
                {p.kind === 'image' ? (
                  <img src={p.dataUrl} alt={p.name} className="w-6 h-6 rounded object-cover shrink-0" />
                ) : (
                  <FileText size={14} className="text-gray-400 shrink-0" />
                )}
                <span className="text-xs text-gray-600 dark:text-gray-300 truncate">{p.name}</span>
                <button
                  type="button"
                  onClick={() => onRemoveFile(p.id)}
                  className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 shrink-0"
                  title={t('chat.removeAttachment', 'Remove')}
                >
                  <X size={12} />
                </button>
              </div>
            ))}
          </div>
        )}
        <textarea
          ref={textareaRef}
          value={draft}
          onChange={(e) => {
            setDraft(e.target.value);
            e.target.style.height = 'auto';
            e.target.style.height = Math.min(e.target.scrollHeight, 160) + 'px';
          }}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          // Clicking a textarea that already has focus fires no focus event, so
          // the panel would stay closed after it was collapsed by a send.
          onClick={openPrompts}
          placeholder={t('chat.inputPlaceholder')}
          rows={1}
          // `py-2` (8px) + text-base 16px line-height 24px + border 2px = 42px,
          // matching the `p-3` buttons (12+18+12 = 42px) so icons and
          // textarea sit on the same baseline. `text-base` (16px) on
          // mobile prevents iOS from auto-zooming on focus.
          className="w-full min-w-0 px-4 py-2 text-base md:text-sm rounded-xl border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50 focus:border-accent transition-colors placeholder:text-gray-400 resize-none overflow-y-auto"
        />
      </div>

      {/* Stop button: only while a turn runs. Stays available even with a draft
          so the user can cancel without having to clear what they typed — but
          not while the bridge is down: /stop cannot reach the agent then, and a
          button that silently does nothing is worse than no button. */}
      {isRunning && !bridgeBlocked && (
        <button
          type="button"
          data-testid="composer-stop"
          onClick={onStop}
          title={t(interruptible ? 'chat.stopInterruptible' : 'chat.stopQueued')}
          className="p-3 rounded-xl bg-red-500 text-white hover:bg-red-600 transition-colors flex items-center shadow-sm"
        >
          <Square size={16} className="fill-current" />
        </button>
      )}
      {/* Send button: always present when idle; while running it appears only
          once there is something to send, so an empty composer shows just Stop.
          While the bridge is down it is always visible and turns amber with a
          spinner — the cheapest way to say "not now" without taking the input
          box away from the user. */}
      {(!isRunning || hasContent || bridgeBlocked) && (
        <button
          type="button"
          data-testid="composer-send"
          onClick={submit}
          disabled={!hasContent || bridgeBlocked}
          title={sendHint}
          aria-label={sendHint}
          className={cn(
            'p-3 rounded-xl transition-colors flex items-center',
            bridgeBlocked
              ? 'bg-amber-400 dark:bg-amber-500 text-black cursor-not-allowed'
              : 'bg-accent text-black hover:bg-accent-dim disabled:opacity-50',
          )}
        >
          {bridgeBlocked ? <Loader2 size={18} className="animate-spin" /> : <Send size={18} />}
        </button>
      )}
    </div>
  );
});

export default ChatComposer;
