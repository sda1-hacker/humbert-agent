import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";
import { localizeVueTemplates } from "./scripts/localize-vue.mjs";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [
    localizeVueTemplates(),
    vue(),
    tailwindcss(),
    // 当前服务使用 Call.ByName 和动态事件，不需要生成的 typed-event bindings。
  ],
});
