<template>
    <el-dialog v-model="visible" :title="$t('aiTools.console.settings')" width="560px" @open="load">
        <el-form label-position="top">
            <el-form-item :label="$t('aiTools.console.poolSize')">
                <el-input-number v-model="size" :min="1" :max="16" :step="1" />
                <div class="vp-set__note">{{ $t('aiTools.console.poolHint') }}</div>
                <div class="vp-set__note">{{ $t('aiTools.console.poolState', [pool.active, pool.size]) }}</div>
            </el-form-item>

            <el-form-item :label="$t('aiTools.console.mcpTitle')">
                <el-switch v-model="mcp.enabled" :disabled="!mcp.installed" />
                <div class="vp-set__note">{{ $t('aiTools.console.mcpHint') }}</div>
                <div v-if="!mcp.installed" class="vp-set__bad">{{ $t('aiTools.console.mcpMissing') }}</div>
                <div v-else class="vp-set__note">{{ $t('aiTools.console.mcpState', [mcp.opCount]) }}</div>
            </el-form-item>

            <!-- 网络代理。
                 存在的原因是实测：香港的服务器 TCP/TLS 都正常，但
                 api.anthropic.com 和 api.openai.com 按出口地区回 403。
                 让控制台的出站走本机 VPN 客户端开的本地端口即可。 -->
            <el-form-item :label="$t('aiTools.console.proxyTitle')">
                <el-switch v-model="proxy.enabled" />
                <div class="vp-set__note">{{ $t('aiTools.console.proxyHint') }}</div>
                <template v-if="proxy.enabled">
                    <el-input
                        v-model="proxy.url"
                        class="vp-set__proxy"
                        spellcheck="false"
                        placeholder="http://127.0.0.1:7890"
                    />
                    <div class="vp-set__note">{{ $t('aiTools.console.proxyExample') }}</div>
                </template>

                <!-- 测试按钮：配完之后必须能当场验证。
                     不给验证手段的话，配错了的表现是「登录卡住」，
                     和不配代理时一模一样，用户无从判断。 -->
                <div class="vp-set__proxyacts">
                    <el-button size="small" :loading="checking" @click="doCheck">
                        {{ $t('aiTools.console.proxyTest') }}
                    </el-button>
                </div>
                <div v-for="r in reach" :key="r.url" class="vp-set__reach">
                    <span :class="r.ok ? 'vp-set__ok' : 'vp-set__bad'">{{ r.ok ? '✓' : '✗' }}</span>
                    <span>{{ r.purpose }}</span>
                    <span class="vp-set__note">{{ r.ok ? $t('aiTools.console.proxyReachOk') : r.detail }}</span>
                </div>
            </el-form-item>

            <el-form-item :label="$t('aiTools.console.harnesses')">
                <!-- 登录那一块由 harness 的能力表决定显不显示，不再按 id 判断。
                     写死 id === 'claude-code' 的那一版，加一个 harness 就得改这里。 -->
                <div v-for="h in harnesses" :key="h.id" class="vp-set__h">
                    <b>{{ h.displayName }}</b>
                    <span class="vp-set__cap">
                        {{ h.capabilities.structuredEvents ? $t('aiTools.console.capChat') : $t('aiTools.console.capTermOnly') }}
                    </span>
                    <template v-if="!h.installed">
                        <span class="vp-set__bad">{{ $t('aiTools.console.notInstalled') }}</span>
                        <!-- 缺前置依赖时显示缺什么，而不是给一个注定失败的按钮 -->
                        <span v-if="h.install.missing?.length" class="vp-set__note">
                            {{ h.install.missing[0].hint }}
                        </span>
                        <el-button
                            v-else-if="h.install.installable"
                            link
                            type="primary"
                            size="small"
                            @click="installRef?.open(h.id, h.displayName, h.install.note)"
                        >
                            {{ $t('aiTools.console.install') }}
                        </el-button>
                        <span v-else class="vp-set__note">{{ h.install.note }}</span>
                    </template>
                    <template v-else-if="h.capabilities.auth">
                        <span v-if="auth[h.id]?.loggedIn" class="vp-set__ok">
                            {{ [auth[h.id]?.email, auth[h.id]?.plan].filter(Boolean).join(' · ') }}
                        </span>
                        <span v-else class="vp-set__bad">{{ $t('aiTools.console.notLoggedIn') }}</span>
                        <el-button
                            v-if="auth[h.id]?.loggedIn"
                            link
                            type="danger"
                            size="small"
                            @click="doLogout(h.id)"
                        >
                            {{ $t('aiTools.console.logout') }}
                        </el-button>
                        <el-button v-else link type="primary" size="small" @click="emit('login', h.id)">
                            {{ $t('aiTools.console.loginNow') }}
                        </el-button>
                    </template>
                </div>
            </el-form-item>
        </el-form>

        <template #footer>
            <el-button @click="visible = false">{{ $t('aiTools.console.cancel') }}</el-button>
            <el-button type="primary" @click="save">{{ $t('aiTools.console.save') }}</el-button>
        </template>
    </el-dialog>

    <AgentInstall ref="installRef" @done="onInstalled" />
