<template>
  <PageHead
    eyebrow="HELP CENTER"
    title="帮助文档"
    description="操作手册与常见问题。页面上的提示看不懂时，先在这里搜一下。"
  />

  <!-- Home: search, the onboarding route, the manual and the questions. -->
  <div v-if="!current" class="help-home">
    <section class="help-search">
      <label for="help-search-input">想找什么？</label>
      <el-input
        id="help-search-input"
        v-model="query"
        size="large"
        clearable
        placeholder="输入页面上看到的提示或关键字，例如：未获授权、设备标识"
        aria-label="搜索帮助文档"
      />
      <div class="help-suggest">
        <span>常搜</span>
        <button v-for="word in helpSuggestions" :key="word" type="button" data-suggest @click="query = word">
          {{ word }}
        </button>
      </div>
    </section>

    <section v-if="searching" class="help-results" aria-live="polite">
      <h3>找到 {{ hits.length }} 条</h3>
      <button
        v-for="hit in hits"
        :key="hit.topic.id"
        type="button"
        class="help-hit"
        :data-hit="hit.topic.id"
        @click="open(hit.topic.id)"
      >
        <span class="help-hit-group">{{ groupLabel(hit.topic.group) }}</span>
        <strong>{{ hit.topic.title }}</strong>
        <span class="help-hit-text"
          >{{ hit.before }}<mark v-if="hit.match">{{ hit.match }}</mark
          >{{ hit.after }}</span
        >
      </button>
      <p v-if="!hits.length" class="help-empty">没有找到相关内容。换个关键字试试，或者直接看下面的操作手册。</p>
    </section>

    <template v-else>
      <section class="help-route">
        <h3>上号路线</h3>
        <p class="help-section-sub">一个账号从准备到上号成功要走的五步，点任意一步看说明。</p>
        <ol>
          <li v-for="(node, i) in helpPath" :key="node.title">
            <button type="button" data-path @click="open(node.topic)">
              <i>{{ i + 1 }}</i>
              <strong>{{ node.title }}</strong>
              <span>{{ node.text }}</span>
            </button>
          </li>
        </ol>
      </section>

      <div class="help-columns">
        <section>
          <h3>操作手册</h3>
          <p class="help-section-sub">按页面讲清楚每一步怎么做。</p>
          <div class="help-cards">
            <button
              v-for="topic in manual"
              :key="topic.id"
              type="button"
              class="help-card"
              :data-card="topic.id"
              @click="open(topic.id)"
            >
              <span class="help-card-icon"><component :is="iconOf(topic.icon)" :size="18" /></span>
              <strong>{{ topic.title }}</strong>
              <span>{{ topic.summary }}</span>
            </button>
          </div>
        </section>
        <section>
          <h3>常见问题</h3>
          <p class="help-section-sub">按你遇到问题的地方分组。</p>
          <div v-for="area in areas" :key="area.key" class="help-area">
            <h4>{{ area.label }}</h4>
            <button
              v-for="topic in area.topics"
              :key="topic.id"
              type="button"
              class="help-question"
              :data-question="topic.id"
              @click="open(topic.id)"
            >
              <span>{{ topic.title }}</span
              ><ChevronRight :size="15" />
            </button>
          </div>
        </section>
      </div>
    </template>
  </div>

  <!-- Article: contents on the left, one topic on the right. -->
  <div v-else class="help-layout">
    <aside class="help-nav" aria-label="文档目录">
      <button type="button" class="help-back" data-home @click="home"><ChevronLeft :size="15" />帮助首页</button>
      <el-input v-model="query" placeholder="筛选目录" clearable aria-label="筛选目录" />
      <template v-for="group in groups" :key="group.key">
        <div v-if="group.topics.length" class="help-group">
          <div class="help-group-title">
            {{ group.label }}<span>{{ group.topics.length }}</span>
          </div>
          <button
            v-for="topic in group.topics"
            :key="topic.id"
            type="button"
            class="help-topic-link"
            :class="{ active: topic.id === current.id }"
            :data-topic="topic.id"
            :aria-current="topic.id === current.id ? 'true' : undefined"
            @click="open(topic.id)"
          >
            {{ topic.title }}
          </button>
        </div>
      </template>
      <p v-if="!matched.length" class="help-empty">没有找到相关内容，换个关键字试试。</p>
    </aside>

    <article class="help-article" :data-current-topic="current.id">
      <div class="help-crumb">{{ groupLabel(current.group) }}</div>
      <h2>{{ current.title }}</h2>
      <p class="help-lead">{{ current.summary }}</p>

      <template v-for="(block, index) in body" :key="current.id + ':' + index">
        <p v-if="block.type === 'p'"><HelpRichText :text="block.text" /></p>
        <ol v-else-if="block.type === 'steps'" class="help-steps">
          <li v-for="item in block.items" :key="item"><HelpRichText :text="item" /></li>
        </ol>
        <ul v-else-if="block.type === 'list'" class="help-list">
          <li v-for="item in block.items" :key="item"><HelpRichText :text="item" /></li>
        </ul>
        <div v-else-if="block.type === 'note'" class="help-note" :class="'tone-' + block.tone" role="note">
          <component :is="block.tone === 'warn' ? TriangleAlert : Info" :size="16" /><span
            ><HelpRichText :text="block.text"
          /></span>
        </div>
        <ol v-else-if="block.type === 'flow'" class="help-flow">
          <li v-for="(step, i) in block.steps" :key="step.title" data-flow-step>
            <i>{{ i + 1 }}</i>
            <strong>{{ step.title }}</strong>
            <span>{{ step.text }}</span>
          </li>
        </ol>
        <div v-else-if="block.type === 'table'" class="help-table">
          <table>
            <thead>
              <tr>
                <th v-for="cell in block.head" :key="cell">{{ cell }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in block.rows" :key="row[0]">
                <td v-for="(cell, c) in row" :key="c"><HelpRichText :text="cell" /></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else-if="block.type === 'code'" class="help-code" data-code>
          <div class="help-code-head">
            <span>{{ block.label || '命令' }}</span>
            <el-button text size="small" @click="copy(index, block.text)">
              <Copy :size="13" />{{
                copiedIndex === index ? '已复制' : copyFailedIndex === index ? '请手动复制' : '复制'
              }}
            </el-button>
          </div>
          <pre><code>{{ block.text }}</code></pre>
        </div>
      </template>

      <section v-if="related.length" class="help-related">
        <h3>相关主题</h3>
        <div class="help-related-list">
          <button
            v-for="link in related"
            :key="link.topic"
            type="button"
            :data-link="link.topic"
            @click="open(link.topic)"
          >
            <strong>{{ link.text }}</strong>
            <span>{{ link.summary }}</span>
          </button>
        </div>
      </section>

      <div class="help-pager" role="navigation" aria-label="上一篇和下一篇">
        <button v-if="around.previous" type="button" data-previous @click="open(around.previous.id)">
          <small>上一篇</small><span><ChevronLeft :size="15" />{{ around.previous.title }}</span>
        </button>
        <span v-else></span>
        <button v-if="around.next" type="button" class="next" data-next @click="open(around.next.id)">
          <small>下一篇</small><span>{{ around.next.title }}<ChevronRight :size="15" /></span>
        </button>
      </div>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  BookOpen,
  ChevronLeft,
  ChevronRight,
  ClipboardList,
  Copy,
  Fingerprint,
  Info,
  LayoutDashboard,
  ListChecks,
  MessagesSquare,
  PencilLine,
  SlidersHorizontal,
  TriangleAlert,
  UserPlus,
} from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import HelpRichText from '../components/HelpRichText.vue'
import {
  helpAreas,
  helpGroups,
  helpPath,
  helpSuggestions,
  helpTopics,
  neighbours,
  searchHits,
  searchTopics,
  type HelpGroupKey,
} from '../help/content'
import { copyText } from '../utils/clipboard'

