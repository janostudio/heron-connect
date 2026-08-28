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

  useEffect(() => {
    if (open && session) {
      setName(session.name || '');
      setError('');
    }
  }, [open, session]);

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
          onKeyDown={(e) => { if (e.key === 'Enter') handleSave(); }}
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
