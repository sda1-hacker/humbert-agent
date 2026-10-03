<script setup>
import { computed, defineAsyncComponent, onErrorCaptured, ref, watch } from "vue";
import { IconSearch } from "@arco-design/web-vue/es/icon";
import AppPageHeader from "../ui/AppPageHeader.vue";
import { settingsFeatures, settingsGroups } from "../../features/settings.js";
import { t } from "../../i18n/index.js";

const props = defineProps({ initialKey: { type: String, default: "models" } });
const emit = defineEmits(["close", "open-skills", "open-session"]);

// 文案在 computed 中翻译，切换语言时无需重新注册页面或重新创建组件。
const navigationGroups = computed(() => settingsGroups.map((group) => ({
  ...group, title: t(group.title),
  items: group.items.map((item) => ({ ...item, title: t(item.title), description: t(item.description) })),
})));
const allItems = computed(() => navigationGroups.value.flatMap((group) => group.items));
function validKey(value) { return allItems.value.some((item) => item.key === value) ? value : "models"; }

const activeKey = ref(validKey(props.initialKey));
const query = ref("");
const childRenderError = ref("");
watch(() => props.initialKey, (value) => { activeKey.value = validKey(value); });
watch(activeKey, () => { childRenderError.value = ""; });

// 沿用技能/连接器的局部错误恢复，是否支持恢复由页面定义声明。
onErrorCaptured((error, _instance, info) => {
  if (!settingsFeatures.get(activeKey.value)?.recoverableError) return true;
  childRenderError.value = `${error?.message ?? String(error)}${info ? `（${info}）` : ""}`;
  console.error(`[Settings/${activeKey.value}] 渲染失败`, error, info);
  return false;
});

const filteredGroups = computed(() => {
  const keyword = query.value.trim().toLowerCase();
  if (!keyword) return navigationGroups.value;
  return navigationGroups.value.map((group) => ({
    ...group,
    items: group.items.filter((item) => [item.title, item.description, ...item.keywords].join(" ").toLowerCase().includes(keyword)),
  })).filter((group) => group.items.length > 0);
});
const activeItem = computed(() => allItems.value.find((item) => item.key === activeKey.value) ?? allItems.value[0]);

// 组件引用在装配时固定，切换页面只选择定义，不重新创建异步组件。
const components = new Map(settingsFeatures.items.map((item) => [item.key, defineAsyncComponent(item.load)]));
const activeComponent = computed(() => components.get(activeKey.value));
// 特殊页面事件在定义中映射，宿主只转发，不依赖技能或归档模块的内部行为。
const activeListeners = computed(() => Object.fromEntries(
  Object.entries(settingsFeatures.get(activeKey.value)?.forwardEvents || {}).map(([event, forwarded]) =>
    [event, (...args) => emit(forwarded, ...args)]),
));
function selectItem(key) { activeKey.value = validKey(key); }
</script>

<template>
  <section class="settings-view">
    <aside class="settings-sidebar">
      <div class="settings-sidebar__top">
        <button
            type="button"
            class="settings-back"
            aria-label="返回"
            title="返回"
            @click="emit('close')"
        >
          <svg class="settings-back__icon" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M15 5L8 12L15 19"/>
          </svg>
        </button>

        <div class="settings-sidebar__title">设置</div>
      </div>

      <div class="settings-search">
        <a-input v-model="query" allow-clear placeholder="搜索设置">
          <template #prefix>
            <IconSearch/>
          </template>
        </a-input>
      </div>

      <nav class="settings-nav" aria-label="设置导航">
        <template v-for="group in filteredGroups" :key="group.key">
          <div class="settings-nav-label">{{ group.title }}</div>

          <button
              v-for="item in group.items"
              :key="item.key"
              type="button"
              class="settings-nav-item"
              :class="{ 'settings-nav-item--active': activeKey === item.key }"
              @click="selectItem(item.key)"
          >
            <span class="settings-nav-item__glyph" aria-hidden="true">
              {{ item.glyph }}
            </span>
            <span class="settings-nav-item__text">{{ item.title }}</span>
          </button>
        </template>

        <div v-if="filteredGroups.length === 0" class="settings-nav-empty">
          没有匹配的设置
        </div>
      </nav>

    </aside>

    <main class="settings-content">
      <div class="settings-content__scroll">
        <div class="settings-content__inner">
          <AppPageHeader
              eyebrow="HUMBERT"
              :title="activeItem.title"
              :description="activeItem.description"
          />

          <section class="settings-content__body">
            <div v-if="activeItem.recoverableError" class="settings-child-host">
              <div v-if="childRenderError" class="settings-child-error">
                <strong>{{ $t(activeItem.recoverableError) }}</strong>
                <span>{{ childRenderError }}</span>
                <button type="button" @click="childRenderError = ''">重新显示</button>
              </div>
              <component :is="activeComponent" v-else v-on="activeListeners" />
            </div>
            <component :is="activeComponent" v-else v-on="activeListeners" />
          </section>
        </div>
      </div>
    </main>
  </section>
</template>
<style scoped>
.settings-view {
  display: grid;

  grid-template-columns:
    252px minmax(0, 1fr);

  background: var(--h-bg);
}