const route = useRoute()
const router = useRouter()
const query = ref('')
const copiedIndex = ref(-1)
const copyFailedIndex = ref(-1)

// The address decides what is shown: no topic is the home, a topic is its
// article. Topics can be linked to and the back button retraces the reading.
const current = computed(() => helpTopics.find((t) => t.id === String(route.query.topic || '')) ?? null)

const searching = computed(() => query.value.trim() !== '')
const hits = computed(() => searchHits(helpTopics, query.value))
const matched = computed(() => searchTopics(helpTopics, query.value))
const groups = computed(() => helpGroups.map((g) => ({ ...g, topics: matched.value.filter((t) => t.group === g.key) })))
const manual = helpTopics.filter((t) => t.group === 'manual')
const areas = helpAreas.map((a) => ({ ...a, topics: helpTopics.filter((t) => t.area === a.key) }))
const groupLabel = (key: HelpGroupKey) => helpGroups.find((g) => g.key === key)?.label ?? ''

const icons: Record<string, Component> = {
  checklist: ClipboardList,
  onboard: UserPlus,
  device: Fingerprint,
  result: ListChecks,
  edit: PencilLine,
  settings: SlidersHorizontal,
  dashboard: LayoutDashboard,
  feedback: MessagesSquare,
}
const iconOf = (name?: string) => icons[name || ''] ?? BookOpen

