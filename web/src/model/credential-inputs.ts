import type { CredentialKey } from './account-form'

interface CredentialInput {
  key: CredentialKey
  label: string
  code: string
  placeholder: string
  required: boolean
  secret?: boolean
  textarea?: boolean
  wide?: boolean
  hint?: string
}
export const credentialInputs: CredentialInput[] = [
  {
    key: 'access_token',
    label: '访问令牌',
    code: 'access_token',
    placeholder: '粘贴 access token',
    secret: true,
    required: true,
  },
  {
    key: 'refresh_token',
    label: '刷新令牌',
    code: 'refresh_token',
    placeholder: '粘贴 refresh token',
    secret: true,
    required: true,
  },
  { key: 'credential_email', label: '凭证邮箱', code: 'email', placeholder: '与账号邮箱一致', required: true },
  { key: 'account_uuid', label: '账号标识', code: 'account_uuid', placeholder: '账号 UUID', required: true },
  { key: 'organization_uuid', label: '组织标识', code: 'organization_uuid', placeholder: '组织 UUID', required: true },
  { key: 'organization_name', label: '组织名称', code: 'organization_name', placeholder: '组织名称', required: true },
  {
    key: 'last_refresh',
    label: '最近刷新时间',
    code: 'last_refresh',
    placeholder: '2026-11-06T08:00:00Z',
    required: true,
  },
  { key: 'expired', label: '到期时间', code: 'expired', placeholder: '2026-12-06T08:00:00Z', required: true },
  {
    key: 'credential_proxy_url',
    secret: true,
    label: '代理地址',
    code: 'proxy_url',
    placeholder: 'http://user:pwd@host:8080',
    required: true,
  },
  {
    key: 'verification_status',
    label: '验证状态',
    code: 'verification_status',
    placeholder: 'not_required',
    required: true,
  },
  { key: 'credential_type', label: '凭证类型', code: 'type', placeholder: 'claude', required: true },
  { key: 'id_token', label: '身份令牌', code: 'id_token · 选填', placeholder: '可留空', secret: true, required: false },
  {
    key: 'claude_device_ids_text',
    label: '设备标识',
    code: 'claude_device_ids',
    placeholder: '填写设备 ID，多个 ID 用逗号或换行分隔',
    textarea: true,
    wide: true,
    required: true,
    hint: '必填。真实设备标识 OAuth 不返回；请从已有有效凭证中获取，不要随机生成。',
  },
]
