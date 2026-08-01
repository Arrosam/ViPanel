<template>
    <el-dialog v-model="visible" :title="$t('aiTools.console.history')" width="680px">
        <el-empty v-if="!loading && !items.length" :image-size="56" :description="$t('aiTools.console.historyEmpty')" />
        <div v-loading="loading" class="vp-hist">
            <div v-for="h in items" :key="h.id" class="vp-hist__item" @click="open(h)">
                <div class="vp-hist__top">
                    <b>{{ h.title }}</b>
                    <i>{{ ago(h.mtime) }} · {{ fmtSize(h.size) }}</i>
                </div>
                <span class="vp-hist__cwd">{{ h.cwd }}</span>
            </div>
        </div>
    </el-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { listHistory, openHistory } from '@/api/modules/vipanel';
import { ViPanel } from '@/api/interface/vipanel';
import { MsgError } from '@/utils/message';

const emit = defineEmits<{ (e: 'opened', id: string): void }>();
const visible = ref(false);
const loading = ref(false);
const items = ref<ViPanel.History[]>([]);

const load = async () => {
    loading.value = true;
    try {
        items.value = (await listHistory()).data || [];
    } finally {
        loading.value = false;
    }
};

const show = async () => {
    visible.value = true;
    items.value = [];
    await load();
};

const open = async (h: ViPanel.History) => {
    try {
        const s = await openHistory(h.id, h.cwd, h.title);
        visible.value = false;
        emit('opened', s.data.id);
    } catch (e: any) {
        MsgError(e?.message || String(e));
    }
};

// 相对时间：列表里绝对时间戳既占地方又不好比新旧
const ago = (ms: number) => {
    const s = Math.max(0, (Date.now() - ms) / 1000);
    if (s < 60) return '刚刚';
    const units: [number, string][] = [[60, '分钟'], [24, '小时'], [30, '天'], [12, '个月']];
    let v = s / 60;
    for (const [step, name] of units) {
        if (v < step) return `${Math.floor(v)}${name}前`;
        v /= step;
    }
    return `${Math.floor(v)}年前`;
};

const fmtSize = (n: number) =>
    n < 1024 * 1024 ? `${(n / 1024).toFixed(0)}KB` : `${(n / 1024 / 1024).toFixed(1)}MB`;

defineExpose({ show });
</script>

<style lang="scss" scoped>
.vp-hist {
    max-height: 420px;
    overflow-y: auto;
}
.vp-hist__item {
    padding: 8px 10px;
    border-radius: 6px;
    border: 1px solid transparent;
    cursor: pointer;
}
.vp-hist__item:hover {
    background: var(--el-fill-color-light);
    border-color: var(--el-border-color-lighter);
}
.vp-hist__top {
    display: flex;
    align-items: baseline;
    gap: 8px;
}
.vp-hist__top b {
    font-size: 13px;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.vp-hist__top i {
    margin-left: auto;
    flex: none;
    font-style: normal;
    font-size: 11px;
    color: var(--el-text-color-secondary);
}
.vp-hist__cwd {
    display: block;
    margin-top: 2px;
    font: 10px/1.4 var(--el-font-family-mono, monospace);
    color: var(--el-text-color-secondary);
    word-break: break-all;
}
</style>