// Links to other topics are gathered at the end of the article instead of
// being scattered through it.
const body = computed(() => (current.value?.blocks || []).filter((b) => b.type !== 'link'))
const related = computed(() =>
  (current.value?.blocks || []).flatMap((b) =>
    b.type === 'link'
      ? [{ topic: b.topic, text: b.text, summary: helpTopics.find((t) => t.id === b.topic)?.summary ?? '' }]
      : [],
  ),
)
const around = computed(() => neighbours(current.value?.id ?? ''))

function open(id: string) {
  query.value = ''
  router.push({ name: 'help', query: { topic: id } })
}
function home() {
  query.value = ''
  router.push({ name: 'help', query: {} })
}
async function copy(index: number, text: string) {
  const ok = await copyText(text)
  copiedIndex.value = ok ? index : -1
  copyFailedIndex.value = ok ? -1 : index
}
watch(
  () => current.value?.id,
  () => {
    copiedIndex.value = -1
    copyFailedIndex.value = -1
    if (typeof window !== 'undefined' && typeof window.scrollTo === 'function') window.scrollTo({ top: 0 })
  },
)
</script>

<style scoped>
button {
  font: inherit;
  cursor: pointer;
}
h3 {
  margin: 0;
  color: #213d4b;
  font-size: 17px;
}
.help-section-sub {
  margin: 4px 0 14px;
  color: var(--muted-soft);
  font-size: 12.5px;
}
.help-empty {
  margin: 14px 2px 4px;
  color: var(--muted-strong);
  font-size: 13px;
}

/* ---------- home ---------- */
.help-home > section,
.help-columns > section {
  background: var(--surface);
  border: 1px solid var(--line-tint);
  border-radius: 14px;
  padding: 22px 24px;
}
.help-home > * + * {
  margin-top: 16px;
}
.help-search label {
  display: block;
  margin-bottom: 10px;
  color: #213d4b;
  font-size: 17px;
  font-weight: 700;
}
.help-search :deep(.el-input) {
  max-width: 720px;
}
.help-suggest {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  color: var(--muted-soft);
  font-size: 12px;
}
.help-suggest button {
  padding: 3px 11px;
  border: 1px solid var(--line-tint);
  border-radius: 99px;
  background: var(--surface-soft);
  color: var(--ink-mid);
  font-size: 12px;
}
.help-suggest button:hover,
.help-suggest button:focus-visible {
  border-color: var(--accent);
  color: var(--accent-deep);
  outline: none;
}

