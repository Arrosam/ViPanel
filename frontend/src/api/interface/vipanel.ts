export namespace ViPanel {
    export interface Caps {
        structuredEvents: boolean;
        resume: boolean;
        interrupt: boolean;
        auth: boolean;
        models: string[];
        effortLevels: string[];
        commands: { name: string; desc: string }[];
        // 登录方式由后端的 harness 声明。文案在 i18n 里按 id 取。
        // needsCodeInput 决定码往哪个方向走：true 是浏览器给码、粘回终端；
        // false 是终端给码、拿到别的设备上去输。
        loginModes: { id: string; needsCodeInput: boolean }[];
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
        mode: string;
        capabilities: Caps;
    }

    export interface Outbound {
        enabled: boolean;
        url: string;
    }

    export interface ReachResult {
        url: string;
        purpose: string;
        ok: boolean;
        status: number;
        detail: string;
    }

    export interface Account {
        id: string;
        label: string;
        // 和当前 live 配置一致的那个。由后端比对指纹得出，不是前端记的。
        active: boolean;
        addedAt: number;
    }

    export interface Prereq {
        binary: string;
        hint: string;
    }

    export interface InstallInfo {
        installable: boolean;
        // 本机缺少的前置依赖。非空时显示原因，不显示安装按钮。
        missing?: Prereq[];
        note?: string;
    }

    export interface Harness {
        id: string;
        displayName: string;
        // 这台机器上装没装。没装就别让用户点进去撞墙。
        installed: boolean;
        install: InstallInfo;
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

    export interface History {
        id: string;
        cwd: string;
        title: string;
        mtime: number;
        size: number;
    }

    export interface Pool {
        size: number;
        active: number;
        order: string[];
    }
}
