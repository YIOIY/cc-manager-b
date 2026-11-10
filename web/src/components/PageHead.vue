<template>
  <div class="page-head">
    <div>
      <div class="eyebrow">{{ eyebrow }}</div>
      <h1>{{ title }}</h1>
      <p class="muted">{{ description }}</p>
    </div>
    <div class="page-actions"><slot /></div>
  </div>
</template>

<script setup lang="ts">
defineProps<{ eyebrow: string; title: string; description: string }>()
</script>

<style scoped>
.page-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  margin-bottom: 22px;
}

:global(.page-head h1) {
  margin: 0 0 5px;
  font-size: 29px;
  letter-spacing: -0.04em;
}

@media (max-width: 620px) {
  .page-head {
    align-items: flex-start;
    gap: 14px;
    flex-direction: column;
  }
}

.eyebrow {
  font:
    700 10px/1 'DM Mono',
    monospace;
  letter-spacing: 0.12em;
  color: #168e76;
  margin-bottom: 9px;
}

.page-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

:global(.page-actions .el-button) {
  display: inline-flex;
  gap: 7px;
  align-items: center;
}

@media (max-width: 620px) {
  .page-actions {
    width: 100%;
  }
  :global(.page-actions .el-button) {
    flex: 1;
  }
}
</style>