.help-hit {
  display: grid;
  grid-template-columns: 76px minmax(0, 1fr);
  gap: 2px 12px;
  width: 100%;
  margin-top: 4px;
  padding: 12px 10px;
  border: 0;
  border-top: 1px solid var(--line-soft);
  background: transparent;
  text-align: left;
}
.help-results h3 + .help-hit {
  margin-top: 12px;
}
.help-hit:hover,
.help-hit:focus-visible {
  background: #f4faf8;
  outline: none;
}
.help-hit-group {
  grid-row: 1 / span 2;
  align-self: start;
  margin-top: 2px;
  color: var(--accent-strong);
  font-size: 12px;
}
.help-hit strong {
  color: var(--ink-soft);
  font-size: 14px;
}
.help-hit-text {
  color: var(--muted-strong);
  font-size: 12.5px;
  line-height: 1.6;
}
.help-hit mark {
  padding: 0 2px;
  border-radius: 3px;
  background: #fbe9a8;
  color: inherit;
}

/* The route is a real sequence, so the stops are numbered and joined by a line. */
.help-route ol {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  margin: 6px 0 0;
  padding: 0;
  list-style: none;
}
.help-route li {
  position: relative;
}
.help-route li + li::before {
  content: '';
  position: absolute;
  top: 25px;
  left: calc(-50% + 22px);
  right: calc(50% + 22px);
  height: 2px;
  background: #cfe6df;
}
.help-route button {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
  width: 100%;
  padding: 8px 8px 10px;
  border: 0;
  border-radius: 12px;
  background: transparent;
  text-align: center;
}
.help-route button:hover,
.help-route button:focus-visible {
  background: #f4faf8;
  outline: none;
}
.help-route i {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border: 2px solid var(--accent);
  border-radius: 50%;
  background: var(--surface);
  color: var(--accent-deep);
  font:
    700 14px/1 'DM Mono',
    ui-monospace,
    monospace;
  font-style: normal;
  position: relative;
  z-index: 1;
}
.help-route button:hover i {
  background: var(--accent);
  color: #fff;
}
.help-route strong {
  margin-top: 4px;
  color: var(--ink-soft);
  font-size: 14px;
}
.help-route span {
  color: var(--muted-strong);
  font-size: 12px;
  line-height: 1.5;
}

.help-columns {
  display: grid;
  grid-template-columns: 1.15fr 0.85fr;
  gap: 16px;
  align-items: start;
}
.help-cards {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.help-card {
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr);
  gap: 2px 12px;
  padding: 14px;
  border: 1px solid var(--line-soft);
  border-radius: 12px;
  background: var(--surface-soft);
  text-align: left;
}
.help-card:hover,
.help-card:focus-visible {
  border-color: var(--accent);
  background: #f4faf8;
  outline: none;
}
.help-card-icon {
  grid-row: 1 / span 2;
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  background: #e3f3ef;
  color: var(--accent-deep);
}
.help-card strong {
  color: var(--ink-soft);
  font-size: 14px;
}
.help-card > span:last-child {
  color: var(--muted-strong);
  font-size: 12px;
  line-height: 1.55;
}
.help-area + .help-area {
  margin-top: 14px;
}
.help-area h4 {
  margin: 0 0 4px;
  color: var(--accent-strong);
  font-size: 12.5px;
}
.help-question {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
  padding: 8px 6px;
  border: 0;
  border-bottom: 1px solid var(--line-soft);
  background: transparent;
  color: var(--ink-mid);
  font-size: 13px;
  line-height: 1.5;
  text-align: left;
}
.help-question svg {
  flex: none;
  color: var(--muted-soft);
}
.help-question:hover,
.help-question:focus-visible {
  background: #f4faf8;
  color: var(--accent-deep);
  outline: none;
}

