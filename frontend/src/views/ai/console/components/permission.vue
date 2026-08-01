<template>
    <el-dialog
        :model-value="!!req"
        :title="isAsk ? $t('aiTools.console.agentAsks') : $t('aiTools.console.permTitle')"
        width="620px"
        :close-on-click-modal="false"
        :show-close="false"
    >
        <!-- agent 中途提问：原生渲染成选择控件，而不是让用户去猜终端里发生了什么 -->
        <template v-if="isAsk">
            <div v-for="(q, qi) in req.questions" :key="qi" class="vp-perm__q">
                <div class="vp-perm__qt">{{ q.question }}</div>
                <el-checkbox-group v-if="q.multiSelect" v-model="multi[qi]">
                    <el-checkbox v-for="o in q.options" :key="o.label" :value="o.label" class="vp-perm__opt">
                        <b>{{ o.label }}</b>
                        <i v-if="o.description">{{ o.description }}</i>
                    </el-checkbox>
                </el-checkbox-group>
                <el-radio-group v-else v-model="single[qi]">
                    <el-radio v-for="o in q.options" :key="o.label" :value="o.label" class="vp-perm__opt">
                        <b>{{ o.label }}</b>
                        <i v-if="o.description">{{ o.description }}</i>
                    </el-radio>
                </el-radio-group>
            </div>
        </template>

        <template v-else>
            <div class="vp-perm__tool">{{ req?.tool }}</div>
            <pre class="vp-perm__input">{{ prettyInput }}</pre>
        </template>

        <template #footer>
            <span class="vp-perm__count">{{ left }}s</span>
            <template v-if="isAsk">
                <el-button type="primary" :disabled="!answered" @click="answer">
                    {{ $t('aiTools.console.confirm') }}
                </el-button>
            </template>
            <template v-else>
                <el-button type="danger" plain @click="decide('deny')">
                    {{ $t('aiTools.console.permDeny') }}
                </el-button>
                <el-button @click="decide('ask')">{{ $t('aiTools.console.permAsk') }}</el-button>
                <el-button type="primary" @click="decide('allow')">
                    {{ $t('aiTools.console.permAllow') }}
                </el-button>
            </template>
        </template>
    </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue';
import { resolvePermission } from '@/api/modules/vipanel';

const props = defineProps<{ req: any }>();

const single = ref<Record<number, string>>({});
const multi = ref<Record<number, string[]>>({});
const left = ref(0);
let timer: ReturnType<typeof setInterval> | undefined;

const isAsk = computed(() => !!props.req?.questions?.length);

const prettyInput = computed(() => {
    if (!props.req) return '';
    try {
        return JSON.stringify(props.req.input, null, 2);
    } catch {
        return String(props.req.input);
    }
});

const answered = computed(() => {
    if (!props.req?.questions) return false;
    return props.req.questions.every((q: any, i: number) =>
        q.multiSelect ? (multi.value[i] || []).length > 0 : !!single.value[i],
    );
});

// 倒计时读的是服务端给的 deadline，不是本地起算的秒数：
// 页面是后打开的、或者标签页被挂起过时，本地计时会和服务端对不上。
watch(
    () => props.req,
    (r) => {
        single.value = {};
        multi.value = {};
        if (timer) clearInterval(timer);
        if (!r) return;
        const tick = () => {
            left.value = Math.max(0, Math.round((r.deadline - Date.now()) / 1000));
        };
        tick();
        timer = setInterval(tick, 500);
    },
);

onBeforeUnmount(() => timer && clearInterval(timer));

const decide = async (d: string) => {
    await resolvePermission(props.req.id, d, '');
};

// AskUserQuestion 的答案靠 deny 的 reason 回传——
// hook 只有 allow/deny/ask 三种输出，没有「返回一个值」的通道，
// 但 claude 会把 deny 的理由当作反馈接受并据此继续（已实测）。
const answer = async () => {
    const parts = props.req.questions.map((q: any, i: number) => {
        const v = q.multiSelect ? (multi.value[i] || []).join('、') : single.value[i];
        return `${q.question} → ${v}`;
    });
    await resolvePermission(
        props.req.id,
        'deny',
        `用户已在面板上作答：${parts.join('；')}。请直接按这个答复继续，不要重复提问。`,
    );
};
</script>

<style lang="scss" scoped>
.vp-perm__tool {
    font-weight: 600;
    margin-bottom: 6px;
}
.vp-perm__input {
    max-height: 260px;
    overflow: auto;
    margin: 0;
    padding: 8px 10px;
    border-radius: 6px;
    background: var(--el-fill-color-light);
    font: 11px/1.5 var(--el-font-family-mono, monospace);
    white-space: pre-wrap;
    word-break: break-all;
}
.vp-perm__q {
    margin-bottom: 14px;
}
.vp-perm__qt {
    font-size: 13px;
    font-weight: 600;
    margin-bottom: 8px;
}
.vp-perm__opt {
    display: flex;
    align-items: baseline;
    gap: 8px;
    height: auto;
    margin: 0 0 6px;
    white-space: normal;
}
.vp-perm__opt i {
    font-style: normal;
    font-size: 12px;
    color: var(--el-text-color-secondary);
}
.vp-perm__count {
    float: left;
    line-height: 32px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
}
</style>
