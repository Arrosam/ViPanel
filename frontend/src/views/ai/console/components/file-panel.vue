<template>
    <div class="vp-files">
        <div class="vp-files__bar">
            <el-button link :icon="Top" :title="$t('aiTools.console.fileUp')" :disabled="!parent" @click="go(parent)" />
            <el-button link :icon="HomeFilled" :title="$t('aiTools.console.fileHome')" @click="go(props.cwd)" />
            <el-button link :icon="FolderAdd" :title="$t('aiTools.console.fileMkdir')" :disabled="!inScope" @click="mkdir" />
            <el-button link :icon="Upload" :title="$t('aiTools.console.fileUpload')" :disabled="!inScope" @click="pick" />
            <input ref="fileInput" type="file" multiple hidden @change="upload" />
        </div>

        <!-- 面包屑：每一段都可点。只有「上级目录」的话，从深处退回去要点很多次 -->
        <div class="vp-files__crumbs">
            <template v-for="(c, i) in crumbs" :key="c.path">
                <button class="vp-crumb" :class="{ on: i === crumbs.length - 1 }" :disabled="i === crumbs.length - 1" @click="go(c.path)">
                    {{ c.name }}
                </button>
                <span v-if="i < crumbs.length - 1" class="vp-crumb__sep">/</span>
            </template>
        </div>

        <div v-if="uploading" class="vp-files__prog">
            <el-progress :percentage="progress" :stroke-width="3" />
        </div>

        <div class="vp-files__list" v-loading="loading">
            <div
                v-for="f in entries"
                :key="f.path"
                class="vp-file"
                :class="{ isdir: f.isDir, drop: dropTarget === f.path }"
                :draggable="true"
                :title="f.path"
                @click="f.isDir ? go(f.path) : emit('insert', f.path)"
                @dragstart="onDragStart($event, f)"
                @dragover="f.isDir && onDragOver($event, f)"
                @dragleave="dropTarget = ''"
                @drop="f.isDir && onDrop($event, f)"
            >
                <el-icon class="vp-file__ic"><Folder v-if="f.isDir" /><Document v-else /></el-icon>
                <span class="vp-file__nm">{{ f.name }}</span>
                <span v-if="!f.isDir" class="vp-file__sz">{{ fmtSize(f.size) }}</span>
            </div>
            <el-empty v-if="!loading && !entries.length" :image-size="48" :description="$t('aiTools.console.fileEmpty')" />
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { ElMessageBox } from 'element-plus';
import { Top, HomeFilled, FolderAdd, Upload, Folder, Document } from '@element-plus/icons-vue';
import { getFilesList, createFile, moveFile } from '@/api/modules/files';
import { MsgError } from '@/utils/message';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();
const props = defineProps<{ cwd: string }>();
const emit = defineEmits<{ (e: 'insert', path: string): void; (e: 'attach', path: string): void }>();

const here = ref('');
const entries = ref<any[]>([]);
const loading = ref(false);
const dropTarget = ref('');
const fileInput = ref<HTMLInputElement | null>(null);
const uploading = ref(false);
const progress = ref(0);

const parent = computed(() => {
    if (!here.value || here.value === '/') return '';
    const i = here.value.lastIndexOf('/');
    return i <= 0 ? '/' : here.value.slice(0, i);
});

// 只在会话目录之内允许改动。越界时按钮**禁用**，
// 而不是让用户点了才吃一个错误提示。
const inScope = computed(() => !!props.cwd && (here.value === props.cwd || here.value.startsWith(props.cwd + '/')));

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
        const res = await getFilesList({ path: p, expand: true, page: 1, pageSize: 500, showHidden: false } as any);
        here.value = res.data.path || p;
        entries.value = (res.data.items || []).slice().sort((a: any, b: any) => {
            if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
            return a.name.localeCompare(b.name);
        });
    } catch (e: any) {
        MsgError(e?.message || String(e));
    } finally {
        loading.value = false;
    }
};

watch(() => props.cwd, (v) => v && go(v), { immediate: true });