</template>

<script setup lang="ts">
import { ref } from 'vue';
import AgentInstall from './agent-install.vue';
import {
    getPool,
    updatePool,
    listHarnesses,
    getAgentAuth,
    agentLogout,
    getMcpSetting,
    updateMcpSetting,
    getOutbound,
    updateProxy,
    checkReachability,
} from '@/api/modules/vipanel';
import { ViPanel } from '@/api/interface/vipanel';
import { MsgError, MsgSuccess } from '@/utils/message';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();
const emit = defineEmits<{ (e: 'changed'): void; (e: 'login', harness: string): void }>();

const visible = ref(false);
const size = ref(2);
const pool = ref<ViPanel.Pool>({ size: 2, active: 0, order: [] });
const harnesses = ref<ViPanel.Harness[]>([]);
// 登录状态**每个 harness 各一份**：Claude 登录了不代表 Codex 登录了。
const auth = ref<Record<string, ViPanel.AuthState>>({});
const mcp = ref({ enabled: true, installed: false, opCount: 0 });
const installRef = ref();
const proxy = ref<ViPanel.Outbound>({ enabled: false, url: '' });
const reach = ref<ViPanel.ReachResult[]>([]);
const checking = ref(false);

// 测试前**先把当前填的代理存下来**，否则测的是上一次保存的值，
// 用户会以为改了地址没生效。
const doCheck = async () => {
    checking.value = true;
    reach.value = [];
    try {
        await updateProxy(proxy.value.enabled, proxy.value.url);
        const targets = harnesses.value.filter((h) => h.installed && h.capabilities.auth);
        const all = await Promise.all(targets.map((h) => checkReachability(h.id).then((r) => r.data || [])));
        reach.value = all.flat();
    } catch (e: any) {
        MsgError(e?.message || String(e));
    } finally {
        checking.value = false;
    }
};

// 装完之后必须重新拉一次列表：installed / 登录状态都变了，
// 界面继续显示「未安装」就是在撒谎。
const onInstalled = async (installed: boolean) => {
    await load();
    if (installed) {
        MsgSuccess(t('aiTools.console.installOk'));
        emit('changed');
    }
};

const load = async () => {
    const [p, hs, m, ob] = await Promise.all([getPool(), listHarnesses(), getMcpSetting(), getOutbound()]);
    pool.value = p.data;
    size.value = p.data.size;
    harnesses.value = hs.data || [];
    mcp.value = { ...mcp.value, ...m.data };
    proxy.value = ob.data;
    reach.value = [];

    // 只问装了、且自己有登录体系的那些。挨个问一个没装的 harness
    // 就是挨个等一次 exec 失败。
    const need = harnesses.value.filter((h) => h.installed && h.capabilities.auth);
    const states = await Promise.all(
        need.map((h) =>
            getAgentAuth(h.id)
                .then((r) => r.data)
                .catch(() => null),
        ),
    );
    const next: Record<string, ViPanel.AuthState> = {};
    need.forEach((h, i) => {
        if (states[i]) next[h.id] = states[i] as ViPanel.AuthState;
    });
    auth.value = next;
};

const save = async () => {
    try {
        pool.value = (await updatePool(size.value)).data;
        await updateMcpSetting(mcp.value.enabled);
        await updateProxy(proxy.value.enabled, proxy.value.url);
        MsgSuccess(t('aiTools.console.saved'));
        visible.value = false;
        emit('changed');
    } catch (e: any) {
        MsgError(e?.message || String(e));
    }
};

const doLogout = async (harness: string) => {
    await agentLogout(harness);
    await load();
    emit('changed');
};

defineExpose({ show: () => (visible.value = true) });
</script>

<style lang="scss" scoped>
.vp-set__proxy {
    margin-top: 6px;
}
.vp-set__proxyacts {
    margin-top: 8px;
}
.vp-set__reach {
    display: flex;
    align-items: baseline;
    gap: 6px;
    margin-top: 4px;
    font-size: 11px;
    line-height: 1.6;
}
.vp-set__note {
    font-size: 11px;
    line-height: 1.6;
    color: var(--el-text-color-secondary);
}
.vp-set__h {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 6px 0;
    border-bottom: 1px solid var(--el-border-color-lighter);
}
.vp-set__cap {
    font-size: 11px;
    color: var(--el-text-color-secondary);
}
.vp-set__ok {
    margin-left: auto;
    font-size: 11px;
    color: var(--el-color-success);
}
.vp-set__bad {
    margin-left: auto;
    font-size: 11px;
    color: var(--el-color-warning);
}
</style>
