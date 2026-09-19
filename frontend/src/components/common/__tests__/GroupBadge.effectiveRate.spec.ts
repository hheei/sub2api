import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import GroupBadge from '../GroupBadge.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null })
}))

const mountBadge = (props: Record<string, unknown>) =>
  mount(GroupBadge, {
    props: { name: 'G', platform: 'openai', ...props },
    global: { stubs: { PlatformIcon: true } }
  })

type Wrapper = ReturnType<typeof mountBadge>

/** 右侧倍率标签（唯一带生效来源 tooltip 的 span）。 */
const labelSpan = (wrapper: Wrapper) =>
  wrapper.get(
    '[title="common.rateSourceGroup"], [title="common.rateSourceCustom"], [title="common.rateSourceCustomDynamic"], [title="usage.dynamicRateTitle"]'
  )

describe('GroupBadge effective rate source', () => {
  it('renders DYN for a dynamic group when the account has no custom rate', () => {
    const wrapper = mountBadge({ rateMultiplier: 1.4, isDynamic: true })

    expect(labelSpan(wrapper).text()).toBe('usage.dynamicRate')
  })

  it('prefers a custom static rate over a dynamic group and keeps it numeric', () => {
    // 关键不变量：专属覆盖只看"有没有"，动态分组不会顶掉静态专属倍率。
    const wrapper = mountBadge({
      rateMultiplier: 1.4,
      isDynamic: true,
      userRateMultiplier: 0.9,
      userRateIsDynamic: false
    })

    const label = labelSpan(wrapper)
    expect(label.text()).toBe('1.4x0.9x')
    expect(label.find('.line-through').text()).toBe('1.4x')
    expect(label.text()).not.toContain('usage.dynamicRate')
  })

  it('renders DYN for a custom expression over a static group and strikes the group rate', () => {
    const wrapper = mountBadge({
      rateMultiplier: 1,
      isDynamic: false,
      userRateMultiplier: 1,
      userRateIsDynamic: true
    })

    const label = labelSpan(wrapper)
    expect(label.find('.line-through').text()).toBe('1x')
    expect(label.text()).toContain('usage.dynamicRate')
  })

  it('treats a custom rate equal to the group as an override of a dynamic group', () => {
    const wrapper = mountBadge({
      rateMultiplier: 1.2,
      isDynamic: true,
      userRateMultiplier: 1.2,
      userRateIsDynamic: false
    })

    const label = labelSpan(wrapper)
    expect(label.text()).toBe('1.2x')
    expect(label.find('.line-through').exists()).toBe(false)
    expect(label.text()).not.toContain('usage.dynamicRate')
  })

  it('renders a custom zero rate as an override and keeps it numeric', () => {
    const wrapper = mountBadge({
      rateMultiplier: 1,
      isDynamic: false,
      userRateMultiplier: 0,
      userRateIsDynamic: false
    })

    const label = labelSpan(wrapper)
    expect(label.text()).toBe('1x0x')
    expect(label.find('.line-through').text()).toBe('1x')
  })

  it('renders the plain group rate when nothing is dynamic and there is no override', () => {
    const wrapper = mountBadge({ rateMultiplier: 2, isDynamic: false })

    expect(labelSpan(wrapper).text()).toBe('2x')
  })

  it('describes the effective source in the label tooltip', () => {
    const customStatic = mountBadge({
      rateMultiplier: 1,
      isDynamic: true,
      userRateMultiplier: 0.5,
      userRateIsDynamic: false
    })
    const customDynamic = mountBadge({
      rateMultiplier: 1,
      isDynamic: false,
      userRateMultiplier: 0.5,
      userRateIsDynamic: true
    })
    const groupDynamic = mountBadge({ rateMultiplier: 1, isDynamic: true })
    const groupStatic = mountBadge({ rateMultiplier: 1, isDynamic: false })

    expect(customStatic.get('[title="common.rateSourceCustom"]').exists()).toBe(true)
    expect(customDynamic.get('[title="common.rateSourceCustomDynamic"]').exists()).toBe(true)
    expect(groupDynamic.get('[title="usage.dynamicRateTitle"]').exists()).toBe(true)
    expect(groupStatic.get('[title="common.rateSourceGroup"]').exists()).toBe(true)
  })
})