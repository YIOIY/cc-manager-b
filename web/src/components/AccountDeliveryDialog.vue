<template>
  <el-dialog
    :model-value="modelValue"
    :title="state === 'linked' ? '账号详情' : state === 'submitting' ? '上号进度' : '账号上号'"
    width="600px"
    class="delivery-dialog"
    destroy-on-close
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template v-if="account">
      <div class="delivery-account">
        <span class="heading-icon"><UsersRound :size="22" /></span>
        <div>
          <strong>{{ shortPlatformAlias(account.platform_alias) || account.platform_alias || account.account }}</strong
          ><span>{{ account.account }}</span>
        </div>
        <span class="delivery-status" :class="presentation.tone">{{ presentation.label }}</span>
      </div>
      <div class="delivery-state-note" :class="presentation.tone">
        <component
          :is="state === 'linked' ? CheckCircle2 : state === 'submitting' ? LoaderCircle : Info"
          :size="18"
          :class="{ 'is-spinning': state === 'submitting' }"
        />
        <p>{{ presentation.description }}</p>
      </div>
      <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" />
      <dl class="delivery-facts">
        <div>
          <dt>账号套餐</dt>
          <dd>{{ planLabel(account.plan) || '未配置' }}</dd>
        </div>
        <div>
          <dt>套餐到期</dt>
          <dd>{{ account.plan_end_time || '未配置' }}</dd>
        </div>
      </dl>
      <template v-if="state === 'linked'">
        <section v-for="link in account.platform_links" :key="link.connection_id" class="delivery-linked-platform">
          <div class="delivery-section-title">
            <CheckCircle2 :size="16" /><strong>{{ link.connection_name || '已关联平台' }}</strong
            ><span>{{ runtimeLabel(link.status) }}</span>
          </div>
          <dl class="delivery-facts">
            <div>
              <dt>平台账号 ID</dt>
              <dd>{{ link.platform_account_id || '待平台返回' }}</dd>
            </div>
            <div>
              <dt>交付记录 ID</dt>
              <dd>{{ link.upstream_id }}</dd>
            </div>
          </dl>
        </section>
        <p class="delivery-footnote">账号已完成上号。指标或账单尚未同步，不影响已上号状态。</p>
      </template>
      <template v-else-if="state === 'submitting' || state === 'unknown'">
        <div class="delivery-task-info">
          <span>{{
            account.submission_job?.state === 'queued'
              ? '任务已排队，等待平台接收'
              : state === 'submitting'
                ? '正在向平台提交账号'
                : '等待核实平台接收结果'
          }}</span
          ><small v-if="account.submission_job?.id">任务编号 {{ account.submission_job.id }}</small>
        </div>
        <el-alert
          v-if="state === 'unknown'"
          :title="
            account.submission_job ? outcomeText(account.submission_job) : deliveryIssue('SUBMIT_OUTCOME_UNKNOWN')
          "
          type="warning"
          show-icon
          :closable="false"
        />
      </template>
      <template v-else>
        <el-alert
          v-if="state === 'failed'"
          :title="account.submission_job ? outcomeText(account.submission_job) : deliveryIssue('PLATFORM_REJECTED')"
          type="warning"
          show-icon
          :closable="false"
        />
        <el-form label-position="top" class="delivery-platform-form" @submit.prevent="submit"
          ><el-form-item label="上号平台" required
            ><el-select v-model="connectionId" :disabled="busy" placeholder="选择要提交的平台" aria-label="上号平台"
              ><el-option
                v-for="connection in connections"
                :key="connection.id"
                :label="connection.name || connection.base_url"
                :value="connection.id"
                :disabled="!connection.enabled" /></el-select
            ><span class="field-hint">{{
              enabledConnections.length === 1 ? '已选择唯一启用的平台连接。' : '提交前确认账号要上号的平台。'
            }}</span></el-form-item
          ></el-form
        >
        <el-alert
          v-if="!enabledConnections.length"
          title="尚无可用平台连接，请先到平台设置中添加或启用。"
          type="warning"
          :closable="false"
        />
        <div class="delivery-checks">
          <div class="delivery-section-title">
            <strong>提交前检查</strong
            ><el-button
              text
              type="primary"
              size="small"
              :disabled="!connectionId || busy"
              :loading="checking"
              @click="check"
              >重新检查</el-button
            >
          </div>
          <p v-if="checking" class="delivery-check-pending">正在检查账号资料、凭证和平台配置…</p>
          <p v-else-if="!connectionId" class="delivery-check-pending">选择平台后自动检查。</p>
          <p v-else-if="preflight?.can_submit" class="delivery-check-good">
            <CheckCircle2 :size="16" />资料与配置检查通过，可以提交上号。
          </p>
          <ul v-else-if="preflight">
            <li v-for="(item, index) in preflight.checks" :key="index">
              <AlertCircle :size="14" />{{ item.message || deliveryIssue(item.code) }}
            </li>
          </ul>
        </div>
      </template>
    </template>
    <template #footer
      ><div class="delivery-dialog-actions">
        <el-button
          v-if="(state === 'pending' || state === 'failed') && can('accounts.delete') && canMutateAccount"
          text
          type="danger"
          class="delivery-delete"
          :disabled="busy"
          @click="remove"
          >删除账号</el-button
        ><el-button :disabled="busy" @click="emit('update:modelValue', false)">{{
          state === 'linked' || state === 'submitting' || state === 'unknown' ? '关闭' : '取消'
        }}</el-button
        ><el-button v-if="state === 'submitting' || state === 'unknown'" :loading="busy" @click="emit('changed')"
          >刷新状态</el-button
        ><el-button
          v-if="!enabledConnections.length && state !== 'linked' && can('connections.create')"
          type="primary"
          @click="emit('configure')"
          >配置平台</el-button
        ><template
          v-else-if="(state === 'pending' || state === 'failed') && can('accounts.onboard') && canMutateAccount"
          ><el-button @click="account && emit('complete', account, connectionId)">完善资料</el-button
          ><el-button type="primary" :loading="busy" :disabled="checking || !preflight?.can_submit" @click="submit">{{
            busy ? '正在提交…' : '提交上号'
          }}</el-button></template
        >
      </div></template
    >
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UsersRound, CheckCircle2, AlertCircle, Info, LoaderCircle } from 'lucide-vue-next'
import { api, errorMessage } from '../api'
import { randomId } from '../utils/id'
import { useSession } from '../composables/useSession'
import type { Account, ConnectionOption, Preflight } from '../types/api'
import { planLabel } from '../utils/format'
import { shortPlatformAlias } from '../model/platform-card'
import { ElMessageBox } from 'element-plus'
import { deliveryState, deliveryStates, runtimeLabel, deliveryIssue, outcomeText } from '../account-workflow'
const props = defineProps<{ modelValue: boolean; account: Account | null; connections: ConnectionOption[] }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  changed: []
  configure: []
  complete: [account: Account, connectionId?: string]
  deleted: [accountId: string]
}>()
const { can, owns } = useSession()
const canMutateAccount = computed(() => owns(props.account?.created_by_user_id))
const connectionId = ref(''),
  preflight = ref<Preflight | null>(null),
  checking = ref(false),
  busy = ref(false),
  error = ref('')
