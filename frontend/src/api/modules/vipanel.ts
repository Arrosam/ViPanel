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

// 登录状态是**每个 harness 各一份**的：Claude 登录了不代表 Codex 登录了。
// 不传就是默认 harness，和后端的 DefaultQuery 对齐。
export const getAgentAuth = (harness?: string) =>
    http.get<ViPanel.AuthState>(`/ai/console/auth/status${harness ? `?harness=${harness}` : ''}`);

export const agentLogout = (harness?: string) =>
    http.post(`/ai/console/auth/logout${harness ? `?harness=${harness}` : ''}`, {});

export const resolvePermission = (id: string, decision: string, reason: string) =>
    http.post(`/ai/console/permission/resolve`, { id, decision, reason });

export const listHistory = () => http.get<ViPanel.History[]>(`/ai/console/history`);

export const openHistory = (id: string, cwd: string, title: string) =>
    http.post<ViPanel.Session>(`/ai/console/history/open`, { id, cwd, title });

export const controlSession = (id: string, kind: string, value: string) =>
    http.post(`/ai/console/sessions/control`, { id, kind, value });

// 面板操作能力（MCP）总开关
export const getMcpSetting = () => http.get<any>('/ai/console/mcp/setting');
export const updateMcpSetting = (enabled: boolean) =>
    http.post<any>('/ai/console/mcp/setting/update', { enabled });

// -- 账号管理 -----------------------------------------------------------------
//
// 账号是**每个 harness 各一套**的：Claude 的账号和 Codex 的账号毫无关系。
export const listAccounts = (harness: string) =>
    http.get<ViPanel.Account[]>(`/ai/console/accounts?harness=${harness}`);

export const captureAccount = (harness: string, label?: string) =>
    http.post<ViPanel.Account>(`/ai/console/accounts/capture`, { harness, label: label || '' });

export const activateAccount = (harness: string, id: string) =>
    http.post<{ needRestart: boolean }>(`/ai/console/accounts/activate`, { harness, id });

export const deleteAccount = (harness: string, id: string) =>
    http.post(`/ai/console/accounts/delete`, { harness, id });
