<template>
  <div class="border-t border-gray-200 pt-4 mt-4 dark:border-dark-400" data-testid="group-model-mapping">
    <div class="flex items-center justify-between gap-3">
      <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.groups.modelMapping.title') }}</h4>
      <button type="button" class="text-sm text-primary-600" data-testid="add-model-mapping" @click="rows.push({ source: '', target: '' })">
        {{ t('common.add') }}
      </button>
    </div>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.modelMapping.hint') }}</p>
    <div v-for="row in rows" :key="rowKey(row)" class="mt-3 flex items-center gap-2">
      <input v-model="row.source" class="input min-w-0 flex-1" required :aria-label="t('admin.channels.form.mappingSource')" :placeholder="t('admin.channels.form.mappingSource')" data-testid="mapping-source" />
      <span class="text-gray-400">→</span>
      <input v-model="row.target" class="input min-w-0 flex-1" required :aria-label="t('admin.channels.form.mappingTarget')" :placeholder="t('admin.channels.form.mappingTarget')" data-testid="mapping-target" />
      <button type="button" class="text-sm text-red-500" data-testid="remove-model-mapping" @click="rows.splice(rows.indexOf(row), 1)">{{ t('common.delete') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'
import type { GroupModelMappingRow } from '@/views/admin/groupsModelMapping'

const rows = defineModel<GroupModelMappingRow[]>({ required: true })
const { t } = useI18n()
const rowKey = createStableObjectKeyResolver<GroupModelMappingRow>('group-model-mapping')
</script>
