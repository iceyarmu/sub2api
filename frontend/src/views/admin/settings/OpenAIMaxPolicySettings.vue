<template>
  <div class="card" data-testid="openai-max-policy">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.openaiMaxPolicy.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.openaiMaxPolicy.description') }}
      </p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-else-if="!loaded" class="space-y-3">
        <p role="alert" class="text-sm text-red-600">{{ t('admin.settings.openaiMaxPolicy.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary btn-sm" @click="load">
          {{ t('admin.settings.openaiMaxPolicy.retry') }}
        </button>
      </div>
      <template v-else>
        <fieldset :disabled="saving" class="space-y-4">
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <label class="block text-sm text-gray-600 dark:text-gray-400">
              {{ t('admin.settings.betaPolicy.action') }}
              <select v-model="form.action" data-testid="max-action" class="input mt-1 w-full">
                <option value="pass">{{ t('admin.settings.betaPolicy.actionPass') }}</option>
                <option value="block">{{ t('admin.settings.betaPolicy.actionBlock') }}</option>
                <option value="downgrade">{{ t('admin.settings.openaiMaxPolicy.actionDowngrade') }}</option>
              </select>
            </label>
            <label class="block text-sm text-gray-600 dark:text-gray-400">
              {{ t('admin.settings.betaPolicy.scope') }}
              <select v-model="form.scope" data-testid="max-scope" class="input mt-1 w-full">
                <option value="all">{{ t('admin.settings.betaPolicy.scopeAll') }}</option>
                <option value="oauth">{{ t('admin.settings.betaPolicy.scopeOAuth') }}</option>
                <option value="apikey">{{ t('admin.settings.betaPolicy.scopeAPIKey') }}</option>
              </select>
            </label>
          </div>
          <div v-if="form.action === 'block'">
            <label class="block text-sm text-gray-600 dark:text-gray-400">
              {{ t('admin.settings.betaPolicy.errorMessage') }}
              <input
                v-model="form.error_message"
                data-testid="max-error-message"
                type="text"
                class="input mt-1 w-full"
                :placeholder="t('admin.settings.betaPolicy.errorMessagePlaceholder')"
              />
            </label>
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.settings.betaPolicy.errorMessageHint') }}</p>
          </div>
          <div v-if="form.action === 'downgrade'">
            <label class="block text-sm text-gray-600 dark:text-gray-400">
              {{ t('admin.settings.openaiMaxPolicy.downgradeEffort') }}
              <select v-model="form.downgrade_effort" data-testid="max-downgrade-effort" class="input mt-1 w-full">
                <option v-for="effort in ['xhigh', 'high', 'medium', 'low']" :key="effort" :value="effort">{{ effort }}</option>
              </select>
            </label>
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.settings.openaiMaxPolicy.downgradeHint') }}</p>
          </div>
        </fieldset>
        <div class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700">
          <button type="button" data-testid="max-save" :disabled="saving" class="btn btn-primary btn-sm" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getOpenAIMaxPolicySettings,
  updateOpenAIMaxPolicySettings,
  type OpenAIMaxPolicySettings,
} from '@/api/admin/openaiMaxPolicy'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const form = reactive<OpenAIMaxPolicySettings>({
  action: 'pass', scope: 'all', error_message: '', downgrade_effort: 'high',
})

function applySettings(settings: OpenAIMaxPolicySettings) {
  Object.assign(form, settings, { downgrade_effort: settings.downgrade_effort || 'high' })
}

async function load() {
  loading.value = true
  loaded.value = false
  try {
    applySettings(await getOpenAIMaxPolicySettings())
    loaded.value = true
  } catch {
    // Keep saving disabled until the persisted policy is successfully loaded.
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!loaded.value || saving.value) return
  saving.value = true
  try {
    applySettings(await updateOpenAIMaxPolicySettings({ ...form }))
    appStore.showSuccess(t('admin.settings.openaiMaxPolicy.saved'))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.openaiMaxPolicy.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
