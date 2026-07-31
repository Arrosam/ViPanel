<template>
    <div>
        <LayoutContent>
            <template #main>
                <el-empty :image-size="72" :description="$t('aiTools.console.empty')" />
            </template>
        </LayoutContent>
    </div>
</template>

<script setup lang="ts">
defineOptions({ name: 'Console' });
</script>
