<!--
  Agent 管理：控制台没有会话时的首屏。

  这块存在的理由很直接——首次登录时**一个会话都没有**，而原来的登录横幅问的是
  「当前会话那个 harness」的状态，于是它退回默认值，永远只显示 Claude 一个。
  用户看不到 Codex 存在，更不知道它能装。

  所以首屏该是「这台机器上有哪些 Agent、各自什么状态」，而不是一句
  「请选择一个会话」。
-->
<template>
    <div class="vp-am">
        <div class="vp-am__head">
            <h3>{{ $t('aiTools.console.agentManage') }}</h3>
            <p>{{ $t('aiTools.console.agentManageHint') }}</p>
        </div>

        <div class="vp-am__grid">
            <div v-for="h in harnesses" :key="h.id" class="vp-am__card">
                <div class="vp-am__title">
                    <b>{{ h.displayName }}</b>
                    <span class="vp-am__cap">
                        {{
                            h.capabilities.structuredEvents
                                ? $t('aiTools.console.capChat')
                                : $t('aiTools.console.capTermOnly')
                        }}
                    </span>
                </div>

                <!-- 三种状态各自只显示自己该显示的东西，不叠在一起 -->
                <template v-if="!h.installed">
                    <div class="vp-am__row vp-am__bad">{{ $t('aiTools.console.notInstalled') }}</div>
                    <div v-if="h.install.missing?.length" class="vp-am__note">
                        {{ h.install.missing[0].hint }}
                    </div>
                    <template v-else-if="h.install.installable">
                        <div class="vp-am__note">{{ h.install.note }}</div>
                        <el-button size="small" type="primary" @click="emit('install', h)">
                            {{ $t('aiTools.console.install') }}
                        </el-button>
                    </template>
                    <div v-else class="vp-am__note">{{ h.install.note }}</div>
                </template>

                <template v-else-if="h.capabilities.auth">
                    <div v-if="auth[h.id]?.loggedIn" class="vp-am__row vp-am__ok">
                        {{ [auth[h.id]?.email, auth[h.id]?.plan].filter(Boolean).join(' · ') }}
                    </div>
                    <div v-else class="vp-am__row vp-am__bad">{{ $t('aiTools.console.notLoggedIn') }}</div>

                    <!-- 账号列表：只有存过账号才显示，没存过时这块是纯噪音 -->
                    <div v-if="accounts[h.id]?.length" class="vp-am__accts">
                        <div v-for="a in accounts[h.id]" :key="a.id" class="vp-am__acct">
                            <el-radio
                                :model-value="a.active ? a.id : ''"
                                :value="a.id"
                                :disabled="a.active || busy"
                                @change="doActivate(h.id, a.id)"
                            >
                                {{ a.label }}
                            </el-radio>
                            <el-button link type="danger" size="small" :disabled="busy" @click="doForget(h.id, a.id)">
                                {{ $t('aiTools.console.remove') }}
                            </el-button>
                        </div>
                    </div>

                    <div class="vp-am__acts">
                        <el-button size="small" type="primary" @click="emit('login', h.id)">
                            {{ auth[h.id]?.loggedIn ? $t('aiTools.console.addAccount') : $t('aiTools.console.signIn') }}
                        </el-button>
                        <!-- 「记住当前账号」只在已登录、且当前这个还没被存过时才有意义 -->
                        <el-button
                            v-if="auth[h.id]?.loggedIn && !hasActiveSaved(h.id)"
                            size="small"
                            :disabled="busy"
                            @click="doCapture(h.id)"
                        >
                            {{ $t('aiTools.console.saveAccount') }}
                        </el-button>
                    </div>
                </template>

                <template v-else>
                    <div class="vp-am__row vp-am__ok">{{ $t('aiTools.console.ready') }}</div>
                    <div class="vp-am__note">{{ h.install.note }}</div>
                </template>
            </div>
        </div>

        <div class="vp-am__foot">
            <el-button type="primary" :disabled="!anyUsable" @click="emit('new-session')">
                {{ $t('aiTools.console.newSession') }}
            </el-button>
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElMessageBox } from 'element-plus';
import {
    listHarnesses,
    getAgentAuth,
    listAccounts,
    captureAccount,
    activateAccount,
    deleteAccount,
} from '@/api/modules/vipanel';
import { ViPanel } from '@/api/interface/vipanel';
import { MsgError, MsgSuccess } from '@/utils/message';

const { t } = useI18n();
const emit = defineEmits<{
    (e: 'login', harness: string): void;
    (e: 'install', h: ViPanel.Harness): void;
    (e: 'new-session'): void;
}>();

const harnesses = ref<ViPanel.Harness[]>([]);
const auth = ref<Record<string, ViPanel.AuthState>>({});
const accounts = ref<Record<string, ViPanel.Account[]>>({});
const busy = ref(false);

// 能不能建会话：至少要有一个装好的 harness。
// shell 永远算数，所以这个条件实际上总能满足——但写出来是为了让
// 「一个都没装」时按钮是灰的，而不是点了报一句看不懂的错。
const anyUsable = computed(() => harnesses.value.some((h) => h.installed));

const hasActiveSaved = (id: string) => (accounts.value[id] || []).some((a) => a.active);

const load = async () => {
    harnesses.value = (await listHarnesses()).data || [];
    // 只问装好了、且有登录体系的那些：问一个没装的 harness
    // 就是白等一次 exec 失败。
    const need = harnesses.value.filter((h) => h.installed && h.capabilities.auth);
    const results = await Promise.all(
        need.map(async (h) => ({
            id: h.id,
            state: await getAgentAuth(h.id).then((r) => r.data).catch(() => null),
            accts: await listAccounts(h.id).then((r) => r.data).catch(() => []),
        })),
    );
    const a: Record<string, ViPanel.AuthState> = {};
    const c: Record<string, ViPanel.Account[]> = {};
    for (const r of results) {
        if (r.state) a[r.id] = r.state;
        c[r.id] = r.accts || [];
    }
    auth.value = a;
    accounts.value = c;
};

const doCapture = async (harness: string) => {
    busy.value = true;
    try {
        await captureAccount(harness);
        await load();
        MsgSuccess(t('aiTools.console.accountSaved'));
    } catch (e: any) {
        MsgError(e?.message || String(e));
    } finally {
        busy.value = false;
    }
};

const doActivate = async (harness: string, id: string) => {
    busy.value = true;
    try {
        const res = await activateAccount(harness, id);
        await load();
        // 切换只改磁盘。跑着的会话在启动时就把凭据读进内存了，
        // 不说清楚的话用户会以为切了、实际还在用旧账号。
        if (res.data?.needRestart) {
            MsgSuccess(t('aiTools.console.accountSwitchedRestart'));
        } else {
            MsgSuccess(t('aiTools.console.accountSwitched'));
        }
    } catch (e: any) {
        MsgError(e?.message || String(e));
    } finally {
        busy.value = false;
    }
};

const doForget = async (harness: string, id: string) => {
    await ElMessageBox.confirm(t('aiTools.console.removeAccountConfirm'), { type: 'warning' });
    busy.value = true;
    try {
        await deleteAccount(harness, id);
        await load();
    } catch (e: any) {
        MsgError(e?.message || String(e));
    } finally {
        busy.value = false;
    }
};

onMounted(load);

defineExpose({ load });
</script>

<style lang="scss" scoped>
.vp-am {
    max-width: 860px;
    margin: 0 auto;
    padding: 32px 24px;
}
.vp-am__head h3 {
    margin: 0 0 6px;
    font-size: 17px;
    font-weight: 600;
}
.vp-am__head p {
    margin: 0 0 20px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
}
.vp-am__grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
    gap: 14px;
}
.vp-am__card {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 14px;
    border: 1px solid var(--el-border-color-lighter);
    border-radius: 8px;
    background: var(--el-bg-color);
}
.vp-am__title {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
}
.vp-am__cap {
    font-size: 11px;
    color: var(--el-text-color-secondary);
}
.vp-am__row {
    font-size: 12px;
}
.vp-am__ok {
    color: var(--el-color-success);
}
.vp-am__bad {
    color: var(--el-color-danger);
}
.vp-am__note {
    font-size: 11px;
    line-height: 1.6;
    color: var(--el-text-color-secondary);
}
.vp-am__accts {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 0;
    border-top: 1px solid var(--el-border-color-lighter);
}
.vp-am__acct {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 6px;
}
.vp-am__acts {
    display: flex;
    gap: 8px;
    margin-top: auto;
    padding-top: 4px;
}
.vp-am__foot {
    margin-top: 22px;
    text-align: center;
}
</style>
