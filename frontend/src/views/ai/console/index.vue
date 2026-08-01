<template>
    <div class="vp-console-wrap">
        <el-alert
            v-if="auth.supported && !auth.loggedIn"
            class="vp-console__banner"
            type="warning"
            :closable="false"
            show-icon
        >
            <span>{{ $t('aiTools.console.notLoggedIn') }}</span>
            <el-button link type="primary" size="small" @click="loginRef?.open()">
                {{ $t('aiTools.console.loginNow') }}
            </el-button>
        </el-alert>

        <el-alert
            v-if="auth.hookInstalled === false"
            class="vp-console__banner"
            type="error"
            :closable="false"
            show-icon
            :title="$t('aiTools.console.permOff')"
            :description="$t('aiTools.console.permOffHint')"
        />

        <div class="vp-console">
        <SessionList
            class="vp-console__rail"
            :sessions="sessions"
            :current="current"
            :pool="pool"
            @select="select"
            @create="openCreate"
            @rename="doRename"
            @restart="doRestart"
            @remove="doRemove"
        />

        <div class="vp-console__main">
            <div v-if="!current" class="vp-console__blank">
                <el-empty :image-size="72" :description="$t('aiTools.console.pickHint')" />
            </div>
            <template v-else>
                <div class="vp-console__bar">
                    <span class="vp-console__cwd" :title="currentSession?.cwd">{{ currentSession?.cwd }}</span>
                    <el-tag v-if="currentSession" size="small" type="info" effect="plain">
                        {{ currentSession.harness }}
                    </el-tag>
                    <div class="grow" />
                    <el-switch
                        v-model="showTerm"
                        size="small"
                        inline-prompt
                        :active-text="$t('aiTools.console.terminal')"
                        :inactive-text="$t('aiTools.console.terminal')"
                    />
                    <el-button plain size="small" @click="reconnect">
                        {{ $t('commons.button.conn') }}
                    </el-button>
                </div>
                <div class="vp-console__panes" :class="{ 'no-term': !showTerm }">
                    <Chat
                        :events="events"
                        :busy="currentSession?.status === 'working'"
                        :can-interrupt="!!currentSession?.capabilities.interrupt"
                        @send="sendMessage"
                        @interrupt="interrupt"
                    />
                    <div v-show="showTerm" class="vp-console__term">
                        <Terminal :key="`term-${current}-${connId}`" ref="terminalRef" />
                    </div>
                </div>
            </template>
        </div>
        </div>
        <AgentLogin ref="loginRef" @done="loadAuth" />
        <Permission :req="perm" />
    </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElMessageBox } from 'element-plus';
import Terminal from '@/components/terminal/index.vue';
import SessionList from './components/session-list.vue';
import Chat from './components/chat.vue';
import AgentLogin from './components/agent-login.vue';
import Permission from './components/permission.vue';
import { ViPanel } from '@/api/interface/vipanel';
import {
    activateSession,
    createSession,
    deleteSession,
    getPool,
    listSessions,
    renameSession,
    restartSession,
    getAgentAuth,
} from '@/api/modules/vipanel';
import { MsgError, MsgSuccess } from '@/utils/message';

defineOptions({ name: 'Console' });

const { t } = useI18n();

const sessions = ref<ViPanel.Session[]>([]);
const pool = ref<ViPanel.Pool>({ size: 2, active: 0, order: [] });
const current = ref('');
const connId = ref(0);
const terminalRef = ref();
const showTerm = ref(true);
const loginRef = ref();
const auth = ref<ViPanel.AuthState>({ supported: false, loggedIn: true, hookInstalled: true });
const events = ref<any[]>([]);
const perm = ref<any>(null);
let poller: ReturnType<typeof setInterval> | undefined;
let evWS: WebSocket | undefined;
let evToken = 0;

const currentSession = computed(() => sessions.value.find((s) => s.id === current.value));

const loadAuth = async () => {
    try {
        auth.value = (await getAgentAuth()).data;
    } catch {
        /* 取不到就当支持且已登录，别用一个横幅挡住整个界面 */
    }
};

const refresh = async () => {
    const [ls, p] = await Promise.all([listSessions(), getPool()]);
    sessions.value = ls.data || [];
    pool.value = p.data;
    // 选中的会话被别处删掉了，别停在一个不存在的 id 上
    if (current.value && !sessions.value.some((s) => s.id === current.value)) {
        current.value = '';
    }
};

// 只负责下半栏的「终端」形态。Agent 屏幕是独立组件，
// 因为它必须用固定尺寸的 xterm，而上游 Terminal 一律 fit 到面板宽度。
const connect = () => {
    const s = currentSession.value;
    if (!s || !showTerm.value) return;
    terminalRef.value?.acceptParams({
        endpoint: '/api/v2/ai/console/pty',
        args: `cwd=${encodeURIComponent(s.cwd)}`,
        error: '',
        initCmd: '',
    });
};

watch(showTerm, () => remount());

// 换会话/重连都靠换 Terminal 的 key 整组件重挂载。
// 上游 Terminal 的 closeRealTerminal 没有 token 校验，旧 socket 的 close 晚到时
// 会往新终端里写断开提示，并把 terminalSocket 置空 —— 之后键盘输入会被静默丢弃。
const remount = async () => {
    connId.value += 1;
    await nextTick();
    connect();
};

const select = async (id: string) => {
    if (id === current.value) return;
    current.value = id;
    try {
        await activateSession(id);
    } catch {
        /* 激活失败也让终端连上去，错误由终端自己显示 */
    }
    await refresh();
    connectEvents(id);
    await remount();
};

