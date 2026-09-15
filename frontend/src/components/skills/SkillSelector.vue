<script setup>
import {
  computed,
  onMounted,
} from "vue";

import {
  useSkillStore,
} from "../../stores/skills.js";

const props =
    defineProps({
      modelValue: {
        type: Array,
        default: () => [],
      },
    });

// 保留 v-model 契约，避免既有 Agent/Project 表单产生未知属性警告；本组件现在只读，不再在
// Agent 配置表单里维护第二套 Skill 选择 UI。
defineEmits([
  "update:modelValue",
]);

const skillStore =
    useSkillStore();

const selected =
    computed(() => (
        Array.isArray(props.modelValue)
            ? props.modelValue
            : []
    ));

const catalogByName =
    computed(() => {
      const result =
          new Map();
      for (const skill of skillStore.items) {
        const name =
            skill?.name || skill?.directoryName;
        if (name) {
          result.set(name, skill);
        }
      }
      return result;
    });

const selectedSummary =
    computed(() => selected.value.map(
        (name) => {
          const skill =
              catalogByName.value.get(name);
          return {
            name,
            label:
                skill?.alias?.trim() ||
                skill?.name ||
                name,
            valid:
                !skillStore.loaded || Boolean(skill?.valid),
            runtimeReady:
                !skillStore.loaded || skill?.runtimeStatus !== "unsupported",
            needsSetup:
                skillStore.loaded && skill?.runtimeStatus === "needs_setup",
            runtimeMessage:
                typeof skill?.runtimeMessage === "string"
                    ? skill.runtimeMessage
                    : "",
          };
        },
    ));

const compactSummary =
    computed(() => selectedSummary.value.slice(0, 6));

const overflowCount =
    computed(() => Math.max(
        0,
        selectedSummary.value.length - compactSummary.value.length,
    ));

onMounted(async () => {
  try {
    // Skills 设置页可以独立修改 Agent.enabled_skills 与 Alias。每次进入 Agent 配置时
    // 都重新读取一次 Catalog，避免复用旧的前端投影覆盖刚刚保存的状态。
    await skillStore.load();
  } catch {
    // Agent 保存仍由后端做最终校验；这里只负责展示摘要。
  }
});
</script>

<template>
  <div class="skill-selector-summary">
    <div
        v-if="selectedSummary.length > 0"
        class="skill-selector-summary__chips"
    >
      <a-tag
          v-for="item in compactSummary"
          :key="item.name"
          size="medium"
          :color="item.valid && item.runtimeReady && !item.needsSetup ? undefined : 'orange'"
          :title="item.label !== item.name ? item.name : undefined"
      >
        {{ item.label }}
      </a-tag>
      <a-tag
          v-if="overflowCount > 0"
          size="medium"
      >
        +{{ overflowCount }}
      </a-tag>
    </div>

    <div
        v-else
        class="skill-selector-summary__empty"
    >
      当前 Agent 尚未启用 Skill
    </div>

    <div class="skill-selector-summary__hint">
      <span>
        已启用 {{ selectedSummary.length }} 个。技能开关统一在左侧「技能」入口中按 Agent / 项目管理。
      </span>
      <span v-if="selectedSummary.some((item) => !item.valid)">
        当前配置包含失效 Skill，请到左侧「技能」入口清理。
      </span>
      <span v-else-if="selectedSummary.some((item) => !item.runtimeReady)">
        当前配置包含已安装但暂不兼容 Runtime 的 Skill，请到左侧「技能」入口关闭或检查兼容性。
      </span>
      <span v-else-if="selectedSummary.some((item) => item.needsSetup)">
        当前配置包含需要补充运行环境的 Skill；它仍可启用，但部分 scripts 可能无法执行。
      </span>
      <span v-if="skillStore.loadError">
        Skill Catalog 暂时不可读取：{{ skillStore.loadError }}
      </span>
    </div>
  </div>
</template>

<style scoped>
.skill-selector-summary {
  display: flex;
  width: 100%;
  min-width: 0;
  flex-direction: column;
  gap: 7px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-bg);
  padding: 10px 11px;
}

.skill-selector-summary__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.skill-selector-summary__empty {
  color: var(--h-text-muted);
  font-size: 12px;
}

.skill-selector-summary__hint {
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: var(--h-text-muted);
  font-size: 11px;
  line-height: 1.45;
}
</style>
