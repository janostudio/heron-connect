import { forwardRef, useCallback, useImperativeHandle, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { FileText, Paperclip, Send, Slash, Square, WifiOff, Loader2, X } from 'lucide-react';
import { cn } from '@/lib/utils';
import CommandPalette, { type SlashCommand } from './CommandPalette';
import type { PickItem } from './chatMessage';

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
  /** Bridge is connected — the composer replaces itself with a warning if not. */
  canSend: boolean;
  /** False once the bridge config has loaded but is unusable. */
  bridgeCfgLoaded: boolean;
  bridgeStatus: string;
  isRunning: boolean;
  onStop: () => void;
  cmdOpen: boolean;
  onCmdOpenChange: (open: boolean) => void;
  onCmdSelect: (cmd: SlashCommand) => void;
}

const ACCEPT = 'image/*,.pdf,.txt,.md,.markdown,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.csv,.json,.yaml,.yml,.zip,.tar,.gz,.py,.js,.ts,.go,.java,.c,.h,.cpp,.sh,.sql,.log';

const ChatComposer = forwardRef<ChatComposerHandle, Props>(function ChatComposer({
  onSend, pickedFiles, onRemoveFile, onAddFiles,
  canSend, bridgeCfgLoaded, bridgeStatus, isRunning, onStop,
  cmdOpen, onCmdOpenChange, onCmdSelect,
}, ref) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState('');
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const cmdBtnRef = useRef<HTMLButtonElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

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

  const submit = useCallback(() => {
    if (isRunning) return;
    if (!draft.trim() || !canSend) return;
    onSend(draft.trim());
    setDraft('');
    // Collapse the textarea back to one row. Runs after the value commits.
    requestAnimationFrame(autoGrow);
  }, [draft, isRunning, canSend, onSend, autoGrow]);

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

  if (!canSend) {
    if (!bridgeCfgLoaded) {
      return (
        <div className="flex items-center gap-2 px-4 py-3 text-sm text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 rounded-xl">
          <WifiOff size={14} />
          <span>{t('sessions.bridgeNotAvailable')}</span>
        </div>
      );
    }
    if (bridgeStatus === 'disconnected' || bridgeStatus === 'error') {
      return (
        <div className="flex items-center gap-2 px-4 py-3 text-sm text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 rounded-xl">
          <WifiOff size={14} />
          <span>{t('sessions.bridgeDisconnected')}</span>
        </div>
      );
    }
    return (
      <div className="flex items-center gap-2 px-4 py-3 text-sm text-gray-400 bg-gray-50 dark:bg-gray-800/50 rounded-xl">
        <Loader2 size={14} className="animate-spin" />
        <span>{t('sessions.bridgeConnecting')}</span>
      </div>
    );
  }

  return (
    <div className="relative flex items-end gap-2">
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
          placeholder={t('chat.inputPlaceholder')}
          rows={1}
          // `py-2` (8px) + text-base 16px line-height 24px + border 2px = 42px,
          // matching the `p-3` buttons (12+18+12 = 42px) so icons and
          // textarea sit on the same baseline. `text-base` (16px) on
          // mobile prevents iOS from auto-zooming on focus.
          className="w-full min-w-0 px-4 py-2 text-base md:text-sm rounded-xl border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-accent/50 focus:border-accent transition-colors placeholder:text-gray-400 resize-none overflow-y-auto"
        />
      </div>

      {/* Send / Stop button */}
      {isRunning ? (
        <button
          type="button"
          onClick={onStop}
          title={t('chat.stop')}
          className="p-3 rounded-xl bg-red-500 text-white hover:bg-red-600 transition-colors flex items-center shadow-sm"
        >
          <Square size={16} className="fill-current" />
        </button>
      ) : (
        <button
          type="button"
          onClick={submit}
          disabled={!draft.trim() && pickedFiles.length === 0}
          className="p-3 rounded-xl bg-accent text-black hover:bg-accent-dim transition-colors disabled:opacity-50 flex items-center"
        >
          <Send size={18} />
        </button>
      )}
    </div>
  );
});

export default ChatComposer;
