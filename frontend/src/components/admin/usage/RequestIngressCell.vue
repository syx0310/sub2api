<template>
  <div v-if="value" data-test="request-ingress" class="space-y-0.5 text-xs text-gray-600 dark:text-gray-400">
    <div class="max-w-64 truncate font-mono" :title="value.host">{{ value.host || '—' }}</div>
    <div>{{ value.source === 'forwarded_host' ? 'X-Forwarded-Host' : 'Host' }}</div>
    <div v-if="value.peer_ip" class="font-mono">{{ t('admin.usage.ingressPeer') }}: {{ value.peer_ip }}</div>
  </div>
  <span v-else data-test="request-ingress" class="text-gray-400">—</span>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { RequestIngress } from '@/types'
defineProps<{ value?: RequestIngress | null }>()
const { t } = useI18n()
</script>
