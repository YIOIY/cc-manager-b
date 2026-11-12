import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import {
  LayoutDashboard,
  UsersRound,
  Network,
  ListChecks,
  ChartNoAxesCombined,
  SlidersHorizontal,
  ShieldCheck,
  UserCog,
  BookOpen,
} from 'lucide-vue-next'
import type { Component } from 'vue'
import type { Permission } from './types/api'
import { can, sessionReady } from './composables/useSession'

declare module 'vue-router' {
  interface RouteMeta {
    title: string
    icon?: Component
    eyebrow?: string
    /** key into listConfigs for generic list pages */
    list?: string
    nav?: boolean
    /** permission needed to see the page; pages without it are always visible */
    permission?: Permission
  }
}

export const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'dashboard',
    component: () => import('./views/DashboardView.vue'),
    meta: { title: '仪表盘', icon: LayoutDashboard, eyebrow: 'WORKSPACE', nav: true, permission: 'dashboard.view' },
  },
  {
    path: '/accounts',
    name: 'accounts',
    component: () => import('./views/ListView.vue'),
    meta: {
      title: '账号管理',
      icon: UsersRound,
      eyebrow: 'ACCOUNT INVENTORY',
      list: 'accounts',
      nav: true,
      permission: 'accounts.view',
    },
  },
  {
    path: '/proxies',
    name: 'proxies',
    component: () => import('./views/ListView.vue'),
    meta: {
      title: '代理池',
      icon: Network,
      eyebrow: 'PROXY POOL',
      list: 'proxies',
      nav: true,
      permission: 'proxies.view',
    },
  },
  {
    path: '/jobs',
    name: 'jobs',
    component: () => import('./views/ListView.vue'),
    meta: {
      title: '任务中心',
      icon: ListChecks,
      eyebrow: 'WORKSPACE',
      list: 'jobs',
      nav: true,
      permission: 'jobs.view',
    },
  },
  {
    path: '/platform-bills',
    name: 'platformBills',
    component: () => import('./views/PlatformBillsView.vue'),
    meta: {
      title: '平台离线账单',
      icon: ChartNoAxesCombined,
      eyebrow: 'PLATFORM BILLING',
      nav: true,
      permission: 'bills.view',
    },
  },
  {
    path: '/connections',
    name: 'connections',
    component: () => import('./views/ListView.vue'),
    meta: {
      title: '平台设置',
      icon: SlidersHorizontal,
      eyebrow: 'WORKSPACE',
      list: 'connections',
      nav: true,
      permission: 'connections.view',
    },
  },
  {
    path: '/audit',
    name: 'audit',
    component: () => import('./views/ListView.vue'),
    meta: {
      title: '审计日志',
      icon: ShieldCheck,
      eyebrow: 'WORKSPACE',
      list: 'audit',
      nav: true,
      permission: 'audit.view',
    },
  },
  {
    path: '/users',
    name: 'users',
    component: () => import('./views/UsersView.vue'),
    meta: { title: '用户与权限', icon: UserCog, eyebrow: 'ACCESS CONTROL', nav: true, permission: 'users.view' },
  },
  {
    // No permission: every signed-in user can read the manual and the FAQ.
    path: '/help',
    name: 'help',
    component: () => import('./views/HelpView.vue'),
    meta: { title: '帮助文档', icon: BookOpen, eyebrow: 'HELP CENTER', nav: true },
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({ history: createWebHistory(), routes })

/** First page the current user may open; used when a route is forbidden. */
export function firstAllowedRoute(): string {
  const hit = routes.find((r) => r.meta?.nav && (!r.meta.permission || can(r.meta.permission)))
  return hit ? String(hit.name) : 'dashboard'
}

router.beforeEach(async (to) => {
  // Permissions are only known after the session check; wait for it so a
  // deep link is judged against the real user instead of an empty session.
  await sessionReady()
  if (to.meta.permission && !can(to.meta.permission)) {
    const fallback = firstAllowedRoute()
    return to.name === fallback ? true : { name: fallback }
  }
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · CCMAX MANAGER` : 'CCMAX MANAGER'
})
