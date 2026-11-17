import { expect, test } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountCompletionChecklist from '../src/components/AccountCompletionChecklist.vue'

const stubs = {
  'el-button': { template: '<button @click="$emit(\'click\')"><slot /></button>', emits: ['click'] },
  'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] },
}
const items = [
  { key: 'password', label: '交付密码', hint: 'Claude 授权不返回密码', done: false, anchor: 'field-password' },
  { key: 'device_ids', label: '设备标识', hint: '选填', done: false, anchor: 'field-device-ids' },
  { key: 'connection', label: '平台连接', hint: '已选 平台 A', done: true, anchor: 'field-connection' },
]

test('shows remaining count, jumps to a field, and hosts the device input while it is missing', async () => {
  const wrapper = mount(AccountCompletionChecklist, {
    props: { items, warnings: ['资料读取失败'] },
    global: { stubs },
    slots: { device: '<textarea id="field-device-ids" />' },
  })
  expect(wrapper.get('.completion-head').text()).toContain('还差 2 项')
  expect(wrapper.findAll('.completion-item.is-done')).toHaveLength(1)
  expect(wrapper.find('#field-device-ids').exists()).toBe(true)
  expect(wrapper.get('.alert').text()).toBe('资料读取失败')
  await wrapper.findAll('button')[0]!.trigger('click')
  expect(wrapper.emitted('jump')?.[0]).toEqual(['field-password'])
  await wrapper.setProps({ items: items.map((i) => ({ ...i, done: true })) })
  expect(wrapper.get('.completion-head').text()).toContain('资料已齐全')
  // the device input stays editable after the item is satisfied
  expect(wrapper.find('#field-device-ids').exists()).toBe(true)
  expect(wrapper.classes()).toContain('is-ready')
  // a non-presence problem (date order) replaces the ready message
  await wrapper.setProps({ blocker: '套餐开始日期早于结束日期' })
  expect(wrapper.get('.completion-head').text()).toContain('套餐开始日期早于结束日期')
  expect(wrapper.get('.completion-head').text()).not.toContain('资料已齐全')
  expect(wrapper.classes()).not.toContain('is-ready')
})
