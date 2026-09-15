// Shared chat message types. Extracted from ChatView so the memoized
// MessageRow / RenderMarkdown components can import them without creating a
// circular dependency back into the page component.

// A file the user has picked for upload in the composer. `dataUrl` is the
// base64 data: URL read via FileReader; we strip the prefix when sending.
export interface PickItem {
  id: string;
  name: string;
  mime_type: string;
  dataUrl: string;
  size: number;
  kind: 'image' | 'file';
}

// A persisted attachment reference loaded from session history. The bytes are
// on disk; `path` is a slash path relative to the project workDir (e.g.
// ".heron-connect/history-attachments/s89/foo.png") served via /files.
export interface HistoryAttachment {
  kind: 'image' | 'file';
  name: string;
  mime_type: string;
  path: string;
  size?: number;
}

export interface ChatMsg {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  format?: 'text' | 'markdown' | 'card' | 'buttons' | 'image' | 'file';
  card?: any;
  buttons?: { text: string; data: string }[][];
  imageUrl?: string;
  fileName?: string;
  fileSize?: number;
  // Local media attached by the user in this message (rendered before content).
  localMedia?: PickItem[];
  // Persisted attachments from session history (rendered on reload).
  historyAttachments?: HistoryAttachment[];
  streaming?: boolean;
  previewHandle?: string;
  timestamp?: string;
  // When the message body is a structured agent-progress payload (sent by
  // the engine with the __heron_connect_progress_card_v1__: prefix), the
  // parsed payload lives here and takes precedence over `content` for
  // rendering. `content` is still kept verbatim so the message survives a
  // downgrade (server without the prefix, history replay, etc.).
  progressCard?: import('./ProgressCard').ProgressCardPayload;
}
