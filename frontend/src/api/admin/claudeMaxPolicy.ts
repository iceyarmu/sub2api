import { apiClient } from '../client'

export interface ClaudeMaxPolicySettings {
  action: 'pass' | 'block' | 'downgrade'
  scope: 'all' | 'oauth' | 'apikey' | 'bedrock'
  error_message: string
  downgrade_effort: '' | 'low' | 'medium' | 'high' | 'xhigh'
}

export async function getClaudeMaxPolicySettings(): Promise<ClaudeMaxPolicySettings> {
  const { data } = await apiClient.get<ClaudeMaxPolicySettings>('/admin/settings/claude-max-policy')
  return data
}

export async function updateClaudeMaxPolicySettings(settings: ClaudeMaxPolicySettings): Promise<ClaudeMaxPolicySettings> {
  const { data } = await apiClient.put<ClaudeMaxPolicySettings>('/admin/settings/claude-max-policy', settings)
  return data
}
