import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Pencil, Plus, X } from 'lucide-react';
import { Modal } from '@/components/ui/Modal';
import { Button } from '@/components/ui/Button';
import { cn } from '@/lib/utils';
import type { QuickPrompt } from '@/api/prompts';

/**
 * The quick-prompt list that drops out of the composer.
 *
 * Prompts are project-scoped, so this component never sees a session ID: the
 * caller loads them once per project. Rows are one-click sends — the list is a
 * shortcut, not a queue, so there is no draft round-trip.
 *
 * All clickable rows call `preventDefault` on mousedown. Without it the
 * composer's textarea blurs first and the list would be unmounted before the
 * click lands.
 */

interface Props {
  prompts: QuickPrompt[];
  loading: boolean;
  loadError: string | null;
  /** Bridge connected — sending needs it; editing does not. */
  canSend: boolean;
  /** A turn is running; rows are inert (no confirm dialog, just no-op). */
  isRunning: boolean;
  maxContentLength: number;
  maxPerProject: number;
  onSend: (content: string) => void;
  onCreate: (content: string) => Promise<void>;
  onUpdate: (id: string, content: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  /** Collapse the panel (after a send). */
  onClose: () => void;
}

interface EditorState {
  id: string | null; // null = create
  content: string;
}

const keepFocus = (e: React.MouseEvent) => e.preventDefault();

/** Counts characters the way the server does (runes), not UTF-16 units. */
const charCount = (s: string) => Array.from(s).length;

export default function PromptList({
  prompts, loading, loadError, canSend, isRunning,
  maxContentLength, maxPerProject,
  onSend, onCreate, onUpdate, onDelete, onClose,
}: Props) {
  const { t } = useTranslation();
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [form, setForm] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // A delete confirmation is transient: leaving it armed forever turns the next
  // stray click on the same row into a deletion.
  useEffect(() => {
    if (!confirmId) return;
    const timer = window.setTimeout(() => setConfirmId(null), 4000);
    return () => window.clearTimeout(timer);
  }, [confirmId]);

  const openEditor = useCallback((p?: QuickPrompt) => {
    setEditor({ id: p?.id ?? null, content: p?.content ?? '' });
    setForm(p?.content ?? '');
    setFormError(null);
    setConfirmId(null);
  }, []);

  const closeEditor = useCallback(() => {
    setEditor(null);
    setFormError(null);
  }, []);

  const validate = useCallback((): string | null => {
    const content = form.trim();
    if (!content) return t('chat.prompts.errorEmpty', 'Prompt cannot be empty');
    if (content.startsWith('/')) {
      // The send path classifies '/...' as a slash command, so such a prompt
      // would never reach the agent as a message.
      return t('chat.prompts.errorSlash', 'A prompt cannot start with "/" (it would run as a command)');
    }
    if (charCount(content) > maxContentLength) {
      return t('chat.prompts.errorTooLong', 'Prompt is limited to {{max}} characters', { max: maxContentLength });
    }
    if (!editor?.id && prompts.length >= maxPerProject) {
      return t('chat.prompts.errorLimit', 'A project holds at most {{max}} prompts', { max: maxPerProject });
    }
    return null;
  }, [form, editor?.id, maxContentLength, maxPerProject, prompts.length, t]);

  const save = useCallback(async () => {
    if (saving) return;
    const problem = validate();
    if (problem) {
      setFormError(problem);
      return;
    }
    const content = form.trim();
    setSaving(true);
    setFormError(null);
    try {
      if (editor?.id) await onUpdate(editor.id, content);
      else await onCreate(content);
      setEditor(null);
    } catch (e) {
      setFormError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }, [saving, validate, form, editor?.id, onUpdate, onCreate]);

  const send = useCallback((p: QuickPrompt) => {
    if (!canSend || isRunning) return;
    onSend(p.content);
    onClose();
  }, [canSend, isRunning, onSend, onClose]);

  const confirmDelete = useCallback(async (id: string) => {
    setBusyId(id);
    setActionError(null);
    try {
      await onDelete(id);
      setConfirmId(null);
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyId(null);
    }
  }, [onDelete]);

  const atLimit = prompts.length >= maxPerProject;
  const disabledReason = !canSend
    ? t('chat.prompts.sendOffline', 'Connection is down')
    : isRunning
      ? t('chat.prompts.sendBusy', 'A reply is in progress')
      : undefined;

  return (
    <>
      <div className="overflow-hidden rounded-xl border border-gray-200 bg-white/95 shadow-lg backdrop-blur-xl dark:border-gray-700 dark:bg-[#1f2228]/95">
        <div className="max-h-40 overflow-y-auto py-1 md:max-h-[200px]">
          {loading && (
            <div className="flex items-center gap-2 px-3 py-2 text-xs text-gray-400">
              <Loader2 size={14} className="animate-spin" />
              <span>{t('common.loading')}</span>
            </div>
          )}

          {!loading && loadError && (
            <div className="px-3 py-2 text-xs text-red-500">{loadError}</div>
          )}

          {!loading && !loadError && prompts.length === 0 && (
            <div className="px-3 py-2 text-xs text-gray-400">
              {t('chat.prompts.empty', 'No saved prompts yet')}
            </div>
          )}

          {!loading && prompts.map((p) => (
            confirmId === p.id ? (
              <div
                key={p.id}
                className="flex items-center gap-2 bg-red-50 px-3 py-2 dark:bg-red-900/20"
              >
                <span className="min-w-0 flex-1 truncate text-xs text-red-600 dark:text-red-400">
                  {t('chat.prompts.deleteConfirm', 'Delete this prompt?')}
                </span>
                <Button
                  size="sm"
                  variant="danger"
                  data-testid="prompt-confirm-delete"
                  loading={busyId === p.id}
                  onMouseDown={keepFocus}
                  onClick={() => void confirmDelete(p.id)}
                >
                  {t('common.confirmDelete')}
                </Button>
                <Button size="sm" variant="ghost" onMouseDown={keepFocus} onClick={() => setConfirmId(null)}>
                  {t('common.cancel')}
                </Button>
              </div>
            ) : (
              <div
                key={p.id}
                className="group flex items-center gap-1 px-2 hover:bg-gray-100/80 dark:hover:bg-white/[0.06]"
              >
                <button
                  type="button"
                  data-testid="prompt-row"
                  disabled={!canSend || isRunning}
                  title={disabledReason}
                  onMouseDown={keepFocus}
                  onClick={() => send(p)}
                  className={cn(
                    'min-w-0 flex-1 truncate px-1 py-2 text-left text-sm text-gray-700 dark:text-gray-200',
                    'disabled:cursor-not-allowed disabled:opacity-50',
                  )}
                >
                  {p.content}
                </button>
                <button
                  type="button"
                  data-testid="prompt-edit"
                  aria-label={t('chat.prompts.edit', 'Edit')}
                  title={t('chat.prompts.edit', 'Edit')}
                  onMouseDown={keepFocus}
                  onClick={() => openEditor(p)}
                  className="shrink-0 rounded p-1 text-gray-400 opacity-0 transition-opacity hover:text-gray-700 focus:opacity-100 group-hover:opacity-100 dark:hover:text-gray-200 max-md:opacity-100"
                >
                  <Pencil size={14} />
                </button>
                <button
                  type="button"
                  data-testid="prompt-delete"
                  aria-label={t('chat.prompts.delete', 'Delete')}
                  title={t('chat.prompts.delete', 'Delete')}
                  onMouseDown={keepFocus}
                  onClick={() => setConfirmId(p.id)}
                  className="shrink-0 rounded p-1 text-gray-400 opacity-0 transition-opacity hover:text-red-500 focus:opacity-100 group-hover:opacity-100 max-md:opacity-100"
                >
                  <X size={14} />
                </button>
              </div>
            )
          ))}

          {actionError && (
            <div className="px-3 py-2 text-xs text-red-500">{actionError}</div>
          )}
        </div>

        {/* Pinned outside the scroll area: adding a prompt must stay reachable
            however many rows there are. */}
        <button
          type="button"
          data-testid="prompt-add"
          disabled={atLimit}
          title={atLimit
            ? t('chat.prompts.errorLimit', 'A project holds at most {{max}} prompts', { max: maxPerProject })
            : t('chat.prompts.add', 'Add prompt')}
          onMouseDown={keepFocus}
          onClick={() => openEditor()}
          className={cn(
            'flex w-full items-center gap-1.5 border-t border-gray-200 px-3 py-2 text-left text-xs',
            'text-accent hover:bg-accent/5 disabled:cursor-not-allowed disabled:opacity-50',
            'dark:border-gray-700',
          )}
        >
          <Plus size={14} />
          <span>{t('chat.prompts.add', 'Add prompt')}</span>
        </button>
      </div>

      <Modal
        open={editor !== null}
        onClose={() => (saving ? undefined : closeEditor())}
        title={editor?.id
          ? t('chat.prompts.editTitle', 'Edit prompt')
          : t('chat.prompts.createTitle', 'Add prompt')}
      >
        {/* The editor lives in a portal, outside the composer that owns this
            panel. `data-prompt-editor` is how the composer knows focus is in
            the editor and must not collapse the panel under it. */}
        <div data-prompt-editor="1">
          <textarea
            autoFocus
            data-testid="prompt-input"
            rows={5}
            value={form}
            onChange={(e) => {
              setForm(e.target.value);
              setFormError(null);
            }}
            placeholder={t('chat.prompts.placeholder', 'Prompt text…')}
            className="w-full resize-none rounded-xl border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/50 dark:border-gray-700 dark:bg-gray-800 dark:text-white"
          />
          <div className="mt-2 flex items-center justify-between gap-3">
            <span className={cn(
              'text-xs',
              charCount(form) > maxContentLength ? 'text-red-500' : 'text-gray-400',
            )}>
              {charCount(form)}/{maxContentLength}
            </span>
            <div className="flex gap-2">
              <Button variant="secondary" size="sm" disabled={saving} onClick={closeEditor}>
                {t('common.cancel')}
              </Button>
              <Button size="sm" data-testid="prompt-save" loading={saving} onClick={() => void save()}>
                {t('common.save')}
              </Button>
            </div>
          </div>
          {formError && <p className="mt-2 text-xs text-red-500">{formError}</p>}
        </div>
      </Modal>
    </>
  );
}
