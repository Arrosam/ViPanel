import http from '@/api';
import { ViPanel } from '@/api/interface/vipanel';

export const listSessions = () => http.post<ViPanel.Session[]>(`/ai/console/sessions`, {});

export const createSession = (params: { cwd: string; title?: string; harness?: string }) =>
    http.post<ViPanel.Session>(`/ai/console/sessions/create`, params);

export const renameSession = (id: string, title: string) =>
    http.post(`/ai/console/sessions/rename`, { id, title });

export const deleteSession = (id: string) => http.post(`/ai/console/sessions/delete`, { id });

export const activateSession = (id: string) =>
    http.post<ViPanel.Session>(`/ai/console/sessions/activate`, { id });

export const restartSession = (id: string) =>
    http.post<ViPanel.Session>(`/ai/console/sessions/restart`, { id });

export const listHarnesses = () => http.get<ViPanel.Harness[]>(`/ai/console/harnesses`);

export const getPool = () => http.get<ViPanel.Pool>(`/ai/console/pool`);

export const updatePool = (size: number) => http.post<ViPanel.Pool>(`/ai/console/pool/update`, { size });

export const getAgentAuth = () => http.get<ViPanel.AuthState>(`/ai/console/auth/status`);

export const agentLogout = () => http.post(`/ai/console/auth/logout`, {});

export const resolvePermission = (id: string, decision: string, reason: string) =>
    http.post(`/ai/console/permission/resolve`, { id, decision, reason });

export const listHistory = () => http.get<ViPanel.History[]>(`/ai/console/history`);

export const openHistory = (id: string, cwd: string, title: string) =>
    http.post<ViPanel.Session>(`/ai/console/history/open`, { id, cwd, title });

export const controlSession = (id: string, kind: string, value: string) =>
    http.post(`/ai/console/sessions/control`, { id, kind, value });
