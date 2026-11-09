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

describe.each(['openai', 'anthropic'])('GroupsView %s group model mapping in simple mode', (platform) => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    listGroups.mockResolvedValue({ items: [{ ...sourceGroup, platform }], total: 1, page: 1, page_size: 20, pages: 1 })
  })

  it('creates mapping directly on the group with Messages dispatch disabled', async () => {
    createGroup.mockResolvedValue(sourceGroup)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="group-form-name"]').setValue('Mapped group')
    expect(wrapper.find('[data-testid="group-model-mapping"]').exists()).toBe(true)
    wrapper.findComponent('[data-tour="group-form-platform"]').vm.$emit('update:modelValue', 'gemini')
    await flushPromises()
    expect(wrapper.find('[data-testid="group-model-mapping"]').exists()).toBe(false)
    wrapper.findComponent('[data-tour="group-form-platform"]').vm.$emit('update:modelValue', platform)
    await flushPromises()
    const form = wrapper.get('#create-group-form')
    await form.get('[data-testid="add-model-mapping"]').trigger('click')
    await form.get('[data-testid="mapping-source"]').setValue(' alias-* ')
    await form.get('[data-testid="mapping-target"]').setValue(' gpt-5.4 ')
    await form.trigger('submit')
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(createGroup).toHaveBeenCalledWith(expect.objectContaining({
      platform, model_mapping: { 'alias-*': 'gpt-5.4' },
      ...(platform === 'openai' ? { allow_messages_dispatch: false } : {}),
    }))
    wrapper.unmount()
  })

  it('loads, updates, reopens and clears the group mapping independently of Messages', async () => {
    let savedGroup = { ...sourceGroup, platform, model_mapping: { alias: 'gpt-5.4' } as Record<string, string> }
    listGroups.mockImplementation(async () => ({ items: [savedGroup], total: 1, page: 1, page_size: 20, pages: 1 }))
    updateGroup.mockImplementation(async (_id, payload) => { savedGroup = { ...savedGroup, ...payload }; return savedGroup })
    const wrapper = mountView()
    await flushPromises()
    const openEdit = async () => {
      await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
      await flushPromises()
      return wrapper.get('#edit-group-form')
    }
    let form = await openEdit()
    if (platform === 'openai') expect(form.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(form.get<HTMLInputElement>('[data-testid="mapping-source"]').element.value).toBe('alias')
    expect(form.get<HTMLInputElement>('[data-testid="mapping-target"]').element.value).toBe('gpt-5.4')
    await form.get('[data-testid="mapping-target"]').setValue('gpt-5.5')
    await form.trigger('submit')
    await flushPromises()
    expect(updateGroup).toHaveBeenLastCalledWith(42, expect.objectContaining({ model_mapping: { alias: 'gpt-5.5' }, ...(platform === 'openai' ? { allow_messages_dispatch: false } : {}) }))
    form = await openEdit()
    expect(form.get<HTMLInputElement>('[data-testid="mapping-target"]').element.value).toBe('gpt-5.5')
    await form.get('[data-testid="remove-model-mapping"]').trigger('click')
    await form.trigger('submit')
    await flushPromises()
    expect(updateGroup).toHaveBeenLastCalledWith(42, expect.objectContaining({ model_mapping: {} }))
    form = await openEdit()
    expect(form.findAll('[data-testid="mapping-source"]')).toHaveLength(0)
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('rejects duplicate sources instead of silently overwriting a rule', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    const form = wrapper.get('#edit-group-form')
    for (let i = 0; i < 2; i++) {
      await form.get('[data-testid="add-model-mapping"]').trigger('click')
      await form.findAll('[data-testid="mapping-source"]')[i]!.setValue('alias')
      await form.findAll('[data-testid="mapping-target"]')[i]!.setValue('gpt-5.4')
    }
    await form.trigger('submit')
    await flushPromises()
    expect(updateGroup).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })
})