const reconnect = () => remount();

// 事件流：先收一次 history 全量渲染，之后增量追加。
// 两者分开是必要的——混在一起前端分不清哪些该一次性铺开、哪些该滚动追加。
const connectEvents = (id: string) => {
    const token = ++evToken;
    try {
        evWS?.close();
    } catch {
        /* 已断开 */
    }
    events.value = [];
    perm.value = null;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/api/v2/ai/console/events?id=${id}`);
    evWS = ws;
    ws.onmessage = (ev) => {
        if (token !== evToken) return; // 迟到的旧会话消息，丢弃
        const msg = JSON.parse(ev.data);
        if (msg.type === 'history') events.value = msg.events || [];
        else if (msg.type === 'events') events.value.push(...(msg.events || []));
        else if (msg.type === 'permission_request') perm.value = msg.request;
        else if (msg.type === 'permission_resolved') {
            // 别的设备先点了，这边的弹窗要立刻收起，不能让人做第二次决定
            if (perm.value?.id === msg.id) perm.value = null;
        } else if (msg.type === 'error') MsgError(msg.message);
    };
};

const sendMessage = (text: string) => {
    if (evWS?.readyState === 1) evWS.send(JSON.stringify({ type: 'message', text }));
};

const interrupt = () => {
    if (evWS?.readyState === 1) evWS.send(JSON.stringify({ type: 'interrupt' }));
};

const openCreate = async () => {
    try {
        const { value } = await ElMessageBox.prompt(
            t('aiTools.console.cwdPlaceholder'),
            t('aiTools.console.newSession'),
            { inputValue: '', inputPlaceholder: '/root' },
        );
        const cwd = (value || '').trim();
        if (!cwd) return;
        const res = await createSession({ cwd });
        await refresh();
        await select(res.data.id);
        MsgSuccess(t('aiTools.console.newSession'));
    } catch {
        /* 取消 */
    }
};

const doRename = async (s: ViPanel.Session) => {
    try {
        const { value } = await ElMessageBox.prompt('', t('aiTools.console.rename'), { inputValue: s.title });
        const title = (value || '').trim();
        if (!title || title === s.title) return;
        await renameSession(s.id, title);
        await refresh();
    } catch {
        /* 取消 */
    }
};

const doRestart = async (s: ViPanel.Session) => {
    try {
        await restartSession(s.id);
        await refresh();
        if (s.id === current.value) await remount();
        MsgSuccess(t('aiTools.console.restarted'));
    } catch (e: any) {
        MsgError(e?.message || String(e));
    }
};

const doRemove = async (s: ViPanel.Session) => {
    try {
        await ElMessageBox.confirm(t('aiTools.console.finishHint'), `${t('aiTools.console.finish')}：${s.title}`, {
            type: 'warning',
        });
    } catch {
        return;
    }
    await deleteSession(s.id);
    if (s.id === current.value) current.value = '';
    await refresh();
};

onMounted(async () => {
    await Promise.all([refresh(), loadAuth()]);
    if (sessions.value.length) await select(sessions.value[0].id);
    // 状态灯要跟着后端走（被驱逐、进程自己退出都发生在后端）
    poller = setInterval(refresh, 3000);
});

onBeforeUnmount(() => {
    if (poller) clearInterval(poller);
    evToken++;
    try {
        evWS?.close();
    } catch {
        /* 已断开 */
    }
    terminalRef.value?.onClose();
});
</script>

<style lang="scss" scoped>
.vp-console-wrap {
    display: flex;
    flex-direction: column;
    gap: 8px;
}
.vp-console__banner {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
}

.vp-console {
    display: grid;
    /* minmax(0,1fr) 而不是 1fr：1fr 的最小尺寸是 auto，
       终端里一行长输出就会把整列撑破 */
    grid-template-columns: 232px minmax(0, 1fr);
    /* 行也要显式约束：只写 columns 的话隐式行是 auto，会被内容撑开，
       整条 min-height:0 的链就断在这里 */
    grid-template-rows: minmax(0, 1fr);
    height: calc(100vh - 120px);
    min-height: 360px;
    border: 1px solid var(--el-border-color-light);
    border-radius: 6px;
    overflow: hidden;
    background: var(--el-bg-color);
}

/* grid item 的 min-height 默认是 auto，不显式清零就会被内容撑破 */
.vp-console__rail,
.vp-console__main {
    min-height: 0;
    min-width: 0;
}

.vp-console__main {
    display: flex;
    flex-direction: column;
}

.vp-console__blank {
    flex: 1;
    display: grid;
    place-items: center;
}

.vp-console__bar {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--el-border-color-lighter);
}
.vp-console__cwd {
    font: 11px/1.4 var(--el-font-family-mono, monospace);
    color: var(--el-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 60%;
}
.grow {
    flex: 1;
}

.vp-console__panes {
    flex: 1;
    min-height: 0;
    display: grid;
    /* 终端占下半，关掉时聊天独占。minmax(0,·) 是必须的：
       1fr 的最小尺寸是 auto，终端一行长输出就会把整块撑破 */
    grid-template-rows: minmax(0, 1fr) minmax(0, 1fr);
}
.vp-console__panes.no-term {
    grid-template-rows: minmax(0, 1fr);
}

.vp-console__term {
    min-height: 0;
    overflow: hidden;
    border-top: 1px solid var(--el-border-color-light);
}
</style>
