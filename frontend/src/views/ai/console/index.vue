<template>
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
                    <el-button plain size="small" @click="reconnect">
                        {{ $t('commons.button.conn') }}
                    </el-button>
                </div>
                <div class="vp-console__term">
                    <Terminal :key="`${current}-${connId}`" ref="terminalRef" />
                </div>
            </template>
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElMessageBox } from 'element-plus';
import Terminal from '@/components/terminal/index.vue';
import SessionList from './components/session-list.vue';
import { ViPanel } from '@/api/interface/vipanel';
import {
    activateSession,
    createSession,
    deleteSession,
    getPool,
    listSessions,
    renameSession,
    restartSession,
} from '@/api/modules/vipanel';
import { MsgError, MsgSuccess } from '@/utils/message';

defineOptions({ name: 'Console' });

const { t } = useI18n();

const sessions = ref<ViPanel.Session[]>([]);
const pool = ref<ViPanel.Pool>({ size: 2, active: 0, order: [] });
const current = ref('');
const connId = ref(0);
const terminalRef = ref();
let poller: ReturnType<typeof setInterval> | undefined;

const currentSession = computed(() => sessions.value.find((s) => s.id === current.value));

const refresh = async () => {
    const [ls, p] = await Promise.all([listSessions(), getPool()]);
    sessions.value = ls.data || [];
    pool.value = p.data;
    // 选中的会话被别处删掉了，别停在一个不存在的 id 上
    if (current.value && !sessions.value.some((s) => s.id === current.value)) {
        current.value = '';
    }
};

const connect = () => {
    const s = currentSession.value;
    if (!s) return;
    terminalRef.value?.acceptParams({
        endpoint: '/api/v2/ai/console/pty',
        args: `cwd=${encodeURIComponent(s.cwd)}`,
        error: '',
        initCmd: '',
    });
};

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
    await remount();
};

const reconnect = () => remount();

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
    await refresh();
    if (sessions.value.length) await select(sessions.value[0].id);
    // 状态灯要跟着后端走（被驱逐、进程自己退出都发生在后端）
    poller = setInterval(refresh, 3000);
});

onBeforeUnmount(() => {
    if (poller) clearInterval(poller);
    terminalRef.value?.onClose();
});
</script>

<style lang="scss" scoped>
.vp-console {
    display: grid;
    /* minmax(0,1fr) 而不是 1fr：1fr 的最小尺寸是 auto，
       终端里一行长输出就会把整列撑破 */
    grid-template-columns: 232px minmax(0, 1fr);
    height: calc(100vh - 120px);
    min-height: 360px;
    border: 1px solid var(--el-border-color-light);
    border-radius: 6px;
    overflow: hidden;
    background: var(--el-bg-color);
}

.vp-console__main {
    display: flex;
    flex-direction: column;
    min-width: 0;
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

.vp-console__term {
    flex: 1;
    min-height: 0;
    overflow: hidden;
}
</style>
