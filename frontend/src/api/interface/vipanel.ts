export namespace ViPanel {
    export interface Caps {
        structuredEvents: boolean;
        resume: boolean;
        interrupt: boolean;
        auth: boolean;
        models: string[];
        effortLevels: string[];
    }

    export interface Session {
        id: string;
        title: string;
        cwd: string;
        dir: string;
        harness: string;
        status: 'idle' | 'working' | 'unread' | 'sleeping' | 'error';
        alive: boolean;
        lastUsed: number;
        notes: string;
        capabilities: Caps;
    }

    export interface Harness {
        id: string;
        displayName: string;
        capabilities: Caps;
    }

    export interface AuthState {
        supported: boolean;
        hookInstalled: boolean;
        loggedIn: boolean;
        authMethod?: string;
        email?: string;
        plan?: string;
    }

    export interface Pool {
        size: number;
        active: number;
        order: string[];
    }
}
