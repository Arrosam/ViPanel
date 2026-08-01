<template>
    <div class="vp-chat" :class="{ 'is-drop': dropping }" @dragover="onDragOver" @dragleave="dropping = false" @drop="onDrop">
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
            <div v-if="attachments.length" class="vp-chips">
                <span v-for="(a, i) in attachments" :key="a" class="vp-chip">
                    {{ a.split('/').pop() }}
                    <el-icon class="vp-chip__x" @click="attachments.splice(i, 1)"><Close /></el-icon>
                </span>
            </div>

            <div class="vp-input">
                <span v-if="bang" class="vp-bang">bash</span>
                <el-input
                    v-model="draft"
                    type="textarea"
                    :rows="2"
                    resize="none"
                    :placeholder="$t('aiTools.console.inputHint')"
                    @input="onInput"
                    @keydown.enter.exact.prevent="send"
                    @keydown.esc="closeAc"
                />
                <div v-if="ac.open && ac.items.length" class="vp-ac">
                    <div
                        v-for="(it, i) in ac.items"
                        :key="it"
                        class="vp-ac__i"
                        :class="{ on: i === ac.idx }"
                        @mousedown.prevent="pickAc(it)"
                    >
                        {{ it }}
                    </div>
                </div>
            </div>
            <div class="vp-chat__acts">
                <el-button link :icon="Paperclip" :title="$t('aiTools.console.attach')" @click="pickFile" />
                <input ref="fileEl" type="file" multiple hidden @change="onPick" />

                <el-button v-if="caps.interrupt" link size="small" @click="emit('control', 'mode', '')">
                    {{ mode || $t('aiTools.console.mode') }}
                </el-button>

                <span v-if="busy" class="vp-chat__busy">{{ $t('aiTools.console.status.working') }}</span>
                <div class="grow" />

                <!-- 模型与 effort 由 harness 的能力表决定有没有，不是写死的 -->
                <el-select
                    v-if="caps.models?.length"
                    class="vp-sel"
                    :model-value="''"
                    size="small"
                    :placeholder="$t('aiTools.console.model')"
                    @change="(v) => emit('control', 'model', v)"
                >
                    <el-option v-for="m in caps.models" :key="m" :value="m" :label="m" />
                </el-select>
                <el-select
                    v-if="caps.effortLevels?.length"
                    class="vp-sel"
                    :model-value="''"
                    size="small"
                    placeholder="effort"
                    @change="(v) => emit('control', 'effort', v)"
                >
                    <el-option v-for="e in caps.effortLevels" :key="e" :value="e" :label="e" />
                </el-select>
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
import { computed, nextTick, ref, watch } from 'vue';
import { Close, Paperclip } from '@element-plus/icons-vue';
import { MdPreview } from 'md-editor-v3';
import 'md-editor-v3/lib/preview.css';

const props = defineProps<{
    events: any[];
    busy: boolean;
    canInterrupt: boolean;
    caps: any;
    mode: string;
    cwd: string;
}>();

const emit = defineEmits<{
    (e: 'send', text: string, attachments: string[]): void;
    (e: 'interrupt'): void;
    (e: 'control', kind: string, value: string): void;
}>();

const attachments = ref<string[]>([]);
const fileEl = ref<HTMLInputElement | null>(null);
const ac = ref<{ open: boolean; items: string[]; idx: number; from: number }>({ open: false, items: [], idx: 0, from: 0 });

// 行首的 ! 切 bash 模式。指示器必须可见——否则用户不知道这一行
// 会被当成 shell 命令执行，那是个能造成实际后果的误解。
const bang = computed(() => draft.value.startsWith('!'));

const pickFile = () => fileEl.value?.click();

// 从文件面板拖进来 = 变附件。拖的是路径不是内容——
// agent 是本机进程，它自己能读，没必要把文件搬一趟。
const dropping = ref(false);
const onDragOver = (ev: DragEvent) => {
    if (!ev.dataTransfer?.types.includes('application/x-vp-path')) return;
    ev.preventDefault();
    dropping.value = true;
};
const onDrop = (ev: DragEvent) => {
    dropping.value = false;
    const p = ev.dataTransfer?.getData('application/x-vp-path');
    if (!p) return;
    ev.preventDefault();
    if (!attachments.value.includes(p)) attachments.value.push(p);
};

