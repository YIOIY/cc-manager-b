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
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--line);
}

:global(.page-head h1) {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: 0;
  line-height: 1.3;
  color: var(--ink);
}

@media (max-width: 620px) {
  .page-head {
    align-items: flex-start;
    gap: 12px;
    flex-direction: column;
  }
}

.eyebrow {
  display: none;
}

.muted {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--muted);
  line-height: 1.5;
}

.page-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-shrink: 0;
}

:global(.page-actions .el-button) {
  display: inline-flex;
  gap: 6px;
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
