<template>
  <PageHead
    eyebrow="HELP CENTER"
    title="帮助文档"
    description="操作手册与常见问题。遇到页面上的提示看不懂时，先在这里搜一下。"
  />
  <div class="help-layout">
    <aside class="help-nav" aria-label="文档目录">
      <el-input v-model="query" placeholder="搜索：如 设备标识、403、复制" clearable aria-label="搜索帮助文档" />
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
      <div class="help-kicker">{{ groupLabel(current.group) }}</div>
      <h2>{{ current.title }}</h2>
      <template v-for="(block, index) in current.blocks" :key="current.id + ':' + index">
        <p v-if="block.type === 'p'">{{ block.text }}</p>
        <ol v-else-if="block.type === 'steps'" class="help-steps">
          <li v-for="item in block.items" :key="item">{{ item }}</li>
        </ol>
        <ul v-else-if="block.type === 'list'" class="help-list">
          <li v-for="item in block.items" :key="item">{{ item }}</li>
        </ul>
        <div v-else-if="block.type === 'note'" class="help-note" :class="'tone-' + block.tone" role="note">
          <component :is="block.tone === 'warn' ? TriangleAlert : Info" :size="16" /><span>{{ block.text }}</span>
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
        <button
          v-else-if="block.type === 'link'"
          type="button"
          class="help-jump"
          :data-link="block.topic"
          @click="open(block.topic)"
        >
          <ArrowRight :size="14" />{{ block.text }}
        </button>
      </template>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowRight, Copy, Info, TriangleAlert } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import { helpGroups, helpTopics, searchTopics, type HelpGroupKey } from '../help/content'
import { copyText } from '../utils/clipboard'

const route = useRoute()
const router = useRouter()
const query = ref('')
const copiedIndex = ref(-1)
const copyFailedIndex = ref(-1)

// The address decides the topic, so a topic can be linked to and the
// browser's back button walks through the topics that were read.
const current = computed(() => {
  const id = String(route.query.topic || '')
  return helpTopics.find((t) => t.id === id) ?? helpTopics[0]!
})
const matched = computed(() => searchTopics(helpTopics, query.value))
const groups = computed(() => helpGroups.map((g) => ({ ...g, topics: matched.value.filter((t) => t.group === g.key) })))
const groupLabel = (key: HelpGroupKey) => helpGroups.find((g) => g.key === key)?.label ?? ''

function open(id: string) {
  router.replace({ name: 'help', query: { topic: id } })
}
async function copy(index: number, text: string) {
  const ok = await copyText(text)
  copiedIndex.value = ok ? index : -1
  copyFailedIndex.value = ok ? -1 : index
}
watch(
  () => current.value.id,
  () => {
    copiedIndex.value = -1
    copyFailedIndex.value = -1
  },
)
</script>

<style scoped>
.help-layout {
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr);
  gap: 18px;
  align-items: start;
}
.help-nav,
.help-article {
  background: var(--surface);
  border: 1px solid var(--line-tint);
  border-radius: 14px;
  box-shadow: 0 8px 24px rgba(20, 35, 40, 0.045);
}
.help-nav {
  position: sticky;
  top: 16px;
  max-height: calc(100vh - 150px);
  overflow-y: auto;
  padding: 16px 14px;
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
  letter-spacing: 0.04em;
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
  padding: 8px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #3d525c;
  font: inherit;
  font-size: 13px;
  line-height: 1.45;
  text-align: left;
  cursor: pointer;
}
.help-topic-link:hover {
  background: #f3f8f7;
}
.help-topic-link.active {
  background: #e3f3ef;
  color: var(--accent-deep);
  font-weight: 600;
}
.help-empty {
  margin: 18px 6px 4px;
  color: var(--muted-soft);
  font-size: 12px;
}
.help-article {
  min-width: 0;
  padding: 26px 30px 30px;
}
.help-kicker {
  color: var(--accent-strong);
  font-size: 12px;
  font-weight: 700;
}
.help-article h2 {
  margin: 6px 0 16px;
  color: #213d4b;
  font-size: 21px;
  letter-spacing: -0.02em;
}
.help-article p,
.help-steps li,
.help-list li {
  max-width: 760px;
  color: #3d525c;
  font-size: 14px;
  line-height: 1.8;
}
.help-article p {
  margin: 0 0 12px;
}
.help-steps,
.help-list {
  margin: 0 0 14px;
  padding-left: 22px;
}
.help-steps li,
.help-list li {
  margin-bottom: 6px;
}
.help-note {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  max-width: 760px;
  margin: 0 0 14px;
  padding: 11px 13px;
  border-radius: 10px;
  font-size: 13px;
  line-height: 1.7;
}
.help-note svg {
  flex: none;
  margin-top: 3px;
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
  max-width: 760px;
  margin: 0 0 14px;
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
.help-jump {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 8px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--el-color-primary);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
.help-jump:hover {
  text-decoration: underline;
}
@media (max-width: 900px) {
  .help-layout {
    grid-template-columns: 1fr;
  }
  .help-nav {
    position: static;
    max-height: none;
  }
  .help-article {
    padding: 20px 18px 22px;
  }
}
</style>
