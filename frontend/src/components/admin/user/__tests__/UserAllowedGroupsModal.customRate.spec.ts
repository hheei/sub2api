import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'

import type { AdminUser, Group } from '@/types'
import UserAllowedGroupsModal from '../UserAllowedGroupsModal.vue'

const { listGroups, updateUser, showSuccess, showError } = vi.hoisted(() => ({
  listGroups: vi.fn(),
  updateUser: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { list: listGroups },
    users: { update: updateUser }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const group: Group = {
  id: 7,
  name: 'Standard group',
  description: '',
  platform: 'openai',
  subscription_type: 'standard',
  status: 'active',
  rate_multiplier: 1,
  is_dynamic: false,
  is_exclusive: false,
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1
} as Group

const user: AdminUser = {
  id: 42,
  email: 'user@example.com',
  role: 'user',
  status: 'active',
  balance: 0,
  created_at: '',
  updated_at: '',
  notes: '',
  group_rates: { 7: { rate_multiplier: 1, rate_multiplier_expr: '$up * 1.05' } }
} as AdminUser

const mountModal = async (dialogUser: AdminUser = user) => {
  const wrapper = mount(UserAllowedGroupsModal, {
    props: { show: false, user: dialogUser },
    global: {
      stubs: { PlatformIcon: true, BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } }
    }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

const rateInput = () => 'input[type="text"]'

const save = async (wrapper: Awaited<ReturnType<typeof mountModal>>) => {
  const buttons = wrapper.findAll('button')
  const saveButton = buttons.find((button) => button.text().includes('common.save'))!
  await saveButton.trigger('click')
  await flushPromises()
}

describe('UserAllowedGroupsModal custom rate editing', () => {
  beforeEach(() => {
    listGroups.mockReset()
    updateUser.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    listGroups.mockResolvedValue({ items: [group], total: 1, page: 1, page_size: 1000, pages: 1 })
    updateUser.mockResolvedValue({})
  })

  it('shows the raw expression so admins can edit it', async () => {
    const wrapper = await mountModal()

    expect(wrapper.get(rateInput()).element.value).toBe('$up * 1.05')
    expect(wrapper.text()).toContain('admin.users.customRateDynamic')
  })

  it('submits the expression with its numeric fallback when untouched', async () => {
    const wrapper = await mountModal()

    await save(wrapper)

    expect(updateUser).toHaveBeenCalledWith(
      42,
      expect.objectContaining({
        group_rates: { 7: { rate_multiplier: 1, rate_multiplier_expr: '$up * 1.05' } }
      })
    )
  })

  it('clears the expression when a plain number is entered', async () => {
    const wrapper = await mountModal()

    await wrapper.get(rateInput()).setValue('0.8')
    await save(wrapper)

    expect(updateUser).toHaveBeenCalledWith(
      42,
      expect.objectContaining({
        group_rates: { 7: { rate_multiplier: 0.8, rate_multiplier_expr: '' } }
      })
    )
  })

  it('accepts 0 as a real override rather than clearing it', async () => {
    const wrapper = await mountModal()

    await wrapper.get(rateInput()).setValue('0')
    await save(wrapper)

    expect(updateUser).toHaveBeenCalledWith(
      42,
      expect.objectContaining({
        group_rates: { 7: { rate_multiplier: 0, rate_multiplier_expr: '' } }
      })
    )
  })

  it('turns a typed expression into a dynamic override with fallback 1', async () => {
    const wrapper = await mountModal()

    await wrapper.get(rateInput()).setValue('$up * 1.2')
    await save(wrapper)

    expect(updateUser).toHaveBeenCalledWith(
      42,
      expect.objectContaining({
        group_rates: { 7: { rate_multiplier: 1, rate_multiplier_expr: '$up * 1.2' } }
      })
    )
  })

  it('clears the override by submitting null when the field is emptied', async () => {
    const wrapper = await mountModal()

    await wrapper.get(rateInput()).setValue('')
    await save(wrapper)

    expect(updateUser).toHaveBeenCalledWith(
      42,
      expect.objectContaining({ group_rates: { 7: null } })
    )
  })

  it('omits group_rates entirely when the user had no override and none is set', async () => {
    const wrapper = await mountModal({ ...user, group_rates: {} })

    expect(wrapper.get(rateInput()).element.value).toBe('')
    await save(wrapper)

    expect(updateUser).toHaveBeenCalledWith(
      42,
      expect.objectContaining({ group_rates: undefined })
    )
  })
})