import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIMaxPolicySettings from '../OpenAIMaxPolicySettings.vue'

const { getSettings, updateSettings, showSuccess, showError } = vi.hoisted(() => ({
  getSettings: vi.fn(), updateSettings: vi.fn(), showSuccess: vi.fn(), showError: vi.fn(),
}))
vi.mock('@/api/admin/openaiMaxPolicy', () => ({
  getOpenAIMaxPolicySettings: getSettings,
  updateOpenAIMaxPolicySettings: updateSettings,
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))

const defaults = { action: 'pass', scope: 'all', error_message: '', downgrade_effort: 'high' }

describe('OpenAIMaxPolicySettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getSettings.mockResolvedValue({ ...defaults })
    updateSettings.mockImplementation(async (settings) => settings)
  })

  it('loads the persisted policy and saves a custom block message', async () => {
    getSettings.mockResolvedValue({ ...defaults, action: 'block', error_message: 'saved message' })
    const wrapper = mount(OpenAIMaxPolicySettings)
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-testid="max-error-message"]').element.value).toBe('saved message')
    await wrapper.get('[data-testid="max-error-message"]').setValue('请降低档位')
    await wrapper.get('[data-testid="max-scope"]').setValue('oauth')
    await wrapper.get('[data-testid="max-save"]').trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ ...defaults, action: 'block', scope: 'oauth', error_message: '请降低档位' })
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('only shows the appropriate fields and saves the selected downgrade target', async () => {
    const wrapper = mount(OpenAIMaxPolicySettings)
    await flushPromises()
    expect(wrapper.find('[data-testid="max-error-message"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="max-downgrade-effort"]').exists()).toBe(false)
    await wrapper.get('[data-testid="max-action"]').setValue('downgrade')
    expect(wrapper.find('[data-testid="max-error-message"]').exists()).toBe(false)
    const target = wrapper.get('[data-testid="max-downgrade-effort"]')
    expect(target.findAll('option').map((option) => option.element.value)).toEqual(['xhigh', 'high', 'medium', 'low'])
    await target.setValue('medium')
    await wrapper.get('[data-testid="max-save"]').trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ ...defaults, action: 'downgrade', downgrade_effort: 'medium' })
    wrapper.unmount()
  })

  it('prevents overwriting an unread policy and allows reloading', async () => {
    getSettings.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(OpenAIMaxPolicySettings)
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="max-save"]').exists()).toBe(false)
    expect(updateSettings).not.toHaveBeenCalled()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="max-save"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps edited values available when saving fails', async () => {
    updateSettings.mockRejectedValueOnce(new Error('save failed'))
    const wrapper = mount(OpenAIMaxPolicySettings)
    await flushPromises()
    await wrapper.get('[data-testid="max-action"]').setValue('downgrade')
    await wrapper.get('[data-testid="max-downgrade-effort"]').setValue('low')
    await wrapper.get('[data-testid="max-save"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.get<HTMLSelectElement>('[data-testid="max-downgrade-effort"]').element.value).toBe('low')
    expect(wrapper.get<HTMLButtonElement>('[data-testid="max-save"]').element.disabled).toBe(false)
    wrapper.unmount()
  })
})
