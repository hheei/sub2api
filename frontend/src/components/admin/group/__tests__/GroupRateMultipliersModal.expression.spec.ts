import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminGroup } from '@/types'
import GroupRateMultipliersModal from '../GroupRateMultipliersModal.vue'

const { getGroupRateMultipliers, batchSetGroupRateMultipliers, showSuccess, showError } = vi.hoisted(
  () => ({
    getGroupRateMultipliers: vi.fn(),
    batchSetGroupRateMultipliers: vi.fn(),
    showSuccess: vi.fn(),
    showError: vi.fn()
  })
)

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { getGroupRateMultipliers, batchSetGroupRateMultipliers },
    users: { list: vi.fn().mockResolvedValue({ items: [] }) }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const group = { id: 3, name: 'Group', platform: 'openai', rate_multiplier: 1 } as AdminGroup

const entry = (overrides: Record<string, unknown>) => ({
  user_id: 9,
  user_name: 'u',
  user_email: 'u@example.com',
  user_notes: '',
  user_status: 'active',
  rate_multiplier: 1,
  rate_multiplier_expr: '',
  rpm_override: null,
  ...overrides
})

const mountModal = async () => {
  const wrapper = mount(GroupRateMultipliersModal, {
    props: { show: false, group },
    global: {
      stubs: {
        PlatformIcon: true,
        Icon: true,
        Pagination: true,
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
      }
    }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

type Wrapper = Awaited<ReturnType<typeof mountModal>>

const rowInput = (wrapper: Wrapper) => wrapper.get('tbody input[type="text"]')

/** 行内输入框用 @change 提交，必须显式触发。 */
const setRowValue = async (wrapper: Wrapper, value: string) => {
  await rowInput(wrapper).setValue(value)
  await rowInput(wrapper).trigger('change')
  await flushPromises()
}

/** 保存按钮只在有未提交修改时出现（v-if="isDirty"）。 */
const saveButton = (wrapper: Wrapper) =>
  wrapper.findAll('button').find((b) => b.text().includes('common.save'))

const save = async (wrapper: Wrapper) => {
  const button = saveButton(wrapper)
  if (!button) throw new Error('save button missing: isDirty was false')
  await button.trigger('click')
  await flushPromises()
}

describe('GroupRateMultipliersModal expression editing', () => {
  beforeEach(() => {
    getGroupRateMultipliers.mockReset()
    batchSetGroupRateMultipliers.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    batchSetGroupRateMultipliers.mockResolvedValue({})
  })

  it('shows the raw expression in the row input', async () => {
    getGroupRateMultipliers.mockResolvedValue([
      entry({ rate_multiplier: 1, rate_multiplier_expr: '$up * 1.1' })
    ])

    const wrapper = await mountModal()

    expect(rowInput(wrapper).element.value).toBe('$up * 1.1')
    expect(wrapper.text()).toContain('usage.dynamicRate')
  })

  it('sends an edited expression through and keeps the dynamic marker', async () => {
    getGroupRateMultipliers.mockResolvedValue([
      entry({ rate_multiplier: 1, rate_multiplier_expr: '$up * 1.1' })
    ])

    const wrapper = await mountModal()
    await setRowValue(wrapper, '$up * 1.3')
    await save(wrapper)

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(3, [
      { user_id: 9, rate_multiplier: 1, rate_multiplier_expr: '$up * 1.3' }
    ])
  })

  it('clears the expression when a number is entered in the same field', async () => {
    getGroupRateMultipliers.mockResolvedValue([
      entry({ rate_multiplier: 1, rate_multiplier_expr: '$up * 1.1' })
    ])

    const wrapper = await mountModal()
    await setRowValue(wrapper, '0.75')
    await save(wrapper)

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(3, [
      { user_id: 9, rate_multiplier: 0.75, rate_multiplier_expr: '' }
    ])
  })

  it('accepts 0 as a real rate rather than dropping the row', async () => {
    getGroupRateMultipliers.mockResolvedValue([entry({ rate_multiplier: 1 })])

    const wrapper = await mountModal()
    await setRowValue(wrapper, '0')
    await save(wrapper)

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(3, [
      { user_id: 9, rate_multiplier: 0, rate_multiplier_expr: '' }
    ])
  })

  it('turns a typed expression into a dynamic override with fallback 1', async () => {
    getGroupRateMultipliers.mockResolvedValue([entry({ rate_multiplier: 0.5 })])

    const wrapper = await mountModal()
    await setRowValue(wrapper, '$up + 0.25')
    await save(wrapper)

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(3, [
      { user_id: 9, rate_multiplier: 1, rate_multiplier_expr: '$up + 0.25' }
    ])
  })

  it('preserves an expression-only row when another row changes', async () => {
    getGroupRateMultipliers.mockResolvedValue([
      entry({ user_id: 9, rate_multiplier: null, rate_multiplier_expr: '$up * 1.1' }),
      entry({ user_id: 10, user_email: 'other@example.com', rate_multiplier: 0.5 })
    ])

    const wrapper = await mountModal()
    const inputs = wrapper.findAll('tbody input[type="text"]')
    expect(inputs[0].element.value).toBe('$up * 1.1')
    await inputs[1].setValue('0.75')
    await inputs[1].trigger('change')
    await flushPromises()
    await save(wrapper)

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(3, [
      { user_id: 9, rate_multiplier: 1, rate_multiplier_expr: '$up * 1.1' },
      { user_id: 10, rate_multiplier: 0.75, rate_multiplier_expr: '' }
    ])
  })

  it('drops the row when the input is emptied', async () => {
    getGroupRateMultipliers.mockResolvedValue([entry({ rate_multiplier: 0.5 })])

    const wrapper = await mountModal()
    await setRowValue(wrapper, '')
    await save(wrapper)

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(3, [])
  })

  it('stays clean until a rate or expression actually changes', async () => {
    getGroupRateMultipliers.mockResolvedValue([
      entry({ rate_multiplier: 1, rate_multiplier_expr: '$up * 1.1' })
    ])

    const wrapper = await mountModal()

    expect(saveButton(wrapper)).toBeUndefined()

    await setRowValue(wrapper, '$up * 1.1')

    expect(saveButton(wrapper)).toBeUndefined()
    expect(batchSetGroupRateMultipliers).not.toHaveBeenCalled()
  })
})