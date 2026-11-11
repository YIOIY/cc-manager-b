<template>
  <PageHead eyebrow="PLATFORM BILLING" title="平台离线账单" :description="listConfigs.platformBills!.description" />
  <el-tabs v-model="tab" class="bills-tabs">
    <el-tab-pane label="离线账单" name="daily">
      <PlatformBills
        @configure="router.push({ name: 'connections' })"
        @manage-accounts="router.push({ name: 'accounts' })"
      />
    </el-tab-pane>
    <el-tab-pane label="按模型消费" name="models" lazy>
      <BillModelsPanel />
    </el-tab-pane>
  </el-tabs>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import PageHead from '../components/PageHead.vue'
import { ref } from 'vue'
import PlatformBills from '../components/PlatformBills.vue'
import BillModelsPanel from '../components/BillModelsPanel.vue'
import { listConfigs } from '../list-model'

const router = useRouter()
const tab = ref<'daily' | 'models'>('daily')
</script>
