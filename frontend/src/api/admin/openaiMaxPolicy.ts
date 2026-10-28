import { apiClient } from '../client'

export interface OpenAIMaxPolicySettings {
  action: 'pass' | 'block' | 'downgrade'
  scope: 'all' | 'oauth' | 'apikey'
  error_message: string
  downgrade_effort: '' | 'low' | 'medium' | 'high' | 'xhigh'
}

export async function getOpenAIMaxPolicySettings(): Promise<OpenAIMaxPolicySettings> {
  const { data } = await apiClient.get<OpenAIMaxPolicySettings>('/admin/settings/openai-max-policy')
  return data
}

export async function updateOpenAIMaxPolicySettings(settings: OpenAIMaxPolicySettings): Promise<OpenAIMaxPolicySettings> {
  const { data } = await apiClient.put<OpenAIMaxPolicySettings>('/admin/settings/openai-max-policy', settings)
  return data
}
