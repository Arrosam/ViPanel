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

        <!-- 板块授权：第一次真正用到这个板块时问一次 -->
        <template v-else-if="isModule">
            <div class="vp-perm__title">{{ req.title }}</div>
            <div class="vp-perm__meta">
                {{ $t('aiTools.console.permOpCount', [req.opCount]) }}
                <span v-if="req.destructiveCount" class="vp-perm__warn">
                    {{ $t('aiTools.console.permDestructiveCount', [req.destructiveCount]) }}
                </span>
            </div>
            <div class="vp-perm__hint">{{ $t('aiTools.console.permModuleHint') }}</div>
        </template>

        <!-- 面板操作：显示渲染好的一句人话，原始入参折在下面 -->
        <template v-else-if="isMcp">
            <div v-if="req.danger" class="vp-perm__danger">
                {{ $t('aiTools.console.permDanger') }}
            </div>
            <div class="vp-perm__title">{{ req.title }}</div>
            <div class="vp-perm__meta">{{ req.moduleTitle }} · {{ shortTool }}</div>
            <el-collapse class="vp-perm__more">
                <el-collapse-item :title="$t('aiTools.console.permRawInput')">
                    <pre class="vp-perm__input">{{ prettyInput }}</pre>
                </el-collapse-item>
            </el-collapse>
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
                <el-button v-if="!isModule && !isMcp" @click="decide('ask')">
                    {{ $t('aiTools.console.permAsk') }}
                </el-button>
                <el-button v-if="req?.canAlways" @click="decide('always')">
                    {{ $t('aiTools.console.permAlways') }}
                </el-button>
                <el-button :type="armed ? 'danger' : 'primary'" @click="confirmAllow">
                    {{ armed ? $t('aiTools.console.permConfirmAgain') : $t('aiTools.console.permAllow') }}
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
const isModule = computed(() => props.req?.kind === 'module');
const isMcp = computed(() => props.req?.kind === 'mcp');
const shortTool = computed(() => (props.req?.tool || '').replace('mcp__vipanel__', ''));

// 危险操作要点两下。目的只是**防手滑**，不是让人再读一遍——
// 那种「手打一遍名字」的二次确认会养成机械照抄的习惯，
// 反而分走了阅读上面那行危险横幅的注意力。
const armed = ref(false);
let armTimer: ReturnType<typeof setTimeout> | undefined;
const confirmAllow = () => {
    if (!props.req?.danger) {
        decide('allow');
        return;
    }
    if (armed.value) {
        armed.value = false;
        if (armTimer) clearTimeout(armTimer);
        decide('allow');
        return;
    }
    armed.value = true;
    armTimer = setTimeout(() => (armed.value = false), 4000);
};

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
        armed.value = false;
        if (armTimer) clearTimeout(armTimer);
        if (timer) clearInterval(timer);
        if (!r) return;
        const tick = () => {
            left.value = Math.max(0, Math.round((r.deadline - Date.now()) / 1000));
        };
        tick();
        timer = setInterval(tick, 500);
    },
);

onBeforeUnmount(() => {
    if (timer) clearInterval(timer);
    if (armTimer) clearTimeout(armTimer);
});

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
.vp-perm__danger {
    margin-bottom: 10px;
    padding: 8px 12px;
    border-radius: 6px;
    font-weight: 600;
    color: var(--el-color-danger);
    background: var(--el-color-danger-light-9);
    border: 1px solid var(--el-color-danger-light-5);
}
.vp-perm__title {
    font-size: 15px;
    font-weight: 600;
    line-height: 1.5;
}
.vp-perm__meta {
    margin-top: 4px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
}
.vp-perm__warn {
    margin-left: 6px;
    color: var(--el-color-danger);
}
.vp-perm__hint {
    margin-top: 10px;
    font-size: 12px;
    line-height: 1.6;
    color: var(--el-text-color-secondary);
}
.vp-perm__more {
    margin-top: 10px;
    border-top: none;
}
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