const onPick = async (e: Event) => {
    const input = e.target as HTMLInputElement;
    for (const f of Array.from(input.files || [])) {
        // 附件先落到会话目录，再把路径交给 agent——
        // agent 是本机进程，给它路径比给它内容更自然
        const fd = new FormData();
        fd.append('file', f);
        fd.append('path', props.cwd);
        const csrf = document.cookie.split('; ').find((c) => c.startsWith('pcsrftoken='))?.split('=')[1];
        await fetch('/api/v2/files/upload', { method: 'POST', body: fd, headers: csrf ? { 'X-CSRF-Token': csrf } : {} });
        attachments.value.push(`${props.cwd}/${f.name}`);
    }
    input.value = '';
};

// @ 补全：候选来自真实目录，不是猜的
const onInput = async () => {
    const v = draft.value;

    // 斜杠命令：候选来自 harness 的 Capabilities.Commands，不是前端写死的。
    // 换一个 harness，这个列表就该跟着换。
    if (v.startsWith('/') && !v.includes(' ')) {
        const cmds = (props.caps?.commands || []) as { name: string; desc: string }[];
        const hit = cmds.filter((c) => c.name.startsWith(v)).map((c) => `${c.name}  ${c.desc}`);
        ac.value = { open: hit.length > 0, items: hit.slice(0, 10), idx: 0, from: 0 };
        return;
    }

    const at = v.lastIndexOf('@');
    if (at < 0 || /\s/.test(v.slice(at + 1))) return closeAc();
    const frag = v.slice(at + 1);
    try {
        const csrf = document.cookie.split('; ').find((c) => c.startsWith('pcsrftoken='))?.split('=')[1];
        const res = await fetch('/api/v2/files/search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) },
            body: JSON.stringify({ path: props.cwd, expand: true, page: 1, pageSize: 200, showHidden: false }),
        }).then((r) => r.json());
        const names = (res?.data?.items || []).map((i: any) => i.name).filter((n: string) => n.toLowerCase().startsWith(frag.toLowerCase()));
        ac.value = { open: names.length > 0, items: names.slice(0, 8), idx: 0, from: at };
    } catch {
        closeAc();
    }
};

const closeAc = () => (ac.value = { open: false, items: [], idx: 0, from: 0 });

const pickAc = (item: string) => {
    // 斜杠命令的候选带着说明文字，取第一段才是命令本身
    if (item.startsWith('/')) {
        draft.value = item.split('  ')[0] + ' ';
    } else {
        draft.value = draft.value.slice(0, ac.value.from) + '@' + item + ' ';
    }
    closeAc();
};

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
    if (!t && !attachments.value.length) return;
    emit('send', t, [...attachments.value]);
    draft.value = '';
    attachments.value = [];
    closeAc();
};

const brief = (s: string) => {
    if (!s) return '';
    const one = s.replace(/\s+/g, ' ').trim();
    return one.length > 160 ? one.slice(0, 160) + '…' : one;
};
</script>

<style lang="scss" scoped>
.vp-chat.is-drop {
    outline: 2px dashed var(--el-color-primary);
    outline-offset: -4px;
}
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

.vp-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    padding-bottom: 6px;
}
.vp-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 6px;
    border-radius: 4px;
    background: var(--el-fill-color);
    font: 11px/1.6 var(--el-font-family-mono, monospace);
}
.vp-chip__x {
    cursor: pointer;
    opacity: 0.6;
}
.vp-chip__x:hover {
    opacity: 1;
}

.vp-input {
    position: relative;
}
.vp-bang {
    position: absolute;
    top: 6px;
    left: 8px;
    z-index: 2;
    padding: 0 5px;
    border-radius: 3px;
    background: var(--el-color-warning);
    color: #fff;
    font: 600 10px/1.6 var(--el-font-family-mono, monospace);
}
.vp-ac {
    position: absolute;
    left: 0;
    bottom: 100%;
    z-index: 10;
    min-width: 220px;
    max-height: 200px;
    overflow-y: auto;
    background: var(--el-bg-color-overlay);
    border: 1px solid var(--el-border-color-light);
    border-radius: 6px;
    box-shadow: var(--el-box-shadow-light);
}
.vp-ac__i {
    padding: 5px 10px;
    font: 11px/1.5 var(--el-font-family-mono, monospace);
    cursor: pointer;
}
.vp-ac__i.on,
.vp-ac__i:hover {
    background: var(--el-fill-color-light);
}
.vp-sel {
    width: 92px;
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