/* =============================
   Sidebar
   ============================= */

.settings-sidebar {
  display: flex;

  min-width: 0;
  min-height: 0;

  flex-direction: column;

  overflow: hidden;

  border-right: 1px solid var(--h-border);

  background: var(--h-sidebar);
}

.settings-sidebar__top {
  display: flex;

  height: 72px;

  flex: 0 0 72px;

  align-items: center;

  gap: 12px;

  padding: 0 18px;
}

.settings-back {
  display: grid;

  width: 32px;
  height: 32px;

  flex: 0 0 32px;

  place-items: center;

  padding: 0;

  border: 0;

  border-radius: 8px;

  background: transparent;

  color: var(--h-text-muted);

  cursor: pointer;

  font-size: 18px;

  transition: background-color 120ms ease,
  color 120ms ease;
}

.settings-back:hover {
  background: var(--h-surface-hover);

  color: var(--h-accent-hover);
}

.settings-back__icon {
  width: 18px;
  height: 18px;

  fill: none;

  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
}

.settings-sidebar__title {
  color: var(--h-text);

  font-size: 20px;

  font-weight: 500;

  letter-spacing: 0.01em;
}

.settings-search {
  padding: 0 14px 18px;
}

.settings-search :deep(.arco-input-wrapper) {
  height: 38px;

  padding: 0 12px;

  border-color: transparent !important;

  border-radius: 6px;

  background: var(--h-surface) !important;
}

.settings-search :deep(.arco-input-wrapper:hover),
.settings-search :deep(.arco-input-wrapper.arco-input-focus) {
  border-color: var(--h-border-strong) !important;

  background: var(--h-surface) !important;
}

.settings-search :deep(.arco-input-prefix) {
  color: var(--h-text-muted);
}

.settings-nav-label {
  flex: 0 0 auto;
  padding: 0 20px 8px;

  color: var(--h-text-muted);

  font-size: 12px;

  font-weight: 500;

  letter-spacing: 0.08em;

  text-transform: uppercase;
}

.settings-nav {
  display: flex;
  min-height: 0;
  flex: 1 1 auto;

  flex-direction: column;

  gap: 4px;

  padding: 0 10px 18px;
  overflow-y: auto;
}

.settings-nav .settings-nav-label {
  padding: 14px 10px 5px;
}

.settings-nav .settings-nav-label:first-child {
  padding-top: 0;
}

.settings-nav-item {
  display: flex;
  flex: 0 0 42px;

  width: 100%;
  height: 42px;

  align-items: center;

  gap: 11px;

  padding: 0 12px 0 14px;

  border: 0;
  border-left: 2px solid transparent;

  border-radius: 0 6px 6px 0;

  background: transparent;

  color: var(--h-text-secondary);

  cursor: pointer;

  text-align: left;

  transition: background-color 120ms ease,
  color 120ms ease;
}

.settings-nav-item:hover {
  background: var(--h-surface-hover);

  color: var(--h-text);
}

.settings-nav-item--active {
  border-left-color: var(--h-accent);

  background: transparent;

  color: var(--h-text);
}

.settings-nav-item__glyph {
  display: grid;

  width: 22px;
  height: 22px;

  flex: 0 0 22px;

  place-items: center;

  border: 0;

  color: var(--h-accent);

  font-family: var(--h-mono);

  font-size: 12px;

  font-weight: 500;
}

.settings-nav-item--active
.settings-nav-item__glyph {
  color: var(--h-accent);
}

.settings-nav-item__text {
  overflow: hidden;

  font-size: 13px;

  font-weight: 400;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.settings-nav-empty {
  padding: 18px 12px;

  color: var(--h-text-muted);

  font-size: 12px;

  text-align: center;
}

/* =============================
   Content
   ============================= */

.settings-content {
  min-width: 0;
  min-height: 0;

  overflow: hidden;

  background: var(--h-bg);
}

.settings-content__scroll {
  width: 100%;
  height: 100%;

  overflow-x: hidden;
  overflow-y: auto;
}

.settings-content__inner {
  width: min(var(--h-content-wide), 100%);
  margin: 0 auto;
  padding: var(--h-page-top) 48px var(--h-page-bottom);
}

.settings-content__body {
  min-width: 0;
}


.settings-child-host {
  min-width: 0;
}

.settings-child-error {
  display: flex;
  flex-direction: column;
  gap: 8px;
  border: 1px solid var(--h-danger-border);
  border-radius: 10px;
  background: var(--h-danger-soft);
  padding: 14px;
  color: var(--h-text-secondary);
  font-size: 12px;
}

.settings-child-error strong {
  color: var(--h-danger);
  font-size: 14px;
}

.settings-child-error button {
  align-self: flex-start;
  border: 1px solid var(--h-border);
  border-radius: 6px;
  background: var(--h-surface);
  padding: 5px 10px;
  color: var(--h-text);
  cursor: pointer;
}

@media (
max-width: 900px
) {
  .settings-view {
    grid-template-columns:
      224px minmax(0, 1fr);
  }

  .settings-content__inner {
    padding: var(--h-page-top) var(--h-page-gutter) var(--h-page-bottom);
  }
}
</style>
