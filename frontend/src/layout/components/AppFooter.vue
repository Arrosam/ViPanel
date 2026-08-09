<template>
    <div class="footer" :style="{ height: isMobile ? '108px' : '48px' }">
        <div class="flex w-full flex-col gap-4 md:justify-between md:flex-row">
            <div class="flex flex-wrap gap-4">
                <!-- 版权行**必须保留**：代码确实是上游的版权，GPLv3 §4/§5(a)
                     要求保留全部版权与许可声明。删掉的只是指向对方官网的超链接
                     （那是引流，不是版权声明的必要组成）。 -->
                <span>Copyright © 2014-{{ year }} {{ $t('commons.fit2cloud') }}</span>
                <span class="opacity-60">·  ViPanel（基于 1Panel 的改动版本，GPL-3.0）</span>
            </div>
            <div class="flex flex-row gap-2 md:flex-col lg:flex-row">
                <SystemUpgrade :footer="true" />
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import SystemUpgrade from '@/components/system-upgrade/index.vue';
import { useGlobalStore } from '@/composables/useGlobalStore';

const { isFxplay, isIntl, isMobile } = useGlobalStore();

const year = new Date().getFullYear();
</script>

<style scoped lang="scss">
.footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 48px;
    background: var(--panel-footer-bg);
    border-top: 1px solid var(--panel-footer-border);
    box-sizing: border-box;
    padding: 10px 20px;
    a {
        font-size: 12px;
        color: #858585;
        text-decoration: none;
        letter-spacing: 0.5px;
    }
    span {
        font-size: 12px;
        color: #858585;
        text-decoration: none;
        letter-spacing: 0.5px;
    }
}
</style>
