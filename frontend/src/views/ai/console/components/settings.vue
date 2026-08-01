<template>
    <el-dialog v-model="visible" :title="$t('aiTools.console.settings')" width="560px" @open="load">
        <el-form label-position="top">
            <el-form-item :label="$t('aiTools.console.poolSize')">
                <el-input-number v-model="size" :min="1" :max="16" :step="1" />
                <div class="vp-set__note">{{ $t('aiTools.console.poolHint') }}</div>
                <div class="vp-set__note">{{ $t('aiTools.console.poolState', [pool.active, pool.size]) }}</div>
            </el-form-item>

            <el-form-item :label="$t('aiTools.console.harnesses')">
                <div v-for="h in harnesses" :key="h.id" class="vp-set__h">
                    <b>{{ h.displayName }}</b>
                    <span class="vp-set__cap">
                        {{ h.capabilities.structuredEvents ? $t('aiTools.console.capChat') : $t('aiTools.console.capTermOnly') }}
                    </span>
                    <template v-if="h.id === 'claude-code'">
                        <span v-if="auth.loggedIn" class="vp-set__ok">{{ auth.email }} · {{ auth.plan }}</span>
                        <span v-else class="vp-set__bad">{{ $t('aiTools.console.notLoggedIn') }}</span>
                        <el-button v-if="auth.loggedIn" link type="danger" size="small" @click="doLogout">
                            {{ $t('aiTools.console.logout') }}
                        </el-button>
                        <el-button v-else link type="primary" size="small" @click="emit('login')">
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
import { getPool, updatePool, listHarnesses, getAgentAuth, agentLogout } from '@/api/modules/vipanel';
import { ViPanel } from '@/api/interface/vipanel';
import { MsgError, MsgSuccess } from '@/utils/message';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();
const emit = defineEmits<{ (e: 'changed'): void; (e: 'login'): void }>();

const visible = ref(false);
const size = ref(2);
const pool = ref<ViPanel.Pool>({ size: 2, active: 0, order: [] });
const harnesses = ref<ViPanel.Harness[]>([]);
const auth = ref<ViPanel.AuthState>({ supported: false, loggedIn: true, hookInstalled: true });

const load = async () => {
    const [p, hs, a] = await Promise.all([getPool(), listHarnesses(), getAgentAuth()]);
    pool.value = p.data;
    size.value = p.data.size;
    harnesses.value = hs.data || [];
    auth.value = a.data;
};

const save = async () => {
    try {
        pool.value = (await updatePool(size.value)).data;
        MsgSuccess(t('aiTools.console.saved'));
        visible.value = false;
        emit('changed');
    } catch (e: any) {
        MsgError(e?.message || String(e));
    }
};

const doLogout = async () => {
    await agentLogout();
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
