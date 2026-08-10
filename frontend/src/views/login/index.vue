<!--
  ViPanel 登录页。

  和上游那版的区别只有一处**默认外观**：左栏原来是一张 1Panel 的宣传图、
  外加一张全屏背景图，现在改成纯 CSS 画的品牌面（brand-panel.vue）。
  那三张图是上游的品牌资源，见 VIPANEL.md。

  **主题设置里的自定义登录图/背景照旧生效**——那是个真功能，
  用户配了就用用户的，只有「没配」时才走我们自己的默认。
-->
<template>
    <div class="vp-login" :style="backgroundStyle">
        <div class="vp-login__card" :style="{ width: cardWidth, minHeight: cardMinHeight }">
            <div class="vp-login__grid" :style="gridStyle">
                <!-- 左栏：用户配了图就用图，否则用画的 -->
                <div v-if="showSide" class="vp-login__side">
                    <img
                        v-if="customLoginImage"
                        :src="customLoginImage"
                        class="vp-login__img"
                        alt=""
                        @error="customImageBroken = true"
                    />
                    <BrandPanel v-else />
                </div>
                <div class="vp-login__form">
                    <LoginForm ref="loginRef" />
                </div>
            </div>
        </div>
    </div>
</template>

<script setup lang="ts">
import LoginForm from './components/login-form.vue';
import BrandPanel from './components/brand-panel.vue';
import { ref, computed, onMounted, onUnmounted } from 'vue';
import { useGlobalStore } from '@/composables/useGlobalStore';
import { preloadImage } from '@/utils/browser';

defineOptions({ name: 'Login' });

const { entrance, themeConfig } = useGlobalStore();

const props = defineProps({
    code: { type: String, default: '' },
});

// -- 用户自定义的登录图 --------------------------------------------------------
//
// themeConfig.loginImage 有三种取值：
//   'loginImage'  —— 用户上传过，真正的图要去 /api/v2/images/loginImage 取
//   非空字符串     —— 直接就是一个 URL
//   空            —— 没配，走我们自己的品牌面
const uploadedLoginImage = ref<string | null>(null);
const customImageBroken = ref(false);

const customLoginImage = computed(() => {
    if (customImageBroken.value) return null; // 取不到就退回品牌面，别留一个碎图标
    const v = themeConfig.value?.loginImage;
    if (!v) return null;
    if (v === 'loginImage') return uploadedLoginImage.value;
    return v;
});

// -- 背景 ---------------------------------------------------------------------
//
// 没配时不再用图片，而是一层很淡的 CSS 渐变：省几百 KB，任何分辨率都不糊。
const uploadedBackground = ref<string | null>(null);
const backgroundStyle = ref<Record<string, string>>({});

const applyBackground = () => {
    const { loginBackground, loginBgType } = themeConfig.value || {};
    if (loginBgType === 'color' && loginBackground) {
        backgroundStyle.value = { background: loginBackground };
        return;
    }
    if (loginBgType === 'image') {
        const url = loginBackground === 'loginBackground' ? uploadedBackground.value : loginBackground;
        if (url) {
            backgroundStyle.value = { backgroundImage: `url(${url})` };
            return;
        }
    }
    backgroundStyle.value = {}; // 交给样式表里的默认渐变
};

onMounted(async () => {
    if (props.code) entrance.value = props.code;

    if (themeConfig.value?.loginImage === 'loginImage') {
        uploadedLoginImage.value = await preloadImage(`/api/v2/images/loginImage?t=${Date.now()}`);
    }
    if (themeConfig.value?.loginBgType === 'image' && themeConfig.value?.loginBackground === 'loginBackground') {
        uploadedBackground.value = await preloadImage(`/api/v2/images/loginBackground?t=${Date.now()}`);
    }
    applyBackground();
    window.addEventListener('resize', onResize);
});
onUnmounted(() => window.removeEventListener('resize', onResize));

// -- 尺寸 ---------------------------------------------------------------------
const winWidth = ref(window.innerWidth);
const onResize = () => (winWidth.value = window.innerWidth);

// 窄屏下砍掉左栏，只留表单——两栏挤在手机上谁都看不清。
//
// 阈值必须 ≥ 卡片宽 + 页面左右内边距（980 + 32 = 1012），否则在
// 「够宽到显示左栏、又不够宽到放下卡片」的那段区间里会横向溢出。
// 第一版写的 960 就落在这个坑里。宽度同时也写成 min()，双保险。
const SIDE_AT = 1024;
const showSide = computed(() => winWidth.value >= SIDE_AT);
const cardWidth = computed(() =>
    showSide.value ? 'min(980px, calc(100vw - 32px))' : 'min(420px, calc(100vw - 32px))',
);
// 不写死高度：内容超出时 overflow:hidden 会**静默裁掉**，
// 底部那行合规声明第一次就是这么消失的。只给下限，让内容撑开。
const cardMinHeight = computed(() => (showSide.value ? '460px' : '0'));
const gridStyle = computed(() => ({
    gridTemplateColumns: showSide.value ? '420px minmax(0, 1fr)' : 'minmax(0, 1fr)',
}));
</script>

<style lang="scss" scoped>
.vp-login {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: 16px;
    background-size: cover;
    background-position: center;
    // 默认背景：不用图片。深浅两套都给，跟着系统/主题走。
    background-color: #eef1f7;
    background-image: radial-gradient(90% 70% at 15% 0%, #dfe6fb 0%, transparent 60%),
        radial-gradient(80% 80% at 100% 100%, #e4e9f7 0%, transparent 55%);
}

html.dark .vp-login {
    background-color: #141414;
    background-image: radial-gradient(90% 70% at 15% 0%, #1c2340 0%, transparent 60%),
        radial-gradient(80% 80% at 100% 100%, #191c2e 0%, transparent 55%);
}

.vp-login__card {
    position: relative;
    z-index: 1;
    // flex 容器 + 子项 flex:1，这样网格才能撑满 min-height 撑出来的高度。
    // 直接给网格 height:100% 是不行的：卡片是 min-height，百分比高度没有可解析的基准，
    // 网格会退回内容高度，左边深色栏下面就留出一条白条。
    display: flex;
    overflow: hidden;
    border-radius: 12px;
    background: var(--el-bg-color, #fff);
    box-shadow: 0 12px 40px rgb(0 0 0 / 12%);
}

.vp-login__grid {
    flex: 1;
    display: grid;
    align-items: stretch;
    min-width: 0;
}

.vp-login__side {
    // flex 容器 + 子项 flex:1：既能在内容矮时把品牌面拉满整栏，
    // 又能在内容高时把整张卡片撑高。用 height:100% 只能做到前者，
    // 后者会被静默裁掉。
    display: flex;
    overflow: hidden;
}

.vp-login__side > * {
    flex: 1;
    min-width: 0;
}

.vp-login__img {
    width: 100%;
    height: 100%;
    object-fit: cover;
}

.vp-login__form {
    display: flex;
    align-items: center;
    justify-content: center;
    min-width: 0;
    padding: 28px;
}
</style>