const state = computed(() => deliveryState(props.account || {})),
  presentation = computed(() => deliveryStates[state.value]),
  enabledConnections = computed(() => props.connections.filter((c) => c.enabled))
let sequence = 0,
  key = ''
async function check() {
  const request = ++sequence
  preflight.value = null
  error.value = ''
  checking.value = false
  if (!connectionId.value || !props.account || !props.modelValue) return
  checking.value = true
  try {
    const result = await api<Preflight>('/accounts/' + props.account!.id + '/preflight', {
      method: 'POST',
      body: JSON.stringify({ platform_connection_id: connectionId.value }),
    })
    if (request === sequence) preflight.value = result.data
  } catch (e) {
    if (request === sequence) error.value = errorMessage(e)
  } finally {
    if (request === sequence) checking.value = false
  }
}
/** Removes an account the platform never accepted, with its local secrets. */
async function remove() {
  if (!props.account || busy.value) return
  const name = props.account.account
  try {
    // Typing the account name back is what stands between a slip and an
    // unrecoverable delete of the local password and credentials.
    await ElMessageBox.prompt(
      `本地保存的密码、Claude 凭证和上号记录会一并删除，无法恢复。请输入账号 ${name} 以确认。`,
      '删除账号',
      {
        type: 'warning',
        confirmButtonText: '确认删除',
        cancelButtonText: '取消',
        confirmButtonClass: 'el-button--danger',
        inputPlaceholder: name,
        inputValidator: (value: string) =>
          value.trim().toLowerCase() === name.trim().toLowerCase() || '输入的账号与要删除的账号不一致',
      },
    )
  } catch {
    return
  }
  busy.value = true
  error.value = ''
  try {
    await api('/accounts/' + props.account.id, { method: 'DELETE' })
    emit('deleted', props.account.id)
    emit('update:modelValue', false)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
async function submit() {
  if (busy.value || !preflight.value?.can_submit) return
  busy.value = true
  error.value = ''
  try {
    await api('/accounts/' + props.account!.id + '/submit', {
      method: 'POST',
      headers: { 'Idempotency-Key': key },
      body: JSON.stringify({ platform_connection_id: connectionId.value, expected_version: props.account!.version }),
    })
    preflight.value = null
    emit('changed')
  } catch (e) {
    error.value = errorMessage(e)
    emit('changed')
  } finally {
    busy.value = false
  }
}
watch(
  () => [props.modelValue, props.account?.id],
  () => {
    sequence++
    error.value = ''
    preflight.value = null
    checking.value = false
    key = randomId()
    if (props.modelValue) {
      const previous = props.account?.submission_job?.connection_id || ''
      connectionId.value = enabledConnections.value.some((c) => c.id === previous)
        ? previous
        : enabledConnections.value.length === 1
          ? enabledConnections.value[0]!.id
          : ''
    }
  },
  { immediate: true },
)
watch(connectionId, () => {
  key = randomId()
})
watch(
  () => [
    props.modelValue,
    props.account?.id,
    props.account?.version,
    props.account?.submission_job?.id,
    props.account?.submission_job?.state,
    connectionId.value,
    state.value,
  ],
  () => {
    if (props.modelValue && (state.value === 'pending' || state.value === 'failed')) {
      key = randomId()
      check()
    } else {
      sequence++
      checking.value = false
      preflight.value = null
    }
  },
  { immediate: true },
)
</script>

<style>
.delivery-dialog.el-dialog {
  width: min(600px, calc(100vw - 32px)) !important;
  max-height: calc(100dvh - 64px);
  margin: 32px auto !important;
  padding: 0;
  overflow: hidden;
  border-radius: 14px;
  display: flex;
  flex-direction: column;
}

.delivery-dialog .el-dialog__header {
  padding: 21px 24px 17px;
  margin: 0;
  border-bottom: 1px solid #edf1f1;
}

.delivery-dialog .el-dialog__title {
  color: #24434e;
  font-size: 17px;
  font-weight: 700;
}

.delivery-dialog .el-dialog__headerbtn {
  top: 5px;
  right: 7px;
}

.delivery-dialog .el-dialog__body {
  padding: 22px 24px;
  overflow-y: auto;
}

.delivery-dialog .el-dialog__footer {
  padding: 16px 24px;
  border-top: 1px solid #eaf0ef;
  background: #fcfdfd;
}

.delivery-account {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 18px;
  min-width: 0;
}

.delivery-account > div {
  min-width: 0;
  flex: 1;
}

.delivery-account strong {
  display: block;
  overflow-wrap: anywhere;
}

.delivery-account > div > span {
  display: block;
  overflow-wrap: anywhere;
}

.delivery-account strong {
  color: #284550;
  font-size: 14px;
  line-height: 1.5;
}

.delivery-account > div > span {
  margin-top: 4px;
  color: #87989f;
  font-size: 11px;
}

.delivery-state-note {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border-radius: 8px;
  background: #fff9ee;
  color: #a97d32;
}

.delivery-state-note > svg {
  flex: none;
  margin-top: 1px;
}

.delivery-state-note p {
  margin: 0;
  font-size: 12px;
  line-height: 1.65;
}

.delivery-state-note.success {
  background: #eff9f4;
  color: #44896d;
}

.delivery-state-note.progress {
  background: #f0f6fd;
  color: #4c83ad;
}

.delivery-state-note.danger {
  background: #fff3f1;
  color: #bf7163;
}

.delivery-dialog .el-alert {
  margin-top: 14px;
}

.delivery-facts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 15px;
  margin: 18px 0;
}

.delivery-facts > div {
  min-width: 0;
}

.delivery-facts dt {
  font-size: 11px;
  color: #8a9aa1;
  margin-bottom: 6px;
}

.delivery-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
  color: #3c5963;
  font-size: 12px;
  font-weight: 600;
}