const fmtSize = (n: number) => {
    if (n < 1024) return `${n}B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)}K`;
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)}M`;
    return `${(n / 1024 / 1024 / 1024).toFixed(1)}G`;
};

const onDragStart = (ev: DragEvent, f: any) => {
    ev.dataTransfer?.setData('application/x-vp-path', f.path);
    ev.dataTransfer?.setData('text/plain', f.path);
};

const onDragOver = (ev: DragEvent, _f: any) => {
    if (!ev.dataTransfer?.types.includes('application/x-vp-path')) return;
    ev.preventDefault();
    dropTarget.value = _f.path;
};

const onDrop = async (ev: DragEvent, f: any) => {
    ev.preventDefault();
    ev.stopPropagation();
    dropTarget.value = '';
    const from = ev.dataTransfer?.getData('application/x-vp-path');
    if (!from || from === f.path) return;
    try {
        await moveFile({ type: 'cut', oldPaths: [from], newPath: f.path } as any);
        await go(here.value);
    } catch (e: any) {
        MsgError(e?.message || String(e));
    }
};

const mkdir = async () => {
    try {
        const { value } = await ElMessageBox.prompt('', t('aiTools.console.fileMkdir'));
        const name = (value || '').trim();
        if (!name) return;
        await createFile({ path: `${here.value}/${name}`, isDir: true, mode: 493 } as any);
        await go(here.value);
    } catch {
        /* 取消 */
    }
};

const pick = () => fileInput.value?.click();

const upload = async (e: Event) => {
    const input = e.target as HTMLInputElement;
    const files = Array.from(input.files || []);
    if (!files.length) return;
    uploading.value = true;
    progress.value = 0;
    try {
        for (let i = 0; i < files.length; i++) {
            const fd = new FormData();
            fd.append('file', files[i]);
            fd.append('path', here.value);
            await new Promise<void>((resolve, reject) => {
                const xhr = new XMLHttpRequest();
                xhr.open('POST', '/api/v2/files/upload');
                const csrf = document.cookie.split('; ').find((c) => c.startsWith('pcsrftoken='))?.split('=')[1];
                if (csrf) xhr.setRequestHeader('X-CSRF-Token', csrf);
                // 进度必须来自 xhr.upload：fetch 没有上传进度事件，
                // 大文件传的时候界面会一动不动看着像卡死
                xhr.upload.onprogress = (ev) => {
                    if (ev.lengthComputable) {
                        progress.value = Math.round(((i + ev.loaded / ev.total) / files.length) * 100);
                    }
                };
                xhr.onload = () => (xhr.status < 400 ? resolve() : reject(new Error(xhr.responseText)));
                xhr.onerror = () => reject(new Error('upload failed'));
                xhr.send(fd);
            });
        }
        await go(here.value);
    } catch (err: any) {
        MsgError(err?.message || String(err));
    } finally {
        uploading.value = false;
        input.value = '';
    }
};
</script>

<style lang="scss" scoped>
.vp-files {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
}
.vp-files__bar {
    flex: none;
    display: flex;
    gap: 2px;
    padding: 4px 8px;
    border-bottom: 1px solid var(--el-border-color-lighter);
}
.vp-files__crumbs {
    flex: none;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    padding: 4px 8px;
    font: 10px/1.5 var(--el-font-family-mono, monospace);
    border-bottom: 1px solid var(--el-border-color-lighter);
}
.vp-crumb {
    background: none;
    border: 0;
    padding: 1px 3px;
    border-radius: 3px;
    font: inherit;
    color: var(--el-text-color-secondary);
    cursor: pointer;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.vp-crumb:hover:not(.on) {
    background: var(--el-fill-color-light);
    color: var(--el-text-color-primary);
}
.vp-crumb.on {
    color: var(--el-text-color-primary);
    cursor: default;
}
.vp-crumb__sep {
    opacity: 0.4;
}
.vp-files__prog {
    flex: none;
    padding: 2px 8px;
}
.vp-files__list {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 4px;
}
.vp-file {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 4px 6px;
    border-radius: 4px;
    cursor: pointer;
    font: 11px/1.5 var(--el-font-family-mono, monospace);
    min-width: 0;
}
.vp-file:hover {
    background: var(--el-fill-color-light);
}
.vp-file.drop {
    background: var(--el-color-primary-light-9);
    outline: 1px dashed var(--el-color-primary);
}
.vp-file__ic {
    flex: none;
    color: var(--el-text-color-secondary);
}
.vp-file.isdir .vp-file__ic {
    color: var(--el-color-primary);
}
.vp-file__nm {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.vp-file__sz {
    flex: none;
    color: var(--el-text-color-secondary);
}
</style>
