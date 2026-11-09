<template>
  <div class="card" data-testid="request-ingress-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.requestIngress.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('admin.settings.requestIngress.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <div class="flex items-center justify-between gap-4">
        <label class="font-medium">{{ t('admin.settings.requestIngress.validate') }}</label>
        <Toggle :model-value="validate" @update:model-value="emit('update:validate', $event)" />
      </div>
      <p class="text-sm text-gray-500">{{ t('admin.settings.requestIngress.hint') }}</p>
      <label for="request-ingress-proxies" class="block font-medium">{{ t('admin.settings.requestIngress.proxies') }}</label>
      <textarea id="request-ingress-proxies" :value="draft" class="input w-full font-mono" rows="5"
        placeholder="192.0.2.10&#10;10.20.0.0/24&#10;2001:db8::/64" @input="onInput" />
      <p class="text-xs text-gray-500">{{ t('admin.settings.requestIngress.proxiesHint') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'

const props = defineProps<{ validate: boolean; proxies: string[] }>()
const emit = defineEmits<{ 'update:validate': [value: boolean]; 'update:proxies': [value: string[]] }>()
const { t } = useI18n()
const parse = (value: string) => [...new Set(value.split(/[\s,]+/).filter(Boolean))]
const draft = ref((props.proxies ?? []).join('\n'))
watch(() => props.proxies, (value) => {
  if (JSON.stringify(parse(draft.value)) !== JSON.stringify(value ?? [])) draft.value = (value ?? []).join('\n')
})
function onInput(event: Event) {
  draft.value = (event.target as HTMLTextAreaElement).value
  emit('update:proxies', parse(draft.value))
}
</script>
