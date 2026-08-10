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
</template>

<script setup lang="ts">
import { ref } from 'vue';
import {
    getPool,
    updatePool,
    listHarnesses,
    getAgentAuth,
    agentLogout,
    getMcpSetting,
    updateMcpSetting,
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

const load = async () => {
    const [p, hs, m] = await Promise.all([getPool(), listHarnesses(), getMcpSetting()]);
    pool.value = p.data;
    size.value = p.data.size;
    harnesses.value = hs.data || [];
    mcp.value = { ...mcp.value, ...m.data };

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
