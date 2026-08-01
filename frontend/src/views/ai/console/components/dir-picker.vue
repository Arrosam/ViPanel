<template>
    <el-dialog v-model="visible" :title="$t('aiTools.console.newSession')" width="620px">
        <!-- 面包屑：每段可点，和文件面板同一套交互 -->
        <div class="vp-dp__crumbs">
            <template v-for="(c, i) in crumbs" :key="c.path">
                <button
                    class="vp-dp__crumb"
                    :class="{ on: i === crumbs.length - 1 }"
                    :disabled="i === crumbs.length - 1"
                    @click="go(c.path)"
                >
                    {{ c.name }}
                </button>
                <span v-if="i < crumbs.length - 1" class="vp-dp__sep">/</span>
            </template>
        </div>

        <div class="vp-dp__list" v-loading="loading">
            <div v-if="parent" class="vp-dp__row" @click="go(parent)">
                <el-icon><Top /></el-icon>
                <span>..</span>
            </div>
            <div v-for="d in dirs" :key="d.path" class="vp-dp__row" @click="go(d.path)">
                <el-icon class="vp-dp__ic"><Folder /></el-icon>
                <span>{{ d.name }}</span>
            </div>
            <el-empty v-if="!loading && !dirs.length && !parent" :image-size="44" :description="$t('aiTools.console.noSubdir')" />
        </div>

        <template #footer>
            <span class="vp-dp__here">{{ here }}</span>
            <el-button @click="visible = false">{{ $t('aiTools.console.cancel') }}</el-button>
            <el-button type="primary" :disabled="!here" @click="create">
                {{ $t('aiTools.console.createHere') }}
            </el-button>
        </template>
    </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { Folder, Top } from '@element-plus/icons-vue';
import { getFilesList } from '@/api/modules/files';
import { createSession } from '@/api/modules/vipanel';
import { MsgError } from '@/utils/message';

const emit = defineEmits<{ (e: 'created', id: string): void }>();

const visible = ref(false);
const loading = ref(false);
const here = ref('');
const dirs = ref<any[]>([]);

const parent = computed(() => {
    if (!here.value || here.value === '/') return '';
    const i = here.value.lastIndexOf('/');
    return i <= 0 ? '/' : here.value.slice(0, i);
});

const crumbs = computed(() => {
    const segs = here.value.split('/').filter(Boolean);
    const out = [{ name: '/', path: '/' }];
    let acc = '';
    for (const s of segs) {
        acc += '/' + s;
        out.push({ name: s, path: acc });
    }
    return out;
});

const go = async (p: string) => {
    if (!p) return;
    loading.value = true;
    try {
        // dir: true 让后端只返回目录——建会话时文件是噪音
        const res = await getFilesList({ path: p, expand: true, dir: true, page: 1, pageSize: 500 } as any);
        here.value = res.data.path || p;
        dirs.value = (res.data.items || []).filter((i: any) => i.isDir);
    } catch (e: any) {
        MsgError(e?.message || String(e));
    } finally {
        loading.value = false;
    }
};

// from 是起始目录：从文件页打开就是当前浏览的目录，
// 从会话页打开就是当前会话的 cwd。少走几步就是这个参数的全部意义。
const open = (from?: string) => {
    visible.value = true;
    dirs.value = [];
    go(from || '/');
};

const create = async () => {
    try {
        const res = await createSession({ cwd: here.value });
        visible.value = false;
        emit('created', res.data.id);
    } catch (e: any) {
        MsgError(e?.message || String(e));
    }
};

defineExpose({ open });
</script>

<style lang="scss" scoped>
.vp-dp__crumbs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    padding-bottom: 6px;
    font: 11px/1.6 var(--el-font-family-mono, monospace);
    border-bottom: 1px solid var(--el-border-color-lighter);
}
.vp-dp__crumb {
    background: none;
    border: 0;
    padding: 1px 4px;
    border-radius: 3px;
    font: inherit;
    color: var(--el-text-color-secondary);
    cursor: pointer;
}
.vp-dp__crumb:hover:not(.on) {
    background: var(--el-fill-color-light);
    color: var(--el-text-color-primary);
}
.vp-dp__crumb.on {
    color: var(--el-text-color-primary);
    cursor: default;
}
.vp-dp__sep {
    opacity: 0.4;
}
.vp-dp__list {
    max-height: 340px;
    overflow-y: auto;
    padding-top: 4px;
}
.vp-dp__row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    border-radius: 5px;
    cursor: pointer;
    font: 12px/1.5 var(--el-font-family-mono, monospace);
}
.vp-dp__row:hover {
    background: var(--el-fill-color-light);
}
.vp-dp__ic {
    color: var(--el-color-primary);
}
.vp-dp__here {
    float: left;
    max-width: 55%;
    line-height: 32px;
    font: 11px/32px var(--el-font-family-mono, monospace);
    color: var(--el-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
</style>
