<template>
    <div class="vp-console">
        <div class="vp-console__bar">
            <el-input
                v-model="cwd"
                class="vp-console__cwd"
                :placeholder="$t('aiTools.console.cwdPlaceholder')"
                spellcheck="false"
                @keyup.enter="reconnect"
            >
                <template #prefix>
                    <el-icon><FolderOpened /></el-icon>
                </template>
            </el-input>
            <el-button plain :disabled="connecting" @click="reconnect">
                {{ $t('commons.button.conn') }}
            </el-button>
        </div>
        <div class="vp-console__term">
            <Terminal :key="connId" ref="terminalRef" />
        </div>
    </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import Terminal from '@/components/terminal/index.vue';

defineOptions({ name: 'Console' });

const terminalRef = ref();
const cwd = ref('');
const connecting = ref(false);
const connId = ref(0);

const connect = () => {
    const args = cwd.value.trim() ? `cwd=${encodeURIComponent(cwd.value.trim())}` : '';
    terminalRef.value?.acceptParams({
        endpoint: '/api/v2/ai/console/pty',
        args,
        error: '',
        initCmd: '',
    });
};

// 重连走整组件重挂载，而不是在原组件上 onClose + 重连。
//
// 原因是上游 Terminal 的 closeRealTerminal 没有做 token 校验：旧 socket 的
// close 事件晚到时，它会往**新**终端里写 "The connection has been disconnected."，
// 并且把 terminalSocket 置成 undefined —— 后者会让 isWsOpen() 变 false，
// 键盘输入被直接丢弃，终端看着还在却打不进字。
// 换 key 之后旧实例连同它的 term 一起销毁，那次晚到的写入落在已死的实例上，
// 卸载钩子也会把 socket 正常关掉，后端的 pty 随之退出。
const reconnect = async () => {
    connecting.value = true;
    connId.value += 1;
    await nextTick();
    connect();
    connecting.value = false;
};

onMounted(() => connect());
onBeforeUnmount(() => terminalRef.value?.onClose());
</script>

<style lang="scss" scoped>
.vp-console {
    display: flex;
    flex-direction: column;
    /* 顶栏 + 面包屑之外的整个高度，终端要占满，不能靠内容撑 */
    height: calc(100vh - 130px);
    min-height: 320px;
    gap: 8px;
}

.vp-console__bar {
    display: flex;
    gap: 8px;
    flex: none;
}

.vp-console__cwd {
    max-width: 520px;
}

.vp-console__term {
    flex: 1;
    min-height: 0;
    border-radius: 6px;
    overflow: hidden;
}
</style>
