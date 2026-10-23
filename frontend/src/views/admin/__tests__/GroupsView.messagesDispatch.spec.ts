import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import GroupsView from '../GroupsView.vue'

const { listGroups, createGroup, updateGroup, showError } = vi.hoisted(() => ({
  listGroups: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { list: listGroups, create: createGroup, update: updateGroup },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess: vi.fn() }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep: () => false }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

// Match the simple-mode API response, which omits commercial/advanced fields.
const sourceGroup = {
  id: 42, name: 'OpenAI', description: '', platform: 'openai', status: 'active',
  allow_messages_dispatch: false, messages_dispatch_model_config: {},
}

const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>',
})

const BaseDialogStub = defineComponent({
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
        DataTable: DataTableStub,
        BaseDialog: BaseDialogStub,
        Pagination: true,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: true,
      },
    },
  })
}

describe('GroupsView Messages dispatch in simple mode', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    listGroups.mockResolvedValue({ items: [sourceGroup], total: 1, page: 1, page_size: 20, pages: 1 })
  })

  it('creates an OpenAI group with Messages dispatch and mappings in simple mode', async () => {
    createGroup.mockResolvedValue(sourceGroup)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="group-form-name"]').setValue('Simple OpenAI')
    wrapper.findComponent('[data-tour="group-form-platform"]').vm.$emit('update:modelValue', 'openai')
    await flushPromises()

    const form = wrapper.get('#create-group-form')
    expect(form.text()).toContain('admin.groups.openaiMessages.title')
    expect(form.text()).not.toContain('admin.groups.openaiLive.title')
    expect(form.text()).not.toContain('admin.groups.accountFilters.title')
    expect(form.findAll('[role="switch"]')).toHaveLength(1)
    await form.get('[role="switch"]').trigger('click')
    await form.get('[placeholder="admin.groups.openaiMessages.sonnetModelPlaceholder"]').setValue('gpt-5.4')
    await form.trigger('submit')
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(createGroup).toHaveBeenCalledWith({
      name: 'Simple OpenAI', description: '', i18n: {}, platform: 'openai',
      allow_messages_dispatch: true,
      messages_dispatch_model_config: {
        opus_mapped_model: 'gpt-5.4', sonnet_mapped_model: 'gpt-5.4',
        haiku_mapped_model: 'gpt-5.4-mini', exact_model_mappings: {},
      },
    })
    wrapper.unmount()
  })

  it('loads, edits, and disables Messages dispatch in simple mode without losing mappings', async () => {
    const mapping = {
      opus_mapped_model: 'gpt-5.4', sonnet_mapped_model: 'gpt-5.3-codex',
      haiku_mapped_model: 'gpt-5.4-mini', exact_model_mappings: { 'claude-custom': 'gpt-5.4-mini' },
    }
    const group = { ...sourceGroup, allow_messages_dispatch: true, messages_dispatch_model_config: mapping }
    listGroups.mockResolvedValue({ items: [group], total: 1, page: 1, page_size: 20, pages: 1 })
    updateGroup.mockResolvedValue(group)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    await flushPromises()

    const form = wrapper.get('#edit-group-form')
    expect(form.findAll('[role="switch"]')).toHaveLength(1)
    expect(form.get('[role="switch"]').attributes('aria-checked')).toBe('true')
    expect(form.get<HTMLInputElement>('[placeholder="admin.groups.openaiMessages.sonnetModelPlaceholder"]').element.value).toBe(mapping.sonnet_mapped_model)
    await form.get('[placeholder="admin.groups.openaiMessages.sonnetModelPlaceholder"]').setValue('gpt-5.4')
    await form.trigger('submit')
    await flushPromises()
    expect(updateGroup).toHaveBeenLastCalledWith(42, {
      name: group.name, description: '', i18n: {}, allow_messages_dispatch: true,
      messages_dispatch_model_config: { ...mapping, sonnet_mapped_model: 'gpt-5.4' },
    })

    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    await wrapper.get('#edit-group-form [role="switch"]').trigger('click')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(updateGroup).toHaveBeenLastCalledWith(42, {
      name: group.name, description: '', i18n: {}, allow_messages_dispatch: false,
      messages_dispatch_model_config: mapping,
    })
    wrapper.unmount()
  })

})
