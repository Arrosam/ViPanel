<template>
    <el-dialog v-model="visible" :title="title" width="640px" @closed="cleanup">
        <div v-if="!running">
            <!-- 有哪几种登录方式由 harness 声明（Capabilities.LoginModes），
                 不是写死的。只有一种时不显示单选钮——一个只能选它自己的
                 单选钮不给人任何信息。 -->
            <el-radio-group v-if="modes.length > 1" v-model="mode">
                <el-radio v-for="m in modes" :key="m.id" :value="m.id">{{ modeLabel(m.id) }}</el-radio>
            </el-radio-group>
            <p class="vp-login__note">{{ modeNote(mode) }}</p>
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
                <!-- 码的方向由 harness 声明。方向搞反的话，界面会要用户去粘一个
                     根本不存在的东西——设备码流里码是终端发出去的，不是收回来的。 -->
                <li v-if="needsCodeInput">
                    {{ $t('aiTools.console.loginStep3') }}
                    <div class="vp-login__code">
                        <el-input v-model="code" spellcheck="false" @keyup.enter="submitCode" />
                        <el-button type="primary" :disabled="!code.trim()" @click="submitCode">
                            {{ $t('aiTools.console.confirm') }}
                        </el-button>
                    </div>
                </li>
                <li v-else>{{ $t('aiTools.console.loginFollowOutput') }}</li>
            </ol>

            <!-- 不用粘码时，终端输出**本身就是**那份说明（一次性码在里面），
                 所以默认展开；要粘码时它只是排障用的，默认收起。 -->
            <el-collapse :model-value="needsCodeInput ? [] : ['raw']">
                <el-collapse-item name="raw" :title="$t('aiTools.console.rawOutput')">
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
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MsgSuccess } from '@/utils/message';
import { listHarnesses } from '@/api/modules/vipanel';
import { ViPanel } from '@/api/interface/vipanel';

const { t, te } = useI18n();
const emit = defineEmits<{ (e: 'done'): void }>();

const visible = ref(false);
const running = ref(false);
const harness = ref('claude-code');
const harnessName = ref('');
const modes = ref<ViPanel.Caps['loginModes']>([]);
const mode = ref('');
const urls = ref<string[]>([]);
const code = ref('');
const raw = ref('');
let ws: WebSocket | undefined;

const decode = (b64: string) => new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
const encode = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)));

const title = computed(() =>
    harnessName.value
        ? `${t('aiTools.console.agentLogin')} · ${harnessName.value}`
        : t('aiTools.console.agentLogin'),
);

// 文案按 id 查 i18n，查不到就把 id 原样显示。
// 加一个新 harness 时忘了补文案，界面上是一个能用但难看的按钮，
// 而不是一处空白——这条比「缺 key 就报错」更适合这里。
const modeLabel = (m: string) => (te(`aiTools.console.loginMode.${m}`) ? t(`aiTools.console.loginMode.${m}`) : m);
const modeNote = (m: string) => (te(`aiTools.console.loginMode.${m}Note`) ? t(`aiTools.console.loginMode.${m}Note`) : '');

// 不知道就当成要粘码——那是原来的行为，多一个输入框比少一个安全。
const needsCodeInput = computed(() => modes.value.find((m) => m.id === mode.value)?.needsCodeInput ?? true);

const open = async (id?: string) => {
    visible.value = true;
    running.value = false;
    urls.value = [];
    code.value = '';
    raw.value = '';
    harness.value = id || 'claude-code';
    harnessName.value = '';
    modes.value = [];
    mode.value = '';
    try {
        const hs: ViPanel.Harness[] = (await listHarnesses()).data || [];
        const h = hs.find((x) => x.id === harness.value);
        harnessName.value = h?.displayName || '';
        modes.value = h?.capabilities?.loginModes || [];
    } catch {
        /* 取不到就让后端用它自己的默认方式，不要把登录整个卡死 */
    }
    mode.value = modes.value[0]?.id || '';
};

const start = () => {
    running.value = true;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const q = new URLSearchParams({ harness: harness.value });
    if (mode.value) q.set('mode', mode.value);
    ws = new WebSocket(`${proto}://${location.host}/api/v2/ai/console/auth/login?${q}`);
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
