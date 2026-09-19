import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey, Group } from '@/types'
import KeysView from '../KeysView.vue'

const { listKeys, getPublicSettings, getDashboardApiKeysUsage, getAvailableGroups, getUserGroupRates } =
  vi.hoisted(() => ({
    listKeys: vi.fn(),
    getPublicSettings: vi.fn(),
    getDashboardApiKeysUsage: vi.fn(),
    getAvailableGroups: vi.fn(),
    getUserGroupRates: vi.fn()
  }))

vi.mock('@/api', () => ({
  keysAPI: {
    list: listKeys,
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    toggleStatus: vi.fn()
  },
  authAPI: { getPublicSettings },
  usageAPI: { getDashboardApiKeysUsage },
  userGroupsAPI: { getAvailable: getAvailableGroups, getUserGroupRates }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep: () => false, nextStep: vi.fn() })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

/** 渲染 #option / #selected 插槽，让 GroupOptionItem / GroupBadge 拿到真实 props。 */
const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: `
    <div>
      <template v-for="option in options" :key="String(option.value)">
        <slot name="selected" :option="option" :selected="option.value === modelValue" />
        <slot name="option" :option="option" :selected="option.value === modelValue" />
      </template>
    </div>
  `
}

/** 渲染 cell-group 插槽，覆盖表格行内的 GroupBadge。 */
const DataTableStub = {
  name: 'DataTable',
  props: { columns: Array, data: Array, selectedKeys: Array, selectable: Boolean },
  emits: ['sort', 'update:selectedKeys'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" data-test="row">
        <slot name="cell-group" :row="row" />
      </div>
    </div>
  `
}

const AppLayoutStub = { template: '<div><slot /></div>' }
const TablePageLayoutStub = {
  template:
    '<div><slot name="filters" /><slot name="actions" /><slot name="table" /><slot name="pagination" /></div>'
}
const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" role="dialog"><slot /><slot name="footer" /></div>'
}

const group: Group = {
  id: 1,
  name: 'Dynamic group',
  description: '',
  platform: 'openai',
  subscription_type: 'standard',
  rate_multiplier: 1.5,
  is_dynamic: true,
  is_exclusive: false,
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1
}

const createApiKey = (): ApiKey & { group: Group } => ({
  id: 1,
  user_id: 1,
  key: 'sk-test',
  name: 'test-key',
  group_id: 1,
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-06-27T00:00:00Z',
  updated_at: '2026-06-27T00:00:00Z',
  current_concurrency: 0,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
  group: { ...group }
})

const mountView = async () => {
  const wrapper = mount(KeysView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Select: SelectStub,
        SearchInput: true,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Icon: true,
        UseKeyModal: true,
        BulkEditKeysModal: true,
        EndpointPopover: true,
        GroupBadge: true,
        GroupOptionItem: true,
        Teleport: true
      }
    }
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

/** 打开创建弹窗，使分组下拉与表格行同时挂载。 */
const mountWithCreateOpen = async () => {
  const wrapper = await mountView()
  await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
  await flushPromises()
  await nextTick()
  return wrapper
}

const rowBadge = (wrapper: Awaited<ReturnType<typeof mountView>>) =>
  wrapper.get('[data-test="row"]').findComponent({ name: 'GroupBadge' })

describe('KeysView group rate selector propagation', () => {
  beforeEach(() => {
    localStorage.clear()
    listKeys.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([{ ...group }])
  })

  it('carries both dynamic flags into the group option item', async () => {
    getUserGroupRates.mockResolvedValue({ 1: { rate_multiplier: 0.5, is_dynamic: true } })

    const wrapper = await mountWithCreateOpen()

    const option = wrapper.findComponent({ name: 'GroupOptionItem' })
    expect(option.props('rateMultiplier')).toBe(1.5)
    expect(option.props('isDynamic')).toBe(true)
    expect(option.props('userRateMultiplier')).toBe(0.5)
    expect(option.props('userRateIsDynamic')).toBe(true)
  })

  it('keeps a static custom override marked static even when the group is dynamic', async () => {
    getUserGroupRates.mockResolvedValue({ 1: { rate_multiplier: 0.5, is_dynamic: false } })

    const wrapper = await mountWithCreateOpen()

    const option = wrapper.findComponent({ name: 'GroupOptionItem' })
    expect(option.props('userRateMultiplier')).toBe(0.5)
    expect(option.props('userRateIsDynamic')).toBe(false)
    expect(option.props('isDynamic')).toBe(true)
  })

  it('treats a zero custom rate as an override instead of an unset value', async () => {
    getUserGroupRates.mockResolvedValue({ 1: { rate_multiplier: 0, is_dynamic: false } })

    const wrapper = await mountWithCreateOpen()

    expect(wrapper.findComponent({ name: 'GroupOptionItem' }).props('userRateMultiplier')).toBe(0)
    expect(rowBadge(wrapper).props('userRateMultiplier')).toBe(0)
  })

  it('carries the flags into the table row badge', async () => {
    getUserGroupRates.mockResolvedValue({ 1: { rate_multiplier: 0.5, is_dynamic: true } })

    const wrapper = await mountView()

    const badge = rowBadge(wrapper)
    expect(badge.props('isDynamic')).toBe(true)
    expect(badge.props('userRateMultiplier')).toBe(0.5)
    expect(badge.props('userRateIsDynamic')).toBe(true)
  })
})