<script setup>
import { computed } from 'vue';
import { useSkillStore } from '../../stores/skills.js';
import { splitSkillReferences } from '../../utils/skillCommand.js';
import SkillReference from './SkillReference.vue';

const props = defineProps({ content: { type: String, default: '' } });
const skills = useSkillStore();
const parts = computed(() => splitSkillReferences(props.content, skills.items));
</script>

<template>
  <div>
    <template v-for="part in parts" :key="part.start">
      <SkillReference v-if="part.type === 'skill'" :skill="part.skill"/>
      <template v-else>{{ part.text }}</template>
    </template>
  </div>
</template>
