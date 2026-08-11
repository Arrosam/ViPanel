<!--
  安装某个 harness 的对话框。

  和登录对话框（agent-login.vue）同一套结构：起一条 WebSocket，后端把伪终端的
  原始输出直接推过来，这边原样显示。

  **输出必须实时可见，不能只给一个转圈。** npm 装这两个包要几十秒到几分钟，
  期间没有任何反馈的话，用户唯一能做的判断就是「是不是卡死了」。
-->
<template>
    <el-dialog v-model="visible" :title="title" width="680px" @closed="cleanup">
        <div v-if="!running">
            <p class="vp-inst__note">{{ note }}</p>
            <el-alert type="warning" :closable="false" show-icon>
                {{ $t('aiTools.console.installWarn') }}
            </el-alert>
        </div>

        <div v-else>
            <div class="vp-inst__state">
                <el-icon v-if="!done" class="is-loading"><Loading /></el-icon>
                <span v-if="!done">{{ $t('aiTools.console.installRunning') }}</span>
                <span v-else-if="ok" class="vp-inst__ok">{{ $t('aiTools.console.installOk') }}</span>
                <span v-else class="vp-inst__bad">{{ $t('aiTools.console.installFail') }}</span>
            </div>
            <!-- 原始输出。ref 用来在追加时把滚动条钉在底部 -->
            <pre ref="logRef" class="vp-inst__log">{{ raw }}</pre>
        </div>

        <template #footer>
            <el-button v-if="!running" @click="visible = false">{{ $t('aiTools.console.cancel') }}</el-button>
            <el-button v-if="!running" type="primary" @click="start">
                {{ $t('aiTools.console.installStart') }}
            </el-button>
            <el-button v-else-if="!done" @click="cleanup">{{ $t('aiTools.console.cancel') }}</el-button>
            <el-button v-else type="primary" @click="finish">{{ $t('aiTools.console.done') }}</el-button>
        </template>
    </el-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { Loading } from '@element-plus/icons-vue';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();
const emit = defineEmits<{ (e: 'done', installed: boolean): void }>();

const visible = ref(false);
const running = ref(false);
const done = ref(false);
const ok = ref(false);
const raw = ref('');
const logRef = ref<HTMLElement>();
const harness = ref('');
const harnessName = ref('');
const note = ref('');
let ws: WebSocket | undefined;

const title = computed(() =>
    harnessName.value ? `${t('aiTools.console.installTitle')} · ${harnessName.value}` : t('aiTools.console.installTitle'),
);

const decode = (b64: string) => new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));

const open = (id: string, name: string, planNote: string) => {
    visible.value = true;
    running.value = false;
    done.value = false;
    ok.value = false;
    raw.value = '';
    harness.value = id;
    harnessName.value = name;
    note.value = planNote;
};

const start = () => {
    running.value = true;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${location.host}/api/v2/ai/console/harness/install?harness=${harness.value}`);
    ws.onmessage = (ev) => {
        const m = JSON.parse(ev.data);
        if (m.type === 'cmd') {
            // 只留最后 40000 字符：npm 的输出可以很长，全留着会把页面拖垮
            raw.value = (raw.value + decode(m.data)).slice(-40000);
            nextTick(() => logRef.value && (logRef.value.scrollTop = logRef.value.scrollHeight));
        } else if (m.type === 'install_done') {
            done.value = true;
            // 装没装成以后端**实测探测**为准，不看 npm 的退出码
            ok.value = !!m.installed;
        } else if (m.type === 'err' || m.type === 'error') {
            raw.value += `\n${m.msg || m.message || ''}\n`;
            done.value = true;
            ok.value = false;
        }
    };
    // 连接断了而没收到 install_done，同样算结束——否则对话框会永远转圈
    ws.onclose = () => {
        if (running.value && !done.value) {
            done.value = true;
            ok.value = false;
        }
    };
};

const finish = () => {
    const installed = ok.value;
    cleanup();
    emit('done', installed);
};

const cleanup = () => {
    try {
        ws?.close();
    } catch {
        /* 已断开 */
    }
    ws = undefined;
    running.value = false;
    visible.value = false;
};

defineExpose({ open });
</script>

<style lang="scss" scoped>
.vp-inst__note {
    margin: 0 0 12px;
    font-size: 13px;
    color: var(--el-text-color-regular);
}
.vp-inst__state {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 10px;
    font-size: 13px;
}
.vp-inst__ok {
    color: var(--el-color-success);
}
.vp-inst__bad {
    color: var(--el-color-danger);
}
.vp-inst__log {
    max-height: 340px;
    overflow-y: auto;
    margin: 0;
    padding: 10px;
    border-radius: 5px;
    background: var(--el-fill-color-darker);
    font: 11px/1.6 var(--el-font-family-mono, monospace);
    white-space: pre-wrap;
    word-break: break-all;
}
</style>
