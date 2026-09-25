<script setup>
import {
  onMounted,
  ref,
  watch,
} from "vue";

import { Message } from "../../utils/uiMessage.js";

import {
  getSkillState,
} from "../../api/skills.js";

import SkillSettings
  from "../settings/SkillSettings.vue";

import AppPageHeader
  from "../ui/AppPageHeader.vue";

import SkillDetailView
  from "./SkillDetailView.vue";

const props =
    defineProps({
      initialSkillName: {
        type: String,
        default: "",
      },
    });

const emit = defineEmits([
  "manage-packages",
]);

const selectedSkill =
    ref(null);

const selectedAgentID =
    ref("");

const catalogRevision =
    ref(0);

const loadingDetail =
    ref(false);

function normalizeSkills(state) {
  return Array.isArray(state?.skills)
      ? state.skills.map(
          (skill) => ({
            ...skill,

            alias:
                typeof skill?.alias ===
                "string"
                    ? skill.alias
                    : "",

            usedByAgents:
                Array.isArray(
                    skill?.usedByAgents,
                )
                    ? skill.usedByAgents
                    : [],

            source:
                skill?.source &&
                typeof skill.source === "object"
                    ? skill.source
                    : {known: false},
          }),
      )
      : [];
}

async function openDetail(name) {
  if (!name || loadingDetail.value) {
    return;
  }

  loadingDetail.value = true;

  try {
    const state =
        await getSkillState();

    const skill =
        normalizeSkills(state)
            .find(
                (item) => (
                    item?.name === name ||
                    item?.directoryName === name
                ),
            );

    if (!skill) {
      Message.warning(
          `没有找到 Skill「${name}」`,
      );

      selectedSkill.value = null;

      return;
    }

    selectedSkill.value = skill;
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    loadingDetail.value = false;
  }
}

function closeDetail() {
  selectedSkill.value = null;
}

async function refreshSelectedSkill() {
  const name =
      selectedSkill.value?.name ||
      selectedSkill.value
          ?.directoryName;

  if (!name) {
    return;
  }

  await openDetail(name);

  catalogRevision.value += 1;
}

function handleDeleted() {
  selectedSkill.value = null;
  catalogRevision.value += 1;
}

function rememberAgent(agentID) {
  selectedAgentID.value =
      agentID || "";
}

watch(
    () => props.initialSkillName,
    (name) => {
      if (name) {
        void openDetail(name);
      } else {
        selectedSkill.value = null;
      }
    },
);

onMounted(() => {
  if (props.initialSkillName) {
    void openDetail(
        props.initialSkillName,
    );
  }
});
</script>

<template>
  <section class="skill-workspace">
    <div class="skill-workspace__scroll">
      <div class="skill-workspace__inner">
        <AppPageHeader
            v-if="!selectedSkill"
            eyebrow="Agent Skills"
            title="技能"
            description="选择 Agent，开启需要的技能。点击技能可查看详情。"
        >
          <template #aside>
            <div class="skill-workspace__legend">
              <span class="skill-workspace__legend-dot"></span>
              <span>开关从下一次对话开始生效</span>
            </div>
          </template>
        </AppPageHeader>

        <SkillDetailView
            v-if="selectedSkill"
            :skill="selectedSkill"
            @back="closeDetail"
            @deleted="handleDeleted"
            @updated="refreshSelectedSkill"
        />

        <SkillSettings
            v-else
            :key="catalogRevision"
            :initial-agent-id="selectedAgentID"
            @agent-change="rememberAgent"
            @manage-packages="emit('manage-packages')"
            @open-detail="openDetail"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.skill-workspace {
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--h-bg);
}

.skill-workspace__scroll {
  width: 100%;
  height: 100%;
  overflow: auto;
}

.skill-workspace__inner {
  width: min(var(--h-content-wide), calc(100% - (var(--h-page-gutter) * 2)));
  min-width: 0;
  margin: 0 auto;
  padding: var(--h-page-top) 0 var(--h-page-bottom);
}

.skill-workspace__legend {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 7px;
  padding-bottom: 3px;
  color: var(--h-text-muted);
  font-size: 11px;
}

.skill-workspace__legend-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--h-success);
}

@media (max-width: 920px) {
  .skill-workspace__inner {
    width: calc(100% - (var(--h-page-gutter) * 2));
  }
}
</style>
