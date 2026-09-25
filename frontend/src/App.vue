<script setup>
import { computed } from "vue";
import zhCN from "@arco-design/web-vue/es/locale/lang/zh-cn.js";
import enUS from "@arco-design/web-vue/es/locale/lang/en-us.js";
import jaJP from "@arco-design/web-vue/es/locale/lang/ja-jp.js";
import koKR from "@arco-design/web-vue/es/locale/lang/ko-kr.js";
import { language } from "./i18n/index.js";
import AppShell
  from "./layouts/AppShell.vue";

const arcoLanguage = computed(() => ({
  "zh-CN": zhCN,
  "en-US": enUS,
  "ja-JP": jaJP,
  "ko-KR": koKR,
})[language.value] || zhCN);
</script>

<template>
  <a-config-provider :locale="arcoLanguage">
    <AppShell />
  </a-config-provider>
</template>