.delivery-linked-platform {
  margin: 16px 0;
  padding: 15px;
  border: 1px solid #e3eeea;
  border-radius: 9px;
  background: #fcfefd;
}

.delivery-linked-platform .delivery-facts {
  margin-bottom: 0;
}

.delivery-section-title {
  display: flex;
  align-items: center;
  gap: 7px;
  color: #428a70;
  font-size: 12px;
}

.delivery-section-title > span {
  margin-left: auto;
  color: #84988e;
  font-size: 11px;
}

.delivery-section-title > .el-button {
  margin-left: auto;
}

.delivery-platform-form {
  margin-top: 22px;
}

.delivery-platform-form .el-form-item__label {
  color: #4c6872;
  font-weight: 600;
}

.delivery-platform-form .el-select {
  width: 100%;
}

.delivery-checks {
  padding: 13px 15px;
  border: 1px solid #e5edeb;
  border-radius: 9px;
  background: #fbfdfc;
}

.delivery-checks ul {
  display: grid;
  gap: 10px;
  margin: 10px 0 2px;
  padding: 0;
  list-style: none;
}

.delivery-checks li {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  color: #a47c40;
  font-size: 11px;
  line-height: 1.6;
}

.delivery-checks li svg {
  flex: none;
  margin-top: 2px;
}

