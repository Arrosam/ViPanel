<template>
    <el-dialog v-model="visible" :title="$t('aiTools.console.agentLogin')" width="640px" @closed="cleanup">
        <div v-if="!running">
            <el-radio-group v-model="mode">
                <el-radio value="claudeai">{{ $t('aiTools.console.loginSub') }}</el-radio>
                <el-radio value="console">{{ $t('aiTools.console.loginConsole') }}</el-radio>
            </el-radio-group>
            <p class="vp-login__note">
                {{ mode === 'console' ? $t('aiTools.console.loginConsoleNote') : $t('aiTools.console.loginSubNote') }}
            </p>
        </div>

        <div v-else class="vp-login">
            <ol class="vp-login__steps">
                <li>
                    {{ $t('aiTools.console.loginStep1') }}
                    <div v-if="urls.length" class="vp-login__links">
                        <div v-for="u in urls" :key="u" class="vp-login__link">
                            <a :href="u" target="_blank" rel="noopener noreferrer">{{ u }}</a>
                            <el-button link size="small" @click="copy(u)">
                                {{ $t('aiTools.console.copy') }}
                            </el-button>
                        </div>
                    </div>
                    <span v-else class="vp-login__waiting">{{ $t('aiTools.console.loginWaitLink') }}</span>
                </li>
                <li>{{ $t('aiTools.console.loginStep2') }}</li>
                <li>
                    {{ $t('aiTools.console.loginStep3') }}
                    <div class="vp-login__code">
                        <el-input v-model="code" spellcheck="false" @keyup.enter="submitCode" />
                        <el-button type="primary" :disabled="!code.trim()" @click="submitCode">
                            {{ $t('aiTools.console.confirm') }}
                        </el-button>
                    </div>
                </li>
            </ol>

            <el-collapse>
                <el-collapse-item :title="$t('aiTools.console.rawOutput')">
                    <pre class="vp-login__raw">{{ raw }}</pre>
                </el-collapse-item>
            </el-collapse>
        </div>

        <template #footer>
            <el-button v-if="!running" @click="visible = false">{{ $t('aiTools.console.cancel') }}</el-button>
            <el-button v-if="!running" type="primary" @click="start">
                {{ $t('aiTools.console.loginStart') }}
            </el-button>
            <el-button v-else @click="cleanup">{{ $t('aiTools.console.cancel') }}</el-button>
        </template>
    </el-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { MsgSuccess } from '@/utils/message';

const emit = defineEmits<{ (e: 'done'): void }>();

const visible = ref(false);
const running = ref(false);
const mode = ref('claudeai');
const urls = ref<string[]>([]);
const code = ref('');
const raw = ref('');
let ws: WebSocket | undefined;

const decode = (b64: string) => new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
const encode = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)));

const open = () => {
    visible.value = true;
    running.value = false;
    urls.value = [];
    code.value = '';
    raw.value = '';
};

const start = () => {
    running.value = true;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${location.host}/api/v2/ai/console/auth/login?mode=${mode.value}`);
    ws.onmessage = (ev) => {
        const m = JSON.parse(ev.data);
        if (m.type === 'cmd') raw.value = (raw.value + decode(m.data)).slice(-8000);
        else if (m.type === 'auth_url' && !urls.value.includes(m.url)) urls.value.push(m.url);
        else if (m.type === 'auth_done') {
            emit('done');
            visible.value = false;
        }
    };
};

// 授权码必须分两次写：正文一次，回车一次。
// 合成 write(code + "\r") 会被 TUI 当成「粘贴了带换行的文本」，
// 换行进了输入框、码根本没提交。这条在 demo 阶段被同一个坑绊过三次。
const submitCode = () => {
    const v = code.value.trim();
    if (!v || ws?.readyState !== 1) return;
    ws.send(JSON.stringify({ type: 'cmd', data: encode(v) }));
    setTimeout(() => ws?.send(JSON.stringify({ type: 'cmd', data: encode('\r') })), 200);
    code.value = '';
};

const copy = async (u: string) => {
    await navigator.clipboard.writeText(u);
    MsgSuccess('OK');
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
.vp-login__note {
    margin: 8px 0 0;
    font-size: 12px;
    color: var(--el-text-color-secondary);
}
.vp-login__steps {
    margin: 0 0 12px;
    padding-left: 20px;
    font-size: 13px;
    line-height: 2;
}
.vp-login__links {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin: 4px 0;
}
.vp-login__link {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
}
.vp-login__link a {
    flex: 1;
    min-width: 0;
    font: 11px/1.5 var(--el-font-family-mono, monospace);
    word-break: break-all;
}
.vp-login__waiting {
    font-size: 12px;
    color: var(--el-text-color-secondary);
}
.vp-login__code {
    display: flex;
    gap: 8px;
    margin: 4px 0;
}
.vp-login__raw {
    max-height: 220px;
    overflow: auto;
    margin: 0;
    font: 11px/1.5 var(--el-font-family-mono, monospace);
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--el-text-color-secondary);
}
</style>
