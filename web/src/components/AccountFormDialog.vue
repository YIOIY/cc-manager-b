<template>
  <el-dialog
    :model-value="modelValue"
    width="760px"
    class="create-dialog account-create-dialog account-wizard"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    :before-close="beforeClose"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="account-dialog-heading">
        <h2>{{ forceReauth ? '重新授权' : edit.id ? '完善资料并上号' : '添加 Claude 账号' }}</h2>
        <a class="dialog-help-link" href="/help?topic=onboard" target="_blank" rel="noopener noreferrer"
          ><BookOpen :size="13" />上号手册</a
        >
      </div>
      <p v-if="forceReauth" class="account-dialog-sub">
        {{
          savedCredentials ? '可直接用已保存凭证同步到平台，也可重新授权。' : '重新登录 Claude，更新凭证并同步到平台。'
        }}
      </p>
      <ol class="wizard-steps" aria-label="上号步骤">
        <li
          v-for="(label, index) in stepLabels"
          :key="label"
          :aria-current="displayStep === index + 1 ? 'step' : undefined"
          :class="{ 'is-complete': displayStep > index + 1 }"
        >
          <span
            ><CheckCircle2 v-if="displayStep > index + 1" :size="17" /><template v-else>{{ index + 1 }}</template></span
          >{{ label }}
        </li>
      </ol>
    </template>
    <ErrorNotice :error="notice" compact closable @close="notice = null" />
    <el-form label-position="top" class="account-form" @submit.prevent="step < 3 ? advance() : save()">
      <section v-show="step === 1" class="wizard-page" aria-label="连接账号">
        <el-form-item :id="fieldAnchors.connection" label="上号到" required :error="fieldErrors.platform_connection_id">
          <el-select
            v-model="edit.platform_connection_id"
            clearable
            filterable
            :disabled="busy || linkedReauth"
            placeholder="选择上号平台"
            aria-label="上号平台"
            ><el-option
              v-for="c in connections"
              :key="c.id"
              :label="c.name || c.base_url"
              :value="c.id"
              :disabled="!c.enabled"
          /></el-select>
          <div class="field-hint">
            {{
              linkedReauth
                ? `凭证将更新到已绑定平台「${linkedConnectionName}」，不可更换。`
                : enabledConnections.length
                  ? '账号将提交到所选平台，授权后继续确认所需资料。'
                  : '暂无启用的平台连接，请先配置平台。'
            }}
          </div>
        </el-form-item>
        <el-form-item :id="fieldAnchors.proxy" label="授权与上号代理" :error="fieldErrors.proxy_id">
          <OnboardingProxyPicker
            v-if="modelValue"
            :model-value="edit.proxy_id"
            :proxies="availableProxies"
            :disabled="saving || oauthBusy"
            @update:model-value="changeProxy"
            @created="proxyCreated"
            @busy="proxyBusy = $event"
            @dirty="proxyDirty = $event"
            @pending="proxyPending = $event"
          />
          <div v-if="savedCredentials && !edit.proxy_id" class="field-hint">未更换代理时沿用已保存凭证中的代理。</div>
          <div v-if="edit.proxy_id && connected && !(linkedReauth && savedCredentials)" class="field-hint">
            更换代理后需要重新授权，已填写的交付资料会保留。
          </div>
          <div v-if="linkedReauth && savedCredentials" class="field-hint">
            沿用已保存凭证时可更换代理后直接同步；清空代理表示解除池绑定并沿用凭证内的代理地址。
          </div>
        </el-form-item>
        <p v-if="proxyChanged" class="wizard-inline-notice" role="status">
          代理已变更，请重新授权；其他已填写资料已保留。
        </p>
        <template v-if="savedCredentials">
          <div class="wizard-connected">
            <CheckCircle2 :size="22" />
            <div>
              <strong>{{ edit.account }}</strong>
              <p>{{ linkedReauth ? '将使用库内已保存凭证同步到平台。' : '账号凭证已保存，无需重复授权。' }}</p>
              <p v-if="fieldErrors.expired" class="wizard-inline-notice" data-saved-expired role="alert">
                {{ fieldErrors.expired }}
              </p>
            </div>
            <el-button text data-reauthorize @click="startReauthorize">重新授权</el-button>
          </div>
          <el-form-item
            v-if="linkedReauth"
            :id="fieldAnchors.deviceIds"
            data-saved-devices
            label="设备资料"
            :class="fieldClass(fieldAnchors.deviceIds)"
            :error="fieldErrors.claude_device_ids_text"
            ><el-input
              v-model="edit.claude_device_ids_text"
              type="textarea"
              :rows="2"
              placeholder="填写设备 ID，多个 ID 用逗号或换行分隔"
            />
            <div class="field-hint">
              选填。有真实设备标识时填写；留空则沿用库内标识，库内也没有时提交会自动生成 UUID。
              <a class="dialog-help-link" href="/help?topic=device-id" target="_blank" rel="noopener noreferrer"
                >如何获取？</a
              >
            </div>
            <el-button text type="primary" @click="deviceImportOpen = !deviceImportOpen"
              >从已有凭证导入设备资料</el-button
            >
            <div v-if="deviceImportOpen" class="wizard-device-import">
              <el-input
                v-model="devicePaste"
                type="textarea"
                :rows="2"
                placeholder="仅提取 JSON 中的 claude_device_ids，不更换账号凭证"
              /><el-button :disabled="!devicePaste.trim()" @click="importDevices">提取设备资料</el-button>
            </div></el-form-item
          >
        </template>
        <template v-else>
          <ClaudeOAuthPanel
            v-if="modelValue && source !== 'import' && source !== 'manual'"
            :key="oauthGeneration"
            :proxy-id="edit.proxy_id"
            :reauthorizing="canReuseCredentialFields"
            :saved-account-id="savedProxyAccountId"
            :disabled="
              saving || proxyBusy || proxyPending || !connectionReady || (!poolProxyReady && !savedProxyAccountId)
            "
            :summary="oauthSummary"
            @started="onAuthorizationStarted"
            @busy="oauthBusy = $event"
            @authorized="fillFromOAuth"
            @change-proxy="jumpTo(fieldAnchors.proxy)"
          />
          <div v-if="source === 'import' || source === 'manual'" class="wizard-connected">
            <CheckCircle2 :size="22" />
            <div>
              <strong>{{ edit.account || edit.credential_email || '凭证已导入' }}</strong>
              <p>
                {{ source === 'import' ? '导入完成，下一步检查并补充缺失资料。' : '下一步填写账号资料与完整凭证。' }}
              </p>
            </div>
            <el-button text :disabled="busy" @click="startReauthorize">改用浏览器授权</el-button>
          </div>
          <div class="wizard-connection-actions">
            <button
              v-if="oauthBlocker"
              type="button"
              class="wizard-blocker"
              data-oauth-blocker
              @click="jumpTo(oauthBlocker.anchor)"
            >
              <AlertCircle :size="14" />还不能登录：{{ oauthBlocker.text }}
            </button>
            <span v-else class="field-hint">服务端授权使用所选代理；浏览器登录使用浏览器自身网络。</span
            ><el-button
              v-if="connected && !edit.id"
              data-change-account
              text
              type="primary"
              :disabled="busy"
              @click="changeAccount"
              >更换账号</el-button
            >
          </div>
          <details class="wizard-import">
            <summary>已有凭证？从 JSON 导入或手动填写</summary>
            <div class="credential-paste">
              <el-input
                v-model="credentialPaste"
                data-import-input
                type="textarea"
                :rows="3"
                :disabled="busy"
                placeholder="粘贴 auth_json 或含账号资料的单条 JSON 记录"
                autocomplete="off"
                spellcheck="false"
              />
              <div class="credential-paste-actions">
                <el-button data-import :disabled="busy || !credentialPaste.trim()" @click="fillFromPaste"
                  >解析并填充</el-button
                ><el-button text :disabled="busy" @click="startManual">手动填写凭证</el-button>
              </div>
              <p class="field-hint">导入后统一检查资料，完整记录可直接进入确认页。</p>
            </div>
          </details>
        </template>
      </section>
      <section v-if="step === 2 && linkedReauth" class="wizard-page" aria-label="补充设备与凭证" data-reauth-devices>
        <div class="wizard-connected">
          <CheckCircle2 :size="22" />
          <div>
            <strong>{{ edit.account || edit.credential_email || '凭证已连接' }}</strong>
            <p>
              {{
                canReuseCredentialFields
                  ? '已保存的设备和代理资料会自动沿用，需要更换时可在此填写。'
                  : '授权不返回设备标识；可在此选填，未填时提交会自动生成 UUID。'
              }}
            </p>
          </div>
        </div>
        <el-form-item
          :id="fieldAnchors.deviceIds"
          label="设备资料"
          :class="fieldClass(fieldAnchors.deviceIds)"
          :error="fieldErrors.claude_device_ids_text"
          ><el-input
            v-model="edit.claude_device_ids_text"
            type="textarea"
            :rows="2"
            placeholder="填写设备 ID，多个 ID 用逗号或换行分隔"
          />
          <div class="field-hint">
            {{
              canReuseCredentialFields
                ? '选填。留空沿用已保存设备标识；库内也没有时提交会自动生成 UUID。'
                : '选填。有真实设备标识时填写；未填时提交会自动生成 UUID。'
            }}
            <a class="dialog-help-link" href="/help?topic=device-id" target="_blank" rel="noopener noreferrer"
              >如何获取？</a
            >
          </div>
          <el-button text type="primary" @click="deviceImportOpen = !deviceImportOpen"
            >从已有凭证导入设备资料</el-button
          >
          <div v-if="deviceImportOpen" class="wizard-device-import">
            <el-input
              v-model="devicePaste"
              type="textarea"
              :rows="2"
              placeholder="仅提取 JSON 中的 claude_device_ids，不更换账号凭证"
            /><el-button :disabled="!devicePaste.trim()" @click="importDevices">提取设备资料</el-button>
          </div></el-form-item
        >
        <details
          :id="fieldAnchors.credentials"
          class="credential-manual"
          :open="credentialDetailsOpen"
          @toggle="credentialDetailsOpen = ($event.target as HTMLDetailsElement).open"
        >
          <summary><ShieldCheck :size="15" />高级凭证字段<span class="field-hint">默认隐藏敏感值</span></summary>
          <p v-if="canReuseCredentialFields" class="field-hint">
            账号资料会在核对身份后自动沿用，无法确认的字段仍需补填。
          </p>
          <div v-if="credentialDetailsOpen" class="form-grid two credential-grid">
            <el-form-item
              v-for="field in visibleCredentialInputs"
              :id="'credential-field-' + field.key"
              :key="field.key"
              :required="
                field.required &&
                !(
                  canReuseCredentialFields &&
                  ['account_uuid', 'organization_name', 'credential_proxy_url'].includes(field.key)
                )
              "
              :error="fieldErrors[field.key]"
              ><template #label
                ><span class="credential-field-label"
                  >{{ field.label }}<code>{{ field.code }}</code></span
                ></template
              ><el-input
                v-model="edit[field.key]"
                :type="field.secret ? 'password' : 'text'"
                :show-password="field.secret"
                :placeholder="field.placeholder"
                autocomplete="off"
            /></el-form-item>
          </div>
        </details>
      </section>
      <section v-if="step === 2 && !linkedReauth" class="wizard-page" aria-label="补充资料">
        <div class="wizard-identity">
          <span class="account-dialog-icon"><UsersRound :size="20" /></span>
          <div>
            <strong>{{ edit.account || edit.credential_email || '待填写账号' }}</strong
            ><span class="wizard-badge">{{ sourceLabel }}</span>
            <p>
              {{ planLabel || '套餐待确认'
              }}<template v-if="edit.register_time"> · 注册于 {{ edit.register_time }}</template>
            </p>
          </div>
        </div>
        <AccountCompletionChecklist
          compact
          :items="checklist"
          :warnings="oauthWarnings"
          :blocker="gapCount ? '' : missingFields[0]"
          @jump="jumpTo"
        />
        <div class="form-grid two">
          <el-form-item
            v-if="showProfile('account')"
            :id="fieldAnchors.account"
            label="账号邮箱"
            required
            :error="fieldErrors.account"
            ><el-input
              v-model="edit.account"
              :disabled="!!edit.id || !!oauthSummary?.email"
              placeholder="name@example.com"
            />
            <div v-if="source === 'oauth'" class="field-hint">更换授权账号请返回第一步。</div></el-form-item
          >
          <el-form-item
            v-if="showProfile('plan')"
            :id="fieldAnchors.plan"
            label="套餐"
            required
            :error="fieldErrors.plan"
            ><el-select v-model="edit.plan" placeholder="选择实际订阅套餐"
              ><el-option v-for="p in plans" :key="p.value" :label="p.label" :value="p.value" /></el-select
          ></el-form-item>
          <el-form-item
            v-if="showProfile('register_time')"
            :id="fieldAnchors.registerTime"
            label="注册日期"
            required
            :error="fieldErrors.register_time"
            ><el-date-picker v-model="edit.register_time" value-format="YYYY-MM-DD" placeholder="请选择注册日期"
          /></el-form-item>
        </div>
        <el-form-item
          :id="fieldAnchors.password"
          label="交付密码"
          :required="!edit.password_configured"
          :class="fieldClass(fieldAnchors.password)"
          :error="fieldErrors.password"
          ><el-input
            v-model="edit.password"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="edit.password_configured ? '已保存，留空保留原密码' : '输入账号密码'"
          />
          <div class="field-hint">上号平台要求的账号密码，Claude 授权不会提供。</div></el-form-item
        >
        <div class="form-grid two">
          <el-form-item
            v-for="field in subscriptionDates"
            :id="field.anchor"
            :key="field.key"
            :label="field.label"
            required
            :class="fieldClass(field.anchor)"
            :error="fieldErrors[field.key]"
            ><el-date-picker v-model="edit[field.key]" value-format="YYYY-MM-DD" placeholder="请选择"
          /></el-form-item>
        </div>
        <p class="field-hint wizard-date-hint">请按账单确认本期订阅日期，与授权凭证的有效期无关。</p>
        <el-form-item
          v-if="!savedCredentials"
          :id="fieldAnchors.deviceIds"
          label="设备资料"
          :class="fieldClass(fieldAnchors.deviceIds)"
          :error="fieldErrors.claude_device_ids_text"
          ><el-input
            v-model="edit.claude_device_ids_text"
            type="textarea"
            :rows="2"
            placeholder="填写设备 ID，多个 ID 用逗号或换行分隔"
          />
          <div class="field-hint">
            选填。有真实设备标识时填写；未填时提交会自动生成 UUID。
            <a class="dialog-help-link" href="/help?topic=device-id" target="_blank" rel="noopener noreferrer"
              >如何获取？</a
            >
          </div>
          <el-button text type="primary" @click="deviceImportOpen = !deviceImportOpen"
            >从已有凭证导入设备资料</el-button
          >
          <div v-if="deviceImportOpen" class="wizard-device-import">
            <el-input
              v-model="devicePaste"
              type="textarea"
              :rows="2"
              placeholder="仅提取 JSON 中的 claude_device_ids，不更换账号凭证"
            /><el-button :disabled="!devicePaste.trim()" @click="importDevices">提取设备资料</el-button>
          </div></el-form-item
        >
        <details class="wizard-known">
          <summary>查看已获取的信息与交付设置</summary>
          <dl class="wizard-facts">
            <dt>账号</dt>
            <dd>{{ edit.account || '待填写' }} · {{ knownSource('account') }}</dd>
            <dt>套餐</dt>
            <dd>{{ planLabel || '待填写' }} · {{ knownSource('plan') }}</dd>
            <dt>注册日期</dt>
            <dd>{{ edit.register_time || '待填写' }} · {{ knownSource('register_time') }}</dd>
            <dt>注册地区</dt>
            <dd>{{ edit.register_location === 'US' ? '美国（US）· 当前平台支持' : edit.register_location }}</dd>
            <dt>上号平台</dt>
            <dd>{{ selectedConnectionName }}</dd>
            <dt>代理</dt>
            <dd>{{ selectedProxyName }}</dd>
          </dl>
          <el-button text type="primary" @click="editKnownFields = !editKnownFields">{{
            editKnownFields ? '收起已获取字段' : '修改账号资料'
          }}</el-button
          ><el-button text @click="backToConnection">修改平台或代理</el-button>
        </details>
        <details
          v-if="!savedCredentials"
          :id="fieldAnchors.credentials"
          class="credential-manual"
          :open="credentialDetailsOpen"
          @toggle="credentialDetailsOpen = ($event.target as HTMLDetailsElement).open"
        >
          <summary><ShieldCheck :size="15" />高级凭证字段<span class="field-hint">默认隐藏敏感值</span></summary>
          <div v-if="credentialDetailsOpen" class="form-grid two credential-grid">
            <el-form-item
              v-for="field in visibleCredentialInputs"
              :id="'credential-field-' + field.key"
              :key="field.key"
              :required="field.required"
              :error="fieldErrors[field.key]"
              ><template #label
                ><span class="credential-field-label"
                  >{{ field.label }}<code>{{ field.code }}</code></span
                ></template
              ><el-input
                v-model="edit[field.key]"
                :type="field.secret ? 'password' : 'text'"
                :show-password="field.secret"
                :placeholder="field.placeholder"
                autocomplete="off"
            /></el-form-item>
          </div>
        </details>
        <AccountOptionalFields v-model="edit" />
      </section>
      <section v-if="submitted" data-submitted class="wizard-page wizard-success" aria-label="已提交">
        <div class="wizard-connected">
          <CheckCircle2 :size="24" />
          <div>
            <strong>已提交 {{ edit.account }}</strong>
            <p>
              平台正在处理，结果会显示在账号卡片上。平台「{{
                selectedConnectionName
              }}」和代理已保留，可以直接添加下一个账号。
            </p>
          </div>
        </div>
      </section>
      <section v-else-if="step === 3" data-review class="wizard-page" aria-label="确认上号">
        <div class="wizard-connected">
          <CheckCircle2 :size="24" />
          <div>
            <strong>{{ linkedReauth ? (pushFailed ? '同步未成功，凭证仍保留' : '凭证已就绪') : '资料已齐全' }}</strong>
            <p>
              {{
                linkedReauth
                  ? pushFailed
                    ? '可修改代理后重试同步，或主动重新授权。关闭窗口不会保存本次未成功推送的新令牌。'
                    : savedCredentials
                      ? '核对后将使用库内已保存凭证推送到平台。'
                      : '核对后将更新本地凭证并推送到平台。'
                  : '核对信息后，提交到所选平台。'
              }}
            </p>
          </div>
        </div>
        <dl class="wizard-facts wizard-review">
          <dt>账号</dt>
          <dd>{{ edit.account }}</dd>
          <template v-if="!linkedReauth">
            <dt>套餐</dt>
            <dd>{{ planLabel }}</dd>
            <dt>本期订阅</dt>
            <dd>{{ edit.plan_start_time }} 至 {{ edit.plan_end_time }}</dd>
            <dt>注册日期</dt>
            <dd>{{ edit.register_time }}</dd>
            <dt>注册地区</dt>
            <dd>{{ edit.register_location === 'US' ? '美国（US）' : edit.register_location }}</dd>
          </template>
          <dt>上号平台</dt>
          <dd>{{ linkedReauth ? linkedConnectionName : selectedConnectionName }}</dd>
          <dt>所选代理</dt>
          <dd>{{ selectedProxyName }}</dd>
          <dt>{{ linkedReauth ? '凭证' : '凭证与密码' }}</dt>
          <dd>{{ savedCredentials ? '沿用已保存凭证' : '已填写 · 内容已隐藏' }}</dd>
          <dt>设备资料</dt>
          <dd>
            {{
              edit.claude_device_ids_text?.trim()
                ? savedCredentials
                  ? '本次补填'
                  : '已填写'
                : savedCredentials || canReuseCredentialFields
                  ? '沿用已保存或自动生成'
                  : '未填写，提交时自动生成'
            }}
          </dd>
        </dl>
        <p class="field-hint">
          {{
            linkedReauth
              ? '仅平台确认接收后才算同步成功；失败时本次新兑换凭证不会写入本地。'
              : '提交后可查看平台处理进度；平台确认接收后才算上号成功。'
          }}
        </p>
      </section>
    </el-form>
    <template #footer
      ><div v-if="submitted" class="wizard-footer wizard-footer-done">
        <el-button data-finish @click="finish">完成</el-button>
        <el-button data-add-another type="primary" @click="addAnother">继续添加下一个</el-button>
      </div>
      <div v-else class="wizard-footer">
        <div class="create-dialog-actions">
          <el-button data-cancel :disabled="busy" @click="requestClose">取消</el-button
          ><el-button v-if="step > 1" data-back :disabled="busy" @click="goBack">上一步</el-button
          ><el-button
            v-if="linkedReauth && (step === 3 || savedCredentials)"
            data-reauthorize
            text
            :disabled="busy"
            @click="startReauthorize"
            >重新授权</el-button
          >
        </div>
        <el-button
          v-if="step < 3"
          data-next
          type="primary"
          :disabled="busy || (step === 1 && !connected)"
          @click="advance"
          >{{
            linkedReauth || (step === 1 && connected && !missingFields.length) || step > 1
              ? '继续，核对信息'
              : '继续，补充资料'
          }}</el-button
        ><el-button
          v-else
          data-submit
          type="primary"
          :loading="saving"
          :disabled="oauthBusy || proxyBusy"
          @click="save"
          >{{ submitLabel }}</el-button
        >
      </div></template
    >
  </el-dialog>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { UsersRound, ShieldCheck, CheckCircle2, BookOpen, AlertCircle } from 'lucide-vue-next'
