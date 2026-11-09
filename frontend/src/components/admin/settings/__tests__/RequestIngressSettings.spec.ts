import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import RequestIngressSettings from '../RequestIngressSettings.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('RequestIngressSettings', () => {
  it('accepts multiple IP/CIDR entries without losing a trailing input newline', async () => {
    const wrapper = mount(RequestIngressSettings, { props: { validate: false, proxies: [] } })
    const input = wrapper.get('textarea')
    await input.setValue('192.0.2.1\n10.0.0.0/8,2001:db8::/64\n192.0.2.1\n')
    const expected = ['192.0.2.1', '10.0.0.0/8', '2001:db8::/64']
    expect(wrapper.emitted('update:proxies')?.at(-1)).toEqual([expected])
    await wrapper.setProps({ proxies: expected })
    expect((input.element as HTMLTextAreaElement).value).toMatch(/\n$/)
    await input.setValue('')
    expect(wrapper.emitted('update:proxies')?.at(-1)).toEqual([[]])
    expect(wrapper.findComponent({ name: 'Toggle' }).props('modelValue')).toBe(false)
  })
})
