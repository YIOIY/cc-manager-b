<template>
  <div class="shell">
    <aside>
      <div class="brand">
        <span class="brand-mark"><KeyRound :size="15" /></span><span class="brand-word">CCMAX <b>MANAGER</b></span>
      </div>
      <div class="nav-caption">管理工作台</div>
      <nav aria-label="主导航">
        <button
          v-for="item in navItems"
          :key="String(item.name)"
          type="button"
          :class="{ active: route.name === item.name }"
          :aria-current="route.name === item.name ? 'page' : undefined"
          :title="item.meta.title"
          @click="go(item)"
        >
          <component :is="item.meta.icon" :size="18" :stroke-width="1.8" /><span>{{ item.meta.title }}</span>
          <em v-if="item.name === 'feedback' && feedbackBadge" class="nav-badge" :title="feedbackBadge + ' 条待处理'">{{
            feedbackBadge
          }}</em>
          <i v-if="route.name === item.name" class="nav-mark" />
        </button>
      </nav>
      <div class="shell-user">
        <span class="shell-user-avatar">{{ (user?.nickname || user?.username || '?').slice(0, 1).toUpperCase() }}</span>
        <div>
          <strong :title="user?.username">{{ user?.nickname || user?.username }}</strong>
          <small>{{ roleLabel }}</small>
        </div>
        <el-button text size="small" title="修改密码" @click="passwordOpen = true"><KeyRound :size="14" /></el-button>
      </div>
      <el-button text @click="signOut"><LogOut :size="16" /><span>退出登录</span></el-button>
    </aside>
    <main>
      <header>
        <div class="breadcrumb">
          工作台<span>/</span><strong>{{ route.meta.title || '工作台' }}</strong>
        </div>
        <div v-if="announcementText" class="header-announcement" :title="announcementText" role="status">
          <Megaphone class="header-announcement-icon" :size="14" :stroke-width="2" aria-hidden="true" />
          <div class="header-announcement-viewport" :class="{ scrolling: announcementNeedsMarquee }">
            <div
              class="header-announcement-track"
              :style="announcementNeedsMarquee ? { animationDuration: marqueeDuration } : undefined"
            >
              <span>{{ announcementText }}</span>
              <span v-if="announcementNeedsMarquee" class="header-announcement-dup" aria-hidden="true">{{
                announcementText
              }}</span>
            </div>
          </div>
        </div>
        <div class="header-actions">
          <button
            v-if="can('announcement.manage')"
            type="button"
            class="header-feedback"
            title="发布公告"
            @click="openAnnouncementEdit()"
          >
            <Megaphone :size="15" />公告
          </button>
          <button type="button" class="header-feedback" @click="openFeedback()">
            <MessageSquarePlus :size="15" />反馈
          </button>
          <span class="status-dot">会话正常</span>
        </div>
      </header>
      <section class="content">
        <router-view :key="route.path" />
      </section>
    </main>
    <ChangePasswordDialog v-model="passwordOpen" />
    <FeedbackDialog />
    <AnnouncementDialog />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { KeyRound, LogOut, Megaphone, MessageSquarePlus } from 'lucide-vue-next'
import { useRoute, useRouter, type RouteRecordRaw } from 'vue-router'
import { routes } from '../router'
import { useSession } from '../composables/useSession'
import { rememberedQuery } from '../composables/useListPage'
import ChangePasswordDialog from '../components/ChangePasswordDialog.vue'
import FeedbackDialog from '../components/FeedbackDialog.vue'
import AnnouncementDialog from '../components/AnnouncementDialog.vue'
import { feedbackOpenCount, openFeedback, refreshFeedbackSummary } from '../composables/useFeedback'
import {
  announcementBody,
  announcementNeedsMarquee,
  loadAnnouncement,
  openAnnouncementEdit,
} from '../composables/useAnnouncement'

