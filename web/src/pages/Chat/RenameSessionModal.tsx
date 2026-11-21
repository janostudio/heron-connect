import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal, Input, Button } from '@/components/ui';
import { updateSession, type Session } from '@/api/sessions';

interface Props {
  open: boolean;
  project: string;
  session: Pick<Session, 'id' | 'name'> | null;
  onClose: () => void;
  onSaved?: (newName: string) => void;
}

export default function RenameSessionModal({ open, project, session, onClose, onSaved }: Props) {
  const { t } = useTranslation();
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // Seed the field when the dialog opens, and re-seed only when it is pointed
  // at a DIFFERENT session.
  //
  // Keying this on the `session` object itself (the original code) fired on
  // every parent render: both call sites pass an inline `{ id, name }` literal,
  // and the chat page re-renders on every bridge frame while the agent streams
  // (the session list polls every 5s too). Each firing wrote the stored name
  // back over the input, so whatever the user had typed vanished mid-edit.
  useEffect(() => {
    if (!open || !session) return;
    setName(session.name || '');
    setError('');
  }, [open, session?.id]);

  const handleSave = async () => {
    if (!session || !name.trim()) return;
    setSaving(true);
    setError('');
    try {
      await updateSession(project, session.id, { name: name.trim() });
      onSaved?.(name.trim());
      onClose();
    } catch (e: any) {
      setError(e?.message || 'Failed to rename session');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={open} onClose={onClose} title={t('chat.renameSession') || '重命名会话'}>
      <div className="space-y-4">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              // 输入法组合中（中文/日文选词）的回车不保存、不关闭，组合结束后的回车才保存。
              if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return;
              e.preventDefault();
              handleSave();
            }
          }}
          placeholder={session?.name || ''}
        />
        {error && <p className="text-sm text-red-500">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose} disabled={saving}>
            {t('common.cancel') || '取消'}
          </Button>
          <Button onClick={handleSave} disabled={saving || !name.trim()}>
            {saving ? '…' : t('common.save') || '保存'}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
