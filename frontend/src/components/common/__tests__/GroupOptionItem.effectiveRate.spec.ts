import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import GroupOptionItem from '../GroupOptionItem.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null })
}))

const mountItem = (props: Record<string, unknown>) =>
  mount(GroupOptionItem, {
    props: { name: 'G', platform: 'openai', ...props },
    global: { stubs: { GroupBadge: true } }
  })

type Wrapper = ReturnType<typeof mountItem>

/** 倍率药丸（唯一带生效来源 tooltip 的 rounded-full 元素）。 */
const pill = (wrapper: Wrapper) =>
  wrapper.get(
    '[title="common.rateSourceGroup"], [title="common.rateSourceCustom"], [title="common.rateSourceCustomDynamic"], [title="usage.dynamicRateTitle"]'
  )

describe('GroupOptionItem effective rate pill', () => {
  it('shows DYN for a dynamic group default', () => {
    const wrapper = mountItem({ rateMultiplier: 1.3, isDynamic: true })

    expect(pill(wrapper).text()).toBe('usage.dynamicRate')
  })

  it('keeps a custom static rate over a dynamic group default', () => {
    const wrapper = mountItem({
      rateMultiplier: 1.3,
      isDynamic: true,
      userRateMultiplier: 1.1,
      userRateIsDynamic: false
    })

    const rate = pill(wrapper)
    expect(rate.find('.line-through').text()).toBe('1.3x')
    expect(rate.text()).toBe('1.3x1.1x')
    expect(rate.text()).not.toContain('usage.dynamicRate')
  })

  it('shows DYN for a custom expression over a static group default', () => {
    const wrapper = mountItem({
      rateMultiplier: 1,
      isDynamic: false,
      userRateMultiplier: 1,
      userRateIsDynamic: true
    })

    const rate = pill(wrapper)
    expect(rate.find('.line-through').text()).toBe('1x')
    expect(rate.text()).toContain('usage.dynamicRate')
  })

  it('treats a custom rate equal to the group as an override', () => {
    const wrapper = mountItem({
      rateMultiplier: 1,
      isDynamic: true,
      userRateMultiplier: 1,
      userRateIsDynamic: false
    })

    const rate = pill(wrapper)
    expect(rate.text()).toBe('1x')
    expect(rate.find('.line-through').exists()).toBe(false)
    expect(rate.text()).not.toContain('usage.dynamicRate')
  })

  it('renders a custom zero rate as an override, not as an unset value', () => {
    const wrapper = mountItem({
      rateMultiplier: 2,
      isDynamic: false,
      userRateMultiplier: 0,
      userRateIsDynamic: false
    })

    const rate = pill(wrapper)
    // 赢家是 0x；被划掉的 2x 仍在（专属 0 ≠ 分组 2）。
    expect(rate.find('.line-through').text()).toBe('2x')
    expect(rate.text()).toBe('2x0x')
    expect(rate.text()).not.toContain('usage.dynamicRate')
  })

  it('renders the group default pill when there is no override', () => {
    const wrapper = mountItem({ rateMultiplier: 2, isDynamic: false })

    expect(pill(wrapper).text()).toBe('2x admin.groups.rateLabel')
  })

  it('names the effective source in the pill tooltip', () => {
    const custom = mountItem({
      rateMultiplier: 1,
      isDynamic: true,
      userRateMultiplier: 0.4,
      userRateIsDynamic: false
    })
    const group = mountItem({ rateMultiplier: 1, isDynamic: false })

    expect(custom.get('[title="common.rateSourceCustom"]').exists()).toBe(true)
    expect(group.get('[title="common.rateSourceGroup"]').exists()).toBe(true)
  })
})