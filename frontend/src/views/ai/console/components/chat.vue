<template>
    <div class="vp-chat">
        <div ref="scrollEl" class="vp-chat__body" @scroll="onScroll">
            <el-empty v-if="!events.length" :image-size="56" :description="$t('aiTools.console.chatEmpty')" />

            <template v-for="(e, i) in events" :key="i">
                <!-- 只有用户的发言带气泡。assistant 的正文和工具调用不带框，
                     否则整页都是方块，反而看不出谁在说话 -->
                <div v-if="e.type === 'user'" class="vp-msg vp-msg--user">
                    <MdPreview :model-value="e.text" />
                </div>

                <div v-else-if="e.type === 'assistant'" class="vp-msg">
                    <MdPreview :model-value="e.text" />
                </div>

                <details v-else-if="e.type === 'thinking'" class="vp-think">
                    <summary>{{ $t('aiTools.console.thinking') }}</summary>
                    <div class="vp-think__body">{{ e.text }}</div>
                </details>

                <div v-else-if="e.type === 'tool'" class="vp-tool">
                    <span class="vp-tool__n">{{ e.name }}</span>
                    <span class="vp-tool__i">{{ brief(e.input) }}</span>
                </div>

                <div v-else-if="e.type === 'tool_result'" class="vp-tool vp-tool--res" :class="{ bad: e.isError }">
                    <span class="vp-tool__i">{{ brief(e.text) }}</span>
                </div>
            </template>
        </div>

        <div class="vp-chat__composer">
            <el-input
                v-model="draft"
                type="textarea"
                :rows="2"
                resize="none"
                :placeholder="$t('aiTools.console.inputHint')"
                @keydown.enter.exact.prevent="send"
            />
            <div class="vp-chat__acts">
                <span v-if="busy" class="vp-chat__busy">{{ $t('aiTools.console.status.working') }}</span>
                <div class="grow" />
                <el-button v-if="busy && canInterrupt" plain size="small" @click="emit('interrupt')">
                    {{ $t('aiTools.console.stop') }}
                </el-button>
                <el-button type="primary" size="small" :disabled="!draft.trim()" @click="send">
                    {{ $t('aiTools.console.send') }}
                </el-button>
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
import { MdPreview } from 'md-editor-v3';
import 'md-editor-v3/lib/preview.css';

const props = defineProps<{
    events: any[];
    busy: boolean;
    canInterrupt: boolean;
}>();

const emit = defineEmits<{
    (e: 'send', text: string): void;
    (e: 'interrupt'): void;
}>();

const draft = ref('');
const scrollEl = ref<HTMLDivElement | null>(null);

// 「贴着底部」是一个**滚动事件维护的状态**，不是每次追加时现算的。
// 现算要在插入前后各量一次高度，插入引起的重排会让判断永远为假，
// 结果就是自动滚动整个失效。
const stuck = ref(true);
const onScroll = () => {
    const el = scrollEl.value;
    if (!el) return;
    stuck.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
};

watch(
    () => props.events.length,
    async () => {
        if (!stuck.value) return;
        await nextTick();
        const el = scrollEl.value;
        if (el) el.scrollTop = el.scrollHeight;
    },
);

const send = () => {
    const t = draft.value.trim();
    if (!t) return;
    emit('send', t);
    draft.value = '';
};

const brief = (s: string) => {
    if (!s) return '';
    const one = s.replace(/\s+/g, ' ').trim();
    return one.length > 160 ? one.slice(0, 160) + '…' : one;
};
</script>

<style lang="scss" scoped>
.vp-chat {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-width: 0;
}

.vp-chat__body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 12px 14px;
}

.vp-msg {
    margin-bottom: 10px;
    font-size: 13px;
    min-width: 0;
}
.vp-msg--user {
    background: var(--el-fill-color-light);
    border-radius: 8px;
    padding: 6px 10px;
    margin-left: auto;
    max-width: 82%;
}

/* 思考默认折叠：它有价值，但展开着会把真正的回答挤没 */
.vp-think {
    margin-bottom: 8px;
    font-size: 12px;
    color: var(--el-text-color-secondary);
}
.vp-think__body {
    white-space: pre-wrap;
    padding: 6px 0 0 4px;
    border-left: 2px solid var(--el-border-color);
    padding-left: 8px;
}

/* 工具调用一律灰色：它是过程不是结论，不该跟正文抢注意力 */
.vp-tool {
    display: flex;
    gap: 8px;
    align-items: baseline;
    margin-bottom: 6px;
    font: 11px/1.5 var(--el-font-family-mono, monospace);
    color: var(--el-text-color-secondary);
    min-width: 0;
}
.vp-tool__n {
    flex: none;
    font-weight: 600;
}
.vp-tool__i {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.vp-tool--res {
    padding-left: 12px;
    opacity: 0.75;
}
.vp-tool--res.bad {
    color: var(--el-color-danger);
}

.vp-chat__composer {
    flex: none;
    border-top: 1px solid var(--el-border-color-lighter);
    padding: 8px 10px;
}
.vp-chat__acts {
    display: flex;
    align-items: center;
    gap: 8px;
    padding-top: 6px;
}
.vp-chat__busy {
    font-size: 11px;
    color: var(--el-color-warning);
}
.grow {
    flex: 1;
}

:deep(.md-editor-preview-wrapper) {
    padding: 0;
}
:deep(.md-editor-preview) {
    font-size: 13px;
    /* md-editor 默认 break-all，会把英文单词从中间劈开 */
    word-break: break-word;
}
:deep(.md-editor-preview p) {
    margin: 0 0 6px;
}
:deep(.md-editor-preview pre) {
    max-width: 100%;
    overflow-x: auto;
}
</style>
