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
  background: #f5f6f8;
}

.shell > aside {
  width: 220px;
  background: #232429;
  color: rgba(255, 255, 255, 0.65);
  padding: 20px 12px 16px;
  display: flex;
  flex-direction: column;
  position: relative;
  overflow: hidden;
  border-right: 1px solid rgba(255, 255, 255, 0.06);
}

.shell > aside:after {
  display: none;
}

.nav-caption {
  color: rgba(255, 255, 255, 0.35);
  text-transform: none;
  letter-spacing: 0.04em;
  font-size: 12px;
  font-weight: 500;
  padding: 4px 12px 12px;
  position: relative;
  z-index: 1;
}

.nav-mark {
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 3px;
  height: 16px;
  border-radius: 0 2px 2px 0;
  background: #0077fa;
  box-shadow: none;
}

.shell-user {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: auto 4px 6px;
  padding: 10px;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.06);
  color: rgba(255, 255, 255, 0.65);
  position: relative;
  z-index: 1;
}
.shell-user-avatar {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  flex: none;
  border-radius: 50%;
  background: #0077fa;
  color: #fff;
  font-weight: 700;
  font-size: 12px;
}
.shell-user > div {
  min-width: 0;
  flex: 1;
  display: grid;
  line-height: 1.3;
}
.shell-user strong {
  color: #fff;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.shell-user small {
  font-size: 11px;
  color: rgba(255, 255, 255, 0.4);
}
.shell-user .el-button {
  color: rgba(255, 255, 255, 0.55);
  padding: 6px;
}
.shell > aside > .el-button {
  margin: 0 4px;
  color: rgba(255, 255, 255, 0.55);
  justify-content: flex-start;
  padding: 10px 12px;
  position: relative;
  z-index: 1;
  display: flex;
  gap: 8px;
  align-items: center;
  border-radius: 6px;
}

.shell > aside > .el-button:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.08);
}

.shell main {
  flex: 1;
  min-width: 0;
  background: #f5f6f8;
}

.shell > main > header {
  height: 56px;
  background: #fff;
  backdrop-filter: none;
  border-bottom: 1px solid #e5e6e8;
  padding: 0 24px;
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
  content: none;
}

.content {
  padding: 20px 24px 40px;
  max-width: 1440px;
  margin: 0 auto;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.header-announcement {
  flex: 1;
  min-width: 0;
  margin: 0 16px;
  color: #1c1f23;
  font-size: 13px;
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
  padding: 6px 12px;
  border: 1px solid #e5e6e8;
  border-radius: 6px;
  background: #fff;
  color: #4e5257;
  font: inherit;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
}

.header-feedback:hover,
.header-feedback:focus-visible {
  border-color: #0077fa;
  color: #0077fa;
  background: #eaf5ff;
  outline: none;
}

.nav-badge {
  margin-left: auto;
  min-width: 18px;
  padding: 1px 6px;
  border-radius: 10px;
  background: #fc8800;
  color: #fff;
  font:
    600 11px/1.5 Inter,
    system-ui,
    sans-serif;
  font-style: normal;
  text-align: center;
}

.status-dot {
  font-size: 12px;
  font-weight: 500;
  color: #3bb346;
  background: rgba(59, 179, 70, 0.1);
  border: none;
  border-radius: 6px;
  padding: 6px 10px;
}

.status-dot:before {
  content: '';
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #3bb346;
  margin-right: 6px;
  vertical-align: 1px;
}

@media (max-width: 980px) {
  .nav-caption {
    display: none;
  }
  .shell > aside {
    width: 64px;
    padding: 16px 8px;
  }
  .shell > aside nav a {
    justify-content: center;
    font-size: 0;
    padding: 12px;
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
    padding-left: 16px;
    padding-right: 16px;
  }
  .shell > main > header {
    padding-left: 16px;
    padding-right: 16px;
  }
}

@media (max-width: 620px) {
  .content {
    padding: 16px 12px 32px;
  }
  .shell > main > header {
    padding: 0 12px;
  }
}

.shell > aside nav button {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 12px 10px 14px;
  color: rgba(255, 255, 255, 0.65);
  border: none;
  border-radius: 6px;
  cursor: pointer;
  background: transparent;
  font-size: 13px;
  font-weight: 500;
  text-align: left;
  position: relative;
}

.shell > aside nav button svg {
  color: rgba(255, 255, 255, 0.45);
}

.shell > aside nav button.active {
  background: rgba(0, 119, 250, 0.18);
  color: #fff;
}

.shell > aside nav button:hover {
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
}

.shell > aside nav button.active svg {
  color: #54a9ff;
}

.shell > aside nav button:hover svg {
  color: #fff;
}

.shell > aside nav button:focus-visible {
  outline: 2px solid #54a9ff;
  outline-offset: 1px;
}

.breadcrumb {
  font-size: 13px;
  line-height: 1.5;
  color: #6b7075;
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

.breadcrumb span {
  margin: 0 8px;
  color: #c8c9cc;
}

.breadcrumb strong {
  color: #1c1f23;
  font-weight: 600;
}

@media (max-width: 980px) {
  .shell > aside nav button {
    justify-content: center;
    font-size: 0;
    padding: 12px;
  }
  .shell > aside > .el-button span {
    display: none;
  }
}

@media (max-width: 620px) {
  .content {
    padding-left: 12px;
    padding-right: 12px;
  }
}
</style>
