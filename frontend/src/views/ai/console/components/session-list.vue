<template>
    <div class="vp-rail">
        <div class="vp-rail__hd">
            <span class="vp-rail__title">{{ $t('aiTools.console.sessions') }}</span>
            <el-button link :icon="Plus" :title="$t('aiTools.console.newSession')" @click="emit('create')" />
        </div>

        <div class="vp-rail__list">
            <el-empty v-if="!groups.length" :image-size="52" :description="$t('aiTools.console.empty')" />

            <template v-for="g in groups" :key="g.cwd">
                <div class="vp-grp" :title="g.cwd">
                    <span class="vp-grp__nm">{{ g.dir }}</span>
                    <span class="vp-grp__n">{{ g.items.length }}</span>
                </div>
                <div
                    v-for="s in g.items"
                    :key="s.id"
                    class="vp-row"
                    :class="{ 'is-on': s.id === current }"
                    :title="statusText(s)"
                    @click="emit('select', s.id)"
                    @dblclick.prevent="emit('rename', s)"
                >
                    <span class="vp-dot" :class="`is-${s.status}`" />
                    <span class="vp-row__t">{{ s.title }}</span>
                    <el-dropdown trigger="click" @command="(cmd) => emit(cmd, s)">
                        <span class="vp-row__more" @click.stop>
                            <el-icon><MoreFilled /></el-icon>
                        </span>
                        <template #dropdown>
                            <el-dropdown-menu>
                                <el-dropdown-item command="rename">
                                    {{ $t('aiTools.console.rename') }}
                                </el-dropdown-item>
                                <el-dropdown-item command="restart">
                                    {{ $t('aiTools.console.restart') }}
                                </el-dropdown-item>
                                <el-dropdown-item command="remove" divided>
                                    {{ $t('aiTools.console.finish') }}
                                </el-dropdown-item>
                            </el-dropdown-menu>
                        </template>
                    </el-dropdown>
                </div>
            </template>
        </div>

        <div class="vp-rail__ft">
            {{ $t('aiTools.console.poolState', [pool.active, pool.size]) }}
        </div>
    </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { Plus, MoreFilled } from '@element-plus/icons-vue';
import { ViPanel } from '@/api/interface/vipanel';

const { t } = useI18n();

const props = defineProps<{
    sessions: ViPanel.Session[];
    current: string;
    pool: ViPanel.Pool;
}>();

const emit = defineEmits<{
    (e: 'select', id: string): void;
    (e: 'create'): void;
    (e: 'rename' | 'restart' | 'remove', s: ViPanel.Session): void;
}>();

// 按目录归类。同一个项目下往往开好几个会话，平铺一列很快就找不着。
const groups = computed(() => {
    const map = new Map<string, { cwd: string; dir: string; items: ViPanel.Session[] }>();
    for (const s of props.sessions) {
        if (!map.has(s.cwd)) map.set(s.cwd, { cwd: s.cwd, dir: s.dir, items: [] });
        map.get(s.cwd)!.items.push(s);
    }
    return [...map.values()];
});

const statusText = (s: ViPanel.Session) => {
    const label = t(`aiTools.console.status.${s.status}`);
    return s.notes ? `${label} · ${s.notes}` : label;
};
</script>

<style lang="scss" scoped>
.vp-rail {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-width: 0;
    border-right: 1px solid var(--el-border-color-light);
}

.vp-rail__hd {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 6px;
    padding: 6px 6px 6px 10px;
    border-bottom: 1px solid var(--el-border-color-lighter);
}
.vp-rail__title {
    font-size: 12px;
    font-weight: 600;
    color: var(--el-text-color-regular);
}

.vp-rail__list {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 4px;
}

.vp-rail__ft {
    flex: none;
    padding: 6px 10px;
    font-size: 11px;
    color: var(--el-text-color-secondary);
    border-top: 1px solid var(--el-border-color-lighter);
}

.vp-grp {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 6px 4px;
    font-size: 10px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--el-text-color-secondary);
}
.vp-grp__nm {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.vp-grp__n {
    margin-left: auto;
    flex: none;
}

/* 一行一个会话：状态灯 + 标题 + ... 。26px 的行里塞三个图标既点不准
   也把标题挤没了，所以操作全收进 ... 菜单。 */
.vp-row {
    display: flex;
    align-items: center;
    gap: 7px;
    height: 28px;
    padding: 0 6px;
    border-radius: 5px;
    cursor: pointer;
    min-width: 0;
}
.vp-row:hover {
    background: var(--el-fill-color-light);
}
.vp-row.is-on {
    background: var(--el-color-primary-light-9);
}
.vp-row__t {
    flex: 1;
    min-width: 0;
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.vp-row__more {
    flex: none;
    opacity: 0;
    color: var(--el-text-color-secondary);
    display: flex;
}
.vp-row:hover .vp-row__more {
    opacity: 1;
}

.vp-dot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--el-text-color-placeholder);
}
/* 在忙 = 琥珀呼吸，有未读 = 亮绿常亮，看过就熄灭。
   「在忙」是状态不是时间窗，所以 agent 想得久也不会灭。 */
.vp-dot.is-working {
    background: var(--el-color-warning);
    animation: vp-pulse 1.1s ease-in-out infinite;
}
.vp-dot.is-unread {
    background: var(--el-color-success);
    box-shadow: 0 0 5px var(--el-color-success-light-3);
}
.vp-dot.is-error {
    background: var(--el-color-danger);
}
/* 休眠不是错误：被实例池挤下去了，一操作就接回来 */
.vp-dot.is-sleeping {
    background: transparent;
    border: 1px solid var(--el-text-color-placeholder);
}

@keyframes vp-pulse {
    0%,
    100% {
        opacity: 1;
    }
    50% {
        opacity: 0.35;
    }
}
</style>
