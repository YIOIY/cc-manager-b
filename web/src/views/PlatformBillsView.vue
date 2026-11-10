<template>
  <PageHead eyebrow="PLATFORM BILLING" title="平台离线账单" :description="listConfigs.platformBills!.description" />
  <PlatformBills
    @configure="router.push({ name: 'connections' })"
    @manage-accounts="router.push({ name: 'accounts' })"
  />
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import PageHead from '../components/PageHead.vue'
import PlatformBills from '../components/PlatformBills.vue'
import { listConfigs } from '../list-model'

const router = useRouter()
</script>