/* ---------- article ---------- */
.help-layout {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  gap: 18px;
  align-items: start;
}
.help-nav,
.help-article {
  background: var(--surface);
  border: 1px solid var(--line-tint);
  border-radius: 14px;
}
.help-nav {
  position: sticky;
  top: 16px;
  max-height: calc(100vh - 150px);
  overflow-y: auto;
  padding: 14px;
}
.help-back {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  margin: 0 0 10px;
  padding: 4px 8px 4px 2px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--accent-strong);
  font-size: 13px;
  font-weight: 600;
}
.help-back:hover {
  background: #f4faf8;
}
.help-group {
  margin-top: 16px;
}
.help-group-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 6px 6px;
  color: var(--accent-strong);
  font-size: 12px;
  font-weight: 800;
}
.help-group-title span {
  padding: 1px 7px;
  border-radius: 99px;
  background: #eef6f4;
  color: #5b7a74;
  font-weight: 600;
  font-size: 11px;
}
.help-topic-link {
  display: block;
  width: 100%;
  padding: 7px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #3d525c;
  font-size: 13px;
  line-height: 1.45;
  text-align: left;
}
.help-topic-link:hover {
  background: #f3f8f7;
}
.help-topic-link.active {
  background: #e3f3ef;
  color: var(--accent-deep);
  font-weight: 600;
}
.help-article {
  min-width: 0;
  padding: 30px 38px 28px;
}
.help-crumb {
  color: var(--accent-strong);
  font-size: 12.5px;
  font-weight: 700;
}
.help-article h2 {
  margin: 6px 0 8px;
  color: #17313d;
  font-size: 25px;
  line-height: 1.3;
  letter-spacing: -0.02em;
}
.help-lead {
  max-width: 720px;
  margin: 0 0 22px;
  padding-bottom: 18px;
  border-bottom: 1px solid var(--line-soft);
  color: var(--muted-strong);
  font-size: 15px;
  line-height: 1.7;
}
.help-article p,
.help-list li,
.help-steps li {
  max-width: 720px;
  color: #33474f;
  font-size: 14.5px;
  line-height: 1.85;
}
.help-article p {
  margin: 0 0 14px;
}
.help-list {
  margin: 0 0 16px;
  padding-left: 20px;
}
.help-list li {
  margin-bottom: 6px;
}
/* Steps are a timeline: a numbered marker per step, joined by a rule. */
.help-steps {
  max-width: 720px;
  margin: 4px 0 18px;
  padding: 0;
  list-style: none;
  counter-reset: step;
}
.help-steps li {
  position: relative;
  margin: 0;
  padding: 0 0 16px 44px;
  counter-increment: step;
}
.help-steps li::before {
  content: counter(step);
  position: absolute;
  left: 0;
  top: 1px;
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--accent-deep);
  color: #fff;
  font:
    700 12px/1 'DM Mono',
    ui-monospace,
    monospace;
}
.help-steps li::after {
  content: '';
  position: absolute;
  left: 12.5px;
  top: 31px;
  bottom: 2px;
  width: 1.5px;
  background: #cfe6df;
}
.help-steps li:last-child::after {
  display: none;
}
.help-flow {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: 1fr;
  gap: 22px;
  max-width: 720px;
  margin: 4px 0 20px;
  padding: 0;
  list-style: none;
}
.help-flow li {
  position: relative;
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr);
  gap: 2px 10px;
  padding: 12px 14px;
  border: 1px solid #cfe6df;
  border-radius: 11px;
  background: #f6fbf9;
}
.help-flow li + li::before {
  content: '';
  position: absolute;
  left: -16px;
  top: 50%;
  width: 8px;
  height: 8px;
  border-top: 2px solid #9fcfc2;
  border-right: 2px solid #9fcfc2;
  transform: translateY(-50%) rotate(45deg);
}
.help-flow i {
  grid-row: 1 / span 2;
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--accent-deep);
  color: #fff;
  font:
    700 12px/1 'DM Mono',
    ui-monospace,
    monospace;
  font-style: normal;
}
.help-flow strong {
  color: var(--ink-soft);
  font-size: 14px;
}
.help-flow span {
  color: var(--muted-strong);
  font-size: 12px;
  line-height: 1.5;
}
.help-table {
  max-width: 820px;
  margin: 0 0 18px;
  border: 1px solid var(--line-tint);
  border-radius: 11px;
  overflow-x: auto;
}
.help-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13.5px;
}
.help-table th {
  padding: 9px 14px;
  background: #f5f9f9;
  color: var(--muted-strong);
  font-size: 12px;
  font-weight: 700;
  text-align: left;
  white-space: nowrap;
}
.help-table td {
  padding: 10px 14px;
  border-top: 1px solid var(--line-soft);
  color: #33474f;
  line-height: 1.7;
  vertical-align: top;
}
.help-table td:first-child {
  color: var(--ink-soft);
  font-weight: 700;
  white-space: nowrap;
}
.help-note {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  max-width: 720px;
  margin: 0 0 16px;
  padding: 12px 14px;
  border-radius: 10px;
  font-size: 13.5px;
  line-height: 1.75;
}
.help-note svg {
  flex: none;
  margin-top: 4px;
}
.help-note.tone-info {
  background: #f1f8f6;
  border: 1px solid #d5ebe5;
  color: #2f5f55;
}
.help-note.tone-warn {
  background: #fdf6ea;
  border: 1px solid #f1dfbd;
  color: #8a5a14;
}
.help-code {
  max-width: 720px;
  margin: 0 0 16px;
  border: 1px solid #dfe8e8;
  border-radius: 10px;
  overflow: hidden;
}
.help-code-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 5px 6px 5px 13px;
  background: #f5f9f9;
  border-bottom: 1px solid #e6eeee;
  color: #5f747d;
  font-size: 12px;
}
.help-code-head :deep(.el-button) {
  gap: 4px;
}
.help-code pre {
  margin: 0;
  padding: 12px 13px;
  background: #fbfdfd;
  overflow-x: auto;
}
.help-code code {
  color: #24414d;
  font:
    12.5px/1.6 'DM Mono',
    ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
  white-space: pre;
}
.help-related {
  max-width: 820px;
  margin-top: 26px;
  padding-top: 20px;
  border-top: 1px solid var(--line-soft);
}
.help-related h3 {
  font-size: 14px;
}
.help-related-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 10px;
  margin-top: 10px;
}
.help-related-list button {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 12px 14px;
  border: 1px solid var(--line-soft);
  border-radius: 11px;
  background: var(--surface-soft);
  text-align: left;
}
.help-related-list button:hover,
.help-related-list button:focus-visible {
  border-color: var(--accent);
  background: #f4faf8;
  outline: none;
}
.help-related-list strong {
  color: var(--accent-deep);
  font-size: 13.5px;
}
.help-related-list span {
  color: var(--muted-strong);
  font-size: 12px;
  line-height: 1.55;
}
.help-pager {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  max-width: 820px;
  margin-top: 22px;
}
.help-pager button {
  display: flex;
  flex-direction: column;
  gap: 3px;
  max-width: 48%;
  padding: 9px 12px;
  border: 0;
  border-radius: 9px;
  background: transparent;
  text-align: left;
}
.help-pager button.next {
  align-items: flex-end;
  text-align: right;
}
.help-pager button:hover,
.help-pager button:focus-visible {
  background: #f4faf8;
  outline: none;
}
.help-pager small {
  color: var(--muted-soft);
  font-size: 11.5px;
}
.help-pager span {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--accent-deep);
  font-size: 13.5px;
  font-weight: 600;
}

@media (max-width: 1000px) {
  .help-columns,
  .help-layout {
    grid-template-columns: 1fr;
  }
  .help-nav {
    position: static;
    max-height: none;
  }
  .help-article {
    padding: 22px 20px;
  }
}
@media (max-width: 760px) {
  .help-route ol {
    grid-template-columns: 1fr;
    gap: 2px;
  }
  .help-route li + li::before {
    display: none;
  }
  .help-route button {
    flex-direction: row;
    flex-wrap: wrap;
    align-items: center;
    text-align: left;
  }
  .help-route span {
    flex-basis: 100%;
    padding-left: 44px;
  }
  .help-cards {
    grid-template-columns: 1fr;
  }
  .help-flow {
    grid-auto-flow: row;
  }
  .help-flow li + li::before {
    display: none;
  }
}
</style>