const route = useRoute()
const router = useRouter()
const { logout, user, can } = useSession()
const passwordOpen = ref(false)
const roleLabel = computed(() => (user.value?.roles || []).map((r) => r.name).join(' · ') || '未分配角色')
const navItems = computed(() =>
  routes.filter(
    (r): r is RouteRecordRaw & { meta: NonNullable<RouteRecordRaw['meta']> } =>
      !!r.meta?.nav && (!r.meta.permission || can(r.meta.permission)),
  ),
)

const announcementText = computed(() => announcementBody.value.trim())
const marqueeDuration = computed(() => `${Math.max(14, announcementText.value.length * 0.28)}s`)

// People who handle feedback see how much is waiting; everyone else only
// has their own, which they sent themselves, so no number is shown.
const feedbackBadge = computed(() => (can('feedback.manage') ? feedbackOpenCount.value : 0))
onMounted(() => {
  void refreshFeedbackSummary()
  void loadAnnouncement()
})
watch(() => route.name, refreshFeedbackSummary)

function go(item: RouteRecordRaw) {
  router.push({ name: item.name as string, query: rememberedQuery(String(item.name)) })
}
async function signOut() {
  await logout()
  router.replace('/')
}
</script>

<style>
.shell {
  display: flex;
  min-height: 100vh;
}

