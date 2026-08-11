<!--
  登录页左侧的品牌面。

  **全部用 CSS 和内联 SVG 画，不带任何图片文件。** 换掉的那三张
  （1panel-login.jpg / -bg.jpg / -enterprise.png）是上游的品牌资源，
  而飞致云的社区协议禁止未经书面许可使用其标识——一个改名的分发版
  顶着对方的宣传图，正是那条禁令指向的情形。

  不带图片还有两个附带好处：产物少几百 KB，且缩放到任何尺寸都不糊。

  底部那行「基于 1Panel · GPL-3.0」是**有意放在第一屏**的：
  GPLv3 §5(a) 要求改动版本显著标注它被改过，登录页是所有人都会看到的地方。
-->
<template>
    <div class="vp-brand">
        <!-- 背景：一个从右下角切进来的巨大 V，压得很淡，只做肌理不抢主体 -->
        <svg class="vp-brand__glyph" viewBox="0 0 200 200" aria-hidden="true">
            <path d="M20 10h34l46 116L146 10h34l-66 180H86L20 10Z" />
        </svg>

        <div class="vp-brand__body">
            <div class="vp-brand__mark">
                <svg viewBox="0 0 33 33" fill="currentColor" fill-rule="evenodd" clip-rule="evenodd">
                    <path
                        d="M9.4 2.5h14.2a6.9 6.9 0 0 1 6.9 6.9v14.2a6.9 6.9 0 0 1-6.9 6.9H9.4a6.9 6.9 0 0 1-6.9-6.9V9.4a6.9 6.9 0 0 1 6.9-6.9Z
                           M8.9 9.3h4.3l3.3 9.1 3.3-9.1h4.3l-5.4 14.4h-4.4L8.9 9.3Z"
                    />
                </svg>
                <span class="vp-brand__name">{{ panelName }}</span>
            </div>

            <p class="vp-brand__tagline">{{ $t('aiTools.console.brandTagline') }}</p>

            <ul class="vp-brand__points">
                <li>{{ $t('aiTools.console.brandPoint1') }}</li>
                <li>{{ $t('aiTools.console.brandPoint2') }}</li>
                <li>{{ $t('aiTools.console.brandPoint3') }}</li>
            </ul>
        </div>

        <div class="vp-brand__foot">{{ $t('aiTools.console.brandFork') }}</div>
    </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useGlobalStore } from '@/composables/useGlobalStore';

const { themeConfig } = useGlobalStore();
// 面板名是运行时设置，用户可以改。这里跟着走，不写死。
const panelName = computed(() => themeConfig.value?.panelName || 'ViPanel');
</script>

<style lang="scss" scoped>
.vp-brand {
    position: relative;
    display: flex;
    flex-direction: column;
    justify-content: space-between; // 主体在上、声明在下
    // **不要写 height: 100%。** 它会把这一栏钉死成单元格高度，于是自身的自然高度
    // 不再参与撑开父级——内容需要 532px 时卡片仍停在 460px，多出来的部分被
    // overflow:hidden 静默裁掉（底部那行合规声明就是这么消失了两次）。
    // 网格的 align-items:stretch 本来就会把它拉满，这行纯属有害。
    padding: 36px 32px;
    overflow: hidden;
    color: #fff;
    // 固定用深色，不跟随主题：品牌面在浅色和深色下都该是同一个样子，
    // 否则一个产品的登录页会有两副面孔。
    background: radial-gradient(120% 120% at 0% 0%, #3b5bfd 0%, #1e2a78 55%, #131a3f 100%);
}

.vp-brand__glyph {
    position: absolute;
    right: -18%;
    bottom: -22%;
    width: 78%;
    fill: #fff;
    opacity: 0.07;
    pointer-events: none;
}

.vp-brand__body {
    position: relative;
    z-index: 1;
}

.vp-brand__mark {
    display: flex;
    align-items: center;
    gap: 12px;
}

.vp-brand__mark svg {
    width: 40px;
    height: 40px;
    flex: none;
}

.vp-brand__name {
    font-size: 30px;
    font-weight: 650;
    letter-spacing: -0.4px;
}

.vp-brand__tagline {
    margin: 18px 0 0;
    font-size: 15px;
    line-height: 1.65;
    opacity: 0.9;
}

.vp-brand__points {
    margin: 22px 0 0;
    padding: 0;
    list-style: none;
    font-size: 13px;
    line-height: 2.1;
    opacity: 0.78;
}

// 圆点必须是**弹性项**，不能是内联元素。
// 内联的话换行后第二行会顶到最左边，跟圆点对齐而不是跟文字对齐——
// 中文要点一旦超过一行就看得出来（"不是一段 / JSON" 那条）。
.vp-brand__points li {
    display: flex;
    align-items: baseline;
    gap: 10px;
}

.vp-brand__points li::before {
    content: '';
    flex: none;
    width: 5px;
    height: 5px;
    // 空的弹性项以下外边距缘作基线，所以 margin-bottom 就是「抬高多少」，
    // 等价于原来那个 vertical-align: 2px。
    margin-bottom: 2px;
    border-radius: 50%;
    background: currentColor;
    opacity: 0.7;
}

.vp-brand__foot {
    position: relative;
    z-index: 1;
    padding-top: 24px;
    font-size: 12px;
    opacity: 0.55;
}
</style>