.delivery-check-pending {
  color: #97a7ac;
  font-size: 11px;
}

.delivery-check-good {
  display: flex;
  gap: 7px;
  align-items: center;
  color: #31906c;
  font-size: 12px;
}

.delivery-task-info {
  padding: 17px 0;
  color: #587480;
  font-size: 12px;
  line-height: 1.8;
}

.delivery-task-info small {
  display: block;
  color: #9ba9b0;
  font-size: 10px;
  overflow-wrap: anywhere;
}

.delivery-footnote {
  color: #93a3a8;
  font-size: 11px;
  line-height: 1.7;
  margin: 16px 0 0;
}

.delivery-dialog-actions {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
}

.delivery-dialog-actions .el-button + .el-button {
  margin: 0;
}

@media (max-width: 620px) {
  .delivery-dialog .el-dialog__body {
    padding: 18px;
  }
  .delivery-dialog .el-dialog__header {
    padding: 19px 18px 15px;
  }
  .delivery-dialog .el-dialog__footer {
    padding: 14px 18px;
  }
  .delivery-account {
    flex-wrap: wrap;
  }
  .delivery-facts {
    gap: 13px;
  }
  .delivery-dialog-actions .el-button {
    flex: 1;
  }
}
.delivery-delete {
  margin-right: auto;
}
</style>
