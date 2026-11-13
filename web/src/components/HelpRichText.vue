<template>
  <template v-for="(part, i) in parts" :key="i"
    ><span v-if="part.ui" class="ui-label">{{ part.text }}</span
    ><template v-else>{{ part.text }}</template></template
  >
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { richParts } from '../help/content'

// Help text names buttons and fields in 「…」; they are shown as small
// labels so a reader can match them to the screen at a glance.
const props = defineProps<{ text: string }>()
const parts = computed(() => richParts(props.text))
</script>

<style>
.ui-label {
  display: inline-block;
  margin: 0 2px;
  padding: 0 6px;
  border: 1px solid #cfdcdb;
  border-bottom-width: 2px;
  border-radius: 5px;
  background: #f6faf9;
  color: #1f3b47;
  font-size: 0.92em;
  font-weight: 600;
  line-height: 1.55;
  white-space: nowrap;
}
</style>