.shell > aside {
  width: 252px;
  background: linear-gradient(180deg, #0a1723 0%, #102334 100%);
  color: var(--slate-300);
  padding: 26px 16px 18px;
  display: flex;
  flex-direction: column;
  position: relative;
  overflow: hidden;
}

.shell > aside:after {
  content: '';
  position: absolute;
  width: 190px;
  height: 190px;
  border: 1px solid rgba(87, 213, 183, 0.16);
  border-radius: 50%;
  right: -105px;
  bottom: -48px;
  box-shadow:
    0 0 0 24px rgba(87, 213, 183, 0.03),
    0 0 0 48px rgba(87, 213, 183, 0.025);
}

.nav-caption {
  color: #5d7483;
  text-transform: uppercase;
  letter-spacing: 0.16em;
  font-size: 10px;
  font-weight: 800;
  padding: 0 15px 10px;
  position: relative;
  z-index: 1;
}

.nav-mark {
  position: absolute;
  right: -17px;
  width: 3px;
  height: 20px;
  border-radius: 3px;
  background: #62d9bb;
  box-shadow: 0 0 14px #62d9bb88;
}

.shell-user {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: auto 8px 4px;
  padding: 10px 10px 10px 12px;
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.05);
  color: var(--slate-300);
  position: relative;
  z-index: 1;
}
.shell-user-avatar {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  flex: none;
  border-radius: 9px;
  background: var(--accent);
  color: var(--surface);
  font-weight: 800;
  font-size: 13px;
}
.shell-user > div {
  min-width: 0;
  flex: 1;
  display: grid;
  line-height: 1.3;
}
.shell-user strong {
  color: var(--surface);
  font-size: 12.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.shell-user small {
  font-size: 11px;
  color: #8ea0b1;
}
.shell-user .el-button {
  color: var(--slate-300);
  padding: 6px;
}
.shell > aside > .el-button {
  margin: 0 8px 0;
  color: var(--slate-300);
  justify-content: flex-start;
  padding: 10px 13px;
  position: relative;
  z-index: 1;
  display: flex;
  gap: 8px;
  align-items: center;
}

.shell > aside > .el-button:hover {
  color: var(--surface);
  background: #ffffff0b;
}

.shell main {
  flex: 1;
  min-width: 0;
}

.shell > main > header {
  height: 84px;
  background: rgba(255, 255, 255, 0.88);
  backdrop-filter: blur(18px);
  border-bottom: 1px solid rgba(218, 226, 230, 0.9);
  padding: 18px 42px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  position: sticky;
  top: 0;
  z-index: 3;
}

.shell > main > header h2 {
  margin: 0;
  font-size: 16px;
}

.shell > main > header h2:before {
  content: '/';
  color: var(--accent);
  margin-right: 8px;
  font-family: 'DM Mono', monospace;
}

.content {
  padding: 34px 42px 54px;
  max-width: 1540px;
  margin: 0 auto;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

.header-announcement {
  flex: 1;
  min-width: 0;
  margin: 0 16px;
  color: #263b46;
  font-size: 14px;
  line-height: 1.5;
  display: flex;
  align-items: center;
  gap: 8px;
}

.header-announcement-icon {
  flex-shrink: 0;
  color: var(--accent);
}

.header-announcement-viewport {
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.header-announcement-viewport.scrolling {
  mask-image: linear-gradient(90deg, #000 0, #000 calc(100% - 16px), transparent);
}

.header-announcement-track {
  display: block;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: inherit;
}

.header-announcement-viewport.scrolling .header-announcement-track {
  display: inline-block;
  overflow: visible;
  text-overflow: clip;
  max-width: none;
  animation-name: announcement-marquee;
  animation-timing-function: linear;
  animation-iteration-count: infinite;
}

.header-announcement-dup {
  padding-left: 48px;
}

@keyframes announcement-marquee {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(-50%);
  }
}

.header-feedback {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 12px;
  border: 1px solid var(--line-tint);
  border-radius: 99px;
  background: var(--surface);
  color: var(--ink-mid);
  font: inherit;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.header-feedback:hover,
.header-feedback:focus-visible {
  border-color: var(--accent);
  color: var(--accent-deep);
  outline: none;
}

.nav-badge {
  margin-left: auto;
  min-width: 20px;
  padding: 1px 6px;
  border-radius: 99px;
  background: #e0a030;
  color: #1a1204;
  font:
    700 11px/1.6 'DM Mono',
    ui-monospace,
    monospace;
  font-style: normal;
  text-align: center;
}

.status-dot {
  font-size: 11px;
  font-weight: 700;
  color: #18876f;
  background: #e5f7f1;
  border: 1px solid #c9eee3;
  border-radius: 99px;
  padding: 7px 11px;
}

.status-dot:before {
  content: '';
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #20b38d;
  margin-right: 6px;
  vertical-align: 1px;
}

@media (max-width: 980px) {
  .nav-caption {
    display: none;
  }
  .shell > aside {
    width: 78px;
    padding: 24px 10px;
  }
  .shell > aside nav a {
    justify-content: center;
    font-size: 0;
    padding: 13px;
  }
  .shell > aside nav a:before {
    display: none;
  }
  .shell > aside > .el-button {
    font-size: 0;
    justify-content: center;
    padding: 12px;
  }
  .content {
    padding-left: 22px;
    padding-right: 22px;
  }
  .shell > main > header {
    padding-left: 22px;
    padding-right: 22px;
  }
}

@media (max-width: 620px) {
  .content {
    padding: 24px 14px 40px;
  }
  .shell > main > header {
    padding: 16px 14px;
  }
}

.shell > aside nav button {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 11px 13px;
  color: #9caebb;
  border: 1px solid transparent;
  border-radius: 8px;
  cursor: pointer;
  background: transparent;
  font-size: 13px;
  font-weight: 600;
  text-align: left;
  position: relative;
}

.shell > aside nav button svg {
  color: #6f8796;
}

.shell > aside nav button.active {
  background: #1a3b40;
  color: var(--tint-50);
  border-color: #30615c;
}

.shell > aside nav button:hover {
  background: #1a3b40;
  color: var(--tint-50);
  border-color: #30615c;
}

.shell > aside nav button.active svg {
  color: var(--accent-light);
}

.shell > aside nav button:hover svg {
  color: var(--accent-light);
}

.shell > aside nav button:focus-visible {
  outline: 2px solid var(--accent-light);
  outline-offset: 2px;
}

.breadcrumb {
  font-size: 12px;
  line-height: 1.5;
  color: #84939b;
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

.breadcrumb span {
  margin: 0 9px;
  color: #bdc9ce;
}

.breadcrumb strong {
  color: #263b46;
}

@media (max-width: 980px) {
  .shell > aside nav button {
    justify-content: center;
    font-size: 0;
    padding: 13px;
  }
  .shell > aside > .el-button span {
    display: none;
  }
}

@media (max-width: 620px) {
  .content {
    padding-left: 15px;
    padding-right: 15px;
  }
}
</style>
