import { createApp } from "vue";
import { createPinia } from "pinia";

import ArcoVue from "@arco-design/web-vue";

import "@arco-design/web-vue/dist/arco.css";

/**
 * 初始化 Wails Frontend Runtime。
 *
 * Wails v3 官方要求至少存在一次 side-effect import。
 * Runtime 初始化以后：
 *
 * - Events；
 * - Window；
 * - Dialog；
 * - CSS Drag Region；
 *
 * 等桌面能力才能按照 Wails 预期工作。
 */
import "@wailsio/runtime";

import App from "./App.vue";

import "./assets/main.css";

const app = createApp(App);

app.use(createPinia());

app.use(ArcoVue);

/**
 * 开发阶段捕获遗漏的 Vue 异常。
 *
 * 业务异常仍然应该在 Store / Component 中显式处理，
 * 这里仅作为最后一道开发诊断入口。
 */
app.config.errorHandler = (
    error,
    instance,
    info,
) => {
    console.error(
        "[Humbert Vue Error]",
        info,
        error,
    );
};

app.mount("#app");