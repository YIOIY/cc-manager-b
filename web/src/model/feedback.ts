import type { FeedbackKind, FeedbackStatus } from '../types/api'

export const feedbackKinds: { value: FeedbackKind; label: string; hint: string }[] = [
  { value: 'problem', label: '遇到问题', hint: '报错、卡住、结果不对' },
  { value: 'suggestion', label: '功能建议', hint: '希望增加或改进的地方' },
  { value: 'other', label: '其他', hint: '' },
]

export const feedbackKindLabel = (kind: string) => feedbackKinds.find((k) => k.value === kind)?.label ?? kind

export const feedbackStatuses: {
  value: FeedbackStatus
  label: string
  tone: 'warning' | 'primary' | 'success' | 'info'
}[] = [
  { value: 'open', label: '待处理', tone: 'warning' },
  { value: 'in_progress', label: '处理中', tone: 'primary' },
  { value: 'resolved', label: '已解决', tone: 'success' },
  { value: 'closed', label: '不处理', tone: 'info' },
]

export const feedbackStatusOf = (status: string) =>
  feedbackStatuses.find((s) => s.value === status) ?? {
    value: status as FeedbackStatus,
    label: status,
    tone: 'info' as const,
  }
