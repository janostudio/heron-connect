import api from './client';

/**
 * A saved prompt of a project. Prompts belong to the project, not to a
 * conversation: the same list appears in every conversation of that project.
 */
export interface QuickPrompt {
  id: string;
  project: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export interface QuickPromptList {
  prompts: QuickPrompt[];
  max_content_length: number;
  max_per_project: number;
}

export const listQuickPrompts = (project: string) =>
  api.get<QuickPromptList>(`/projects/${encodeURIComponent(project)}/prompts`);

export const createQuickPrompt = (project: string, content: string) =>
  api.post<QuickPrompt>(`/projects/${encodeURIComponent(project)}/prompts`, { content });

export const updateQuickPrompt = (project: string, id: string, content: string) =>
  api.patch<QuickPrompt>(
    `/projects/${encodeURIComponent(project)}/prompts/${encodeURIComponent(id)}`,
    { content },
  );

export const deleteQuickPrompt = (project: string, id: string) =>
  api.delete(`/projects/${encodeURIComponent(project)}/prompts/${encodeURIComponent(id)}`);
