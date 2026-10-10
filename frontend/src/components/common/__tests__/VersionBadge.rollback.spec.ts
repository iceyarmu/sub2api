import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import VersionBadge from '../VersionBadge.vue'

const api = vi.hoisted(() => ({
  getRollbackVersions: vi.fn(), rollback: vi.fn(), performUpdate: vi.fn(), restartService: vi.fn()
}))
const appStore = vi.hoisted(() => ({
  currentVersion: '0.2.15', latestVersion: '0.2.15', hasUpdate: false,
  versionLoading: false, releaseInfo: null, buildType: 'release',
  fetchVersion: vi.fn(), clearVersionCache: vi.fn()
}))
vi.mock('@/api/admin/system', () => api)
vi.mock('@/stores', () => ({ useAuthStore: () => ({ isAdmin: true }), useAppStore: () => appStore }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({
  t: (key: string, args?: { version?: string }) => key + (args?.version ? ` ${args.version}` : '')
}) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() }) }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  api.getRollbackVersions.mockResolvedValue({ versions: [
    { version: '0.2.15', published_at: '', html_url: '' },
    { version: '0.2.14', published_at: '', html_url: '' }
  ] })
  api.rollback.mockResolvedValue({ need_restart: true })
})

async function openRollbackPanel() {
  const wrapper = mount(VersionBadge, { global: { stubs: { Icon: true } } })
  await wrapper.get('button').trigger('click')
  await wrapper.findAll('button').find(button => button.text() === 'version.rollback')!.trigger('click')
  await flushPromises()
  return wrapper
}

describe('version rollback', () => {
  it('labels and reinstalls the current version through the versioned rollback API', async () => {
    const wrapper = await openRollbackPanel()
    const current = wrapper.findAll('button').find(button => button.text().includes('v0.2.15') && button.text().includes('version.currentVersion'))!
    expect(current.text()).toContain('v0.2.15')
    await current.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'version.rollbackConfirm v0.2.15')!.trigger('click')
    await flushPromises()
    expect(api.rollback).toHaveBeenCalledWith('0.2.15')
    expect(appStore.clearVersionCache).toHaveBeenCalled()
    expect(wrapper.text()).toContain('version.restartRequired')
  })

  it('uses fork scripts and images and forces a fresh Docker install', async () => {
    const wrapper = await openRollbackPanel()
    await wrapper.findAll('button').find(button => button.text().includes('v0.2.15') && button.text().includes('version.currentVersion'))!.trigger('click')
    expect(wrapper.text()).toContain('https://raw.githubusercontent.com/iceyarmu/sub2api/main/deploy/install.sh')
    expect(wrapper.text()).toContain('rollback v0.2.15')
    await wrapper.findAll('button').find(button => button.text() === 'version.deployDocker')!.trigger('click')
    expect(wrapper.text()).toContain('ghcr.io/iceyarmu/sub2api:0.2.15')
    expect(wrapper.text()).toContain('docker compose up -d --pull always --force-recreate sub2api')
    await wrapper.findAll('button').find(button => button.text() === 'v0.2.14')!.trigger('click')
    expect(wrapper.text()).toContain('version.rollbackConfirm v0.2.14')
    expect(wrapper.text()).toContain('ghcr.io/iceyarmu/sub2api:0.2.14')
    expect(wrapper.text()).not.toContain('weishaw/sub2api')
  })
})
