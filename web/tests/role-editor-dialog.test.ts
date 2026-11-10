import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import RoleEditorDialog from '../src/components/users/RoleEditorDialog.vue'
import { session } from '../src/api'
import { useSession } from '../src/composables/useSession'
import type { PermissionModule, RoleRecord } from '../src/types/api'

const fetchMock = vi.fn()
const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(async () => {
  vi.stubGlobal('fetch', fetchMock)
  session.value = { authenticated: true, csrf_token: 'csrf' }
  fetchMock.mockImplementationOnce(() =>
    json(200, {
      data: {
        authenticated: true,
        csrf_token: 'csrf',
        user: { id: 'u', username: 'admin', roles: [], permissions: [], is_admin: true },
      },
    }),
  )
  await useSession().check()
  fetchMock.mockReset()
})
afterEach(() => {
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

const catalogue: PermissionModule[] = [
  {
    key: 'accounts',
    label: '账号',
    page: '账号库存',
    permissions: [
      { key: 'accounts.view', label: '查看账号' },
      { key: 'accounts.create', label: '新建账号' },
    ],
  },
  {
    key: 'proxies',
    label: '代理',
    page: '代理池',
    permissions: [{ key: 'proxies.view', label: '查看代理' }],
  },
]
const editing: RoleRecord = {
  id: 'r1',
  name: '运维',
  description: '',
  builtin: false,
  permissions: ['accounts.view', 'accounts.create', 'proxies.view'],
  version: 2,
  user_count: 0,
  created_at: '',
  updated_at: '',
}

// Element Plus is stubbed so the teleported dialog renders inline; the switch
// stub emits `change` like the real component does.
const stubs = {
  'el-dialog': {
    template: '<div class="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
    props: ['title'],
  },
  'el-button': {
    template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
  },
  'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] },
  'el-form': { template: '<form><slot /></form>' },
  'el-form-item': { template: '<div><slot name="label" /><slot /></div>' },
  'el-input': { template: '<input />' },
  'el-switch': {
    template:
      '<input type="checkbox" class="switch" :checked="modelValue" :disabled="disabled" @click="$emit(\'change\', !modelValue)" />',
    props: ['modelValue', 'disabled'],
  },
  'el-dropdown': { template: '<div><slot /><slot name="dropdown" /></div>' },
  'el-dropdown-menu': { template: '<div><slot /></div>' },
  'el-dropdown-item': { template: '<div><slot /></div>' },
}

function mountDialog(role: RoleRecord | null) {
  return mount(RoleEditorDialog, {
    props: { modelValue: true, role, catalogue, roles: [editing] },
    global: { stubs },
  })
}

test('hiding a page removes every permission of that module and marks the form dirty', async () => {
  const wrapper = mountDialog(editing)
  await flushPromises()
  expect(wrapper.text()).toContain('没有改动')
  expect(wrapper.text()).toContain('已选 3')
  const pageSwitches = wrapper.findAll('.access-page-toggle .switch')
  expect(pageSwitches).toHaveLength(2)
  await pageSwitches[0]!.trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('已选 1')
  expect(wrapper.text()).toContain('保存后对已绑定用户立即生效')
  // the action inside the hidden page is now locked until the page is visible again
  const locked = wrapper.findAll('.access-perm-list li').find((li) => li.text().includes('新建账号'))!
  expect(locked.classes()).toContain('is-locked')
  expect(locked.text()).toContain('先开启「账号库存」页面可见')
})

test('saving an edited role patches with the expected version and emits saved', async () => {
  fetchMock.mockImplementation(() => json(200, { data: {} }))
  const wrapper = mountDialog(editing)
  await flushPromises()
  const create = wrapper.findAll('.access-perm-list li').find((li) => li.text().includes('新建账号'))!
  await create.find('.switch').trigger('click')
  await flushPromises()
  const save = wrapper.findAll('button').find((b) => b.text() === '保存')!
  expect(save.attributes('disabled')).toBeUndefined()
  await save.trigger('click')
  await flushPromises()
  const call = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/roles/r1'))!
  const init = call[1] as RequestInit
  expect(init.method).toBe('PATCH')
  expect(JSON.parse(init.body as string)).toEqual({
    name: '运维',
    description: '',
    permissions: ['accounts.view', 'proxies.view'],
    expected_version: 2,
  })
  expect(wrapper.emitted('saved')).toBeTruthy()
  expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([false])
})

test('creating from a copy source prefixes the name and copies permissions', async () => {
  const wrapper = mount(RoleEditorDialog, {
    props: { modelValue: true, role: null, copyFrom: editing, catalogue, roles: [editing] },
    global: { stubs },
  })
  await flushPromises()
  expect(wrapper.text()).toContain('新建角色')
  expect(wrapper.text()).toContain('已选 3')
  // a fresh form is always dirty, so the create button is enabled
  const create = wrapper.findAll('button').find((b) => b.text() === '创建角色')!
  expect(create.attributes('disabled')).toBeUndefined()
})
