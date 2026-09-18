import React, { memo } from 'react';
import { useTranslation } from 'react-i18next';
import { Bot, User } from 'lucide-react';
import { cn } from '@/lib/utils';
import ProgressCard from './ProgressCard';
import {
  RenderMarkdown, CardBlock, ButtonsBlock, FileBlock, ImageBlock,
  HistoryImage, MsgCopyButton,
} from './markdownBlocks';
import type { ChatMsg } from './chatMessage';
import { formatMessageTime, fullMessageTime } from './messageTime';

// MessageRow renders a single chat bubble. It is memoized so that while a turn
// streams, only the row whose `msg` object actually changed re-renders — the
// rest of the transcript (and its parsed markdown / progress cards) is skipped.
//
// This only works if every prop is identity-stable when its data is unchanged:
// `msg` must keep its reference unless its content truly changed (see the
// per-session reducer, which never clones untouched messages), and
// `onOpenFile` / `onCardAction` must be useCallback-stabilised by the parent.

interface Props {
  msg: ChatMsg;
  projectName: string;
  onOpenFile: (path: string, fileName: string) => void;
  onCardAction: (value: string) => void;
}

function MessageRowInner({ msg, projectName, onOpenFile, onCardAction }: Props) {
  const { t, i18n } = useTranslation();
  const isUser = msg.role === 'user';
  const isEmpty = !msg.content && !msg.card && !msg.buttons && !msg.imageUrl && !msg.fileName && !msg.localMedia?.length && !msg.historyAttachments?.length;

  // Formatted here rather than by the memoized parent: the stamp lives on the
  // message itself, so deriving the label in-place keeps every prop identity-
  // stable (a precomputed label on `msg` would defeat Transcript/MessageRow's
  // memo on every clock tick). Both calls are cheap relative to the markdown
  // parse this row already does.
  const timeLabel = formatMessageTime(msg.timestamp, i18n.language, t('chat.time.yesterday', 'Yesterday'));
  const timeTitle = timeLabel ? fullMessageTime(msg.timestamp, i18n.language) ?? undefined : undefined;

  return (
    <div className={cn('flex flex-col gap-1', isUser ? 'items-end' : 'items-start')}>
      {timeLabel && (
        <span
          title={timeTitle}
          className={cn(
            'px-1 text-[10px] leading-none text-gray-400 dark:text-gray-500 select-none tabular-nums',
            isUser ? 'mr-11' : 'ml-11',
          )}
        >
          {timeLabel}
        </span>
      )}
      <div className={cn('flex gap-3 w-full', isUser ? 'justify-end' : 'justify-start')}>
        {!isUser && (
          <div className="w-8 h-8 rounded-lg bg-accent/10 flex items-center justify-center shrink-0 mt-1">
            <Bot size={16} className="text-accent" />
          </div>
        )}
        <div className={cn(
          'group/msg relative rounded-2xl px-3.5 py-3 sm:px-5 sm:py-3.5 text-sm min-w-0 break-words',
          isUser
            ? 'max-w-[85%] sm:max-w-[70%] bg-accent text-black rounded-br-md'
            : 'max-w-[85%] bg-white dark:bg-gray-800/80 border border-gray-200 dark:border-gray-700/60 text-gray-900 dark:text-gray-100 rounded-bl-md shadow-sm',
          msg.streaming && 'animate-pulse-subtle',
        )}>
          {isEmpty ? (
            <p className="text-xs text-gray-400 dark:text-gray-500 italic">{t('chat.unsupportedMessage', '[Unsupported message]')}</p>
          ) : msg.format === 'card' ? (
            <CardBlock card={msg.card} onAction={onCardAction} />
          ) : msg.format === 'buttons' && msg.buttons ? (
            <ButtonsBlock content={msg.content} buttons={msg.buttons} onAction={onCardAction} />
          ) : msg.format === 'image' && msg.imageUrl ? (
            <ImageBlock url={msg.imageUrl} />
          ) : msg.format === 'file' && msg.fileName ? (
            <FileBlock name={msg.fileName} size={msg.fileSize} />
          ) : isUser ? (
            <div className="space-y-2">
              {((msg.localMedia?.length ?? 0) > 0 || (msg.historyAttachments?.length ?? 0) > 0) && (
                <div className="flex flex-wrap gap-2">
                  {msg.localMedia?.map((m) =>
                    m.kind === 'image' ? (
                      <img key={m.id} src={m.dataUrl} alt={m.name} className="max-w-[220px] max-h-[220px] rounded-lg border border-white/20 object-cover" />
                    ) : (
                      <FileBlock key={m.id} name={m.name} size={m.size} />
                    ),
                  )}
                  {msg.historyAttachments?.map((a, idx) =>
                    a.kind === 'image' ? (
                      <HistoryImage key={`ha-${idx}`} project={projectName} att={a} />
                    ) : (
                      <FileBlock key={`ha-${idx}`} name={a.name} size={a.size} />
                    ),
                  )}
                </div>
              )}
              {msg.content && <div className="whitespace-pre-wrap">{msg.content}</div>}
            </div>
          ) : msg.progressCard ? (
            <ProgressCard payload={msg.progressCard} />
          ) : (
            <RenderMarkdown content={msg.content} onOpenFile={onOpenFile} />
          )}
          {msg.streaming && !msg.progressCard && (
            <span className="inline-block w-1.5 h-4 bg-accent/60 rounded-sm ml-0.5 animate-pulse" />
          )}
          {!isUser && !msg.streaming && msg.content && !msg.progressCard && (
            <MsgCopyButton text={msg.content} stripFooter />
          )}
        </div>
        {isUser && (
          <div className="w-8 h-8 rounded-lg bg-gray-200 dark:bg-gray-700 flex items-center justify-center shrink-0 mt-1">
            <User size={16} className="text-gray-500" />
          </div>
        )}
      </div>
    </div>
  );
}

export default memo(MessageRowInner);