import { useAccountOnboarding, type AccountOnboardingProps } from '../composables/useAccountOnboarding'
import type { OnboardResult, ReauthorizeResult } from '../types/api'
import ClaudeOAuthPanel from './ClaudeOAuthPanel.vue'
import AccountCompletionChecklist from './AccountCompletionChecklist.vue'
import OnboardingProxyPicker from './OnboardingProxyPicker.vue'
import AccountOptionalFields from './AccountOptionalFields.vue'
import ErrorNotice from './ErrorNotice.vue'
const props = defineProps<AccountOnboardingProps>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: [result: OnboardResult]
  submitted: [result: OnboardResult]
  reauthorized: [result: ReauthorizeResult]
  'proxy-created': []
}>()
const forceReauth = computed(() => !!props.forceReauth)
const {
  stepLabels,
  submitted,
  oauthBlocker,
  finish,
  addAnother,
  step,
  displayStep,
  linkedReauth,
  linkedConnectionName,
  edit,
  source,
  credentialPaste,
  notice,
  fieldErrors,
  oauthBusy,
  proxyBusy,
  proxyDirty,
  proxyPending,
  saving,
  busy,
  oauthGeneration,
  proxyChanged,
  pushFailed,
  submitLabel,
  availableProxies,
  savedCredentials,
  connected,
  connectionReady,
  poolProxyReady,
  canReuseCredentialFields,
  savedProxyAccountId,
  selectedConnectionName,
  selectedProxyName,
  planLabel,
  sourceLabel,
  oauthSummary,
  oauthWarnings,
  editKnownFields,
  credentialDetailsOpen,
  deviceImportOpen,
  devicePaste,
  checklist,
  gapCount,
  missingFields,
  visibleCredentialInputs,
  subscriptionDates,
  enabledConnections,
  fieldAnchors,
  plans,
  showProfile,
  knownSource,
  fieldClass,
  fillFromOAuth,
  onAuthorizationStarted,
  proxyCreated,
  changeProxy,
  startReauthorize,
  changeAccount,
  fillFromPaste,
  importDevices,
  startManual,
  jumpTo,
  goBack,
  backToConnection,
  advance,
  save,
  requestClose,
  beforeClose,
} = useAccountOnboarding(props, emit)
</script>
<style src="../styles/account-onboarding.css"></style>
