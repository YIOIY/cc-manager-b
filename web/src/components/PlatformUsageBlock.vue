<template>
  <div class="platform-usage-block">
    <div class="platform-usage-head">
      <span>账号用量（定时同步）</span>
      <small v-if="usage" :class="{ stale }">
        观察于 {{ formatResetAt(usage.observed_at) }}<template v-if="stale"> · 数据可能过期</template>
      </small>
    </div>
    <div v-if="usage" class="platform-usage-grid">
      <div>
        <small>5 小时窗口已用</small>
        <b :class="'tone-' + usageTone(usage.five_hour_used_percent)">{{
          formatPercent(usage.five_hour_used_percent)
        }}</b>
      </div>
      <div>
        <small>5 小时窗口重置时间</small><b>{{ formatResetAt(usage.five_hour_reset_at) }}</b>
      </div>
      <div>
        <small>7 天窗口已用</small>
        <b :class="'tone-' + usageTone(usage.week_used_percent)">{{ formatPercent(usage.week_used_percent) }}</b>
      </div>
      <div>
        <small>7 天窗口重置时间</small><b>{{ formatResetAt(usage.week_reset_at) }}</b>
      </div>
      <div>
        <small>累计输入 Token</small><b class="blue-number">{{ formatTokens(usage.input_tokens) }}</b>
      </div>
      <div>
        <small>累计输出 Token</small><b class="date-number">{{ formatTokens(usage.output_tokens) }}</b>
      </div>
      <div class="span-2">
        <small>累计花费</small><b class="purple-number">¥{{ usage.cost_yuan }}</b>
      </div>
    </div>
    <p v-else class="platform-usage-empty">平台未返回用量</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { PlatformUsage } from '../types/api'
import { formatPercent, formatResetAt, formatTokens, usageStale, usageTone } from '../model/platform-card'

const props = defineProps<{ usage: PlatformUsage | null; observedAt?: string }>()
const stale = computed(() => usageStale(props.usage?.observed_at || props.observedAt))
</script>

<style scoped>
.platform-usage-block {
  margin-top: 12px;
  padding: 12px 14px;
  border-radius: 12px;
  background: var(--surface-muted, #f6f7fb);
  border: 1px solid var(--line, #e8eaf0);
}
.platform-usage-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 10px;
  font-size: 12px;
  color: var(--text-secondary, #606266);
}
.platform-usage-head small {
  font-size: 11px;
  color: var(--text-tertiary, #909399);
}
.platform-usage-head small.stale {
  color: var(--el-color-warning);
}
.platform-usage-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 16px;
}
.platform-usage-grid > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.platform-usage-grid small {
  font-size: 12px;
  color: var(--text-secondary, #606266);
}
.platform-usage-grid b {
  font-size: 14px;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.span-2 {
  grid-column: 1 / -1;
}
.tone-warn {
  color: var(--el-color-warning);
}
.tone-danger {
  color: var(--el-color-danger);
}
.platform-usage-empty {
  margin: 0;
  font-size: 13px;
  color: var(--text-tertiary, #909399);
}
</style>
