// In-app help: the operator manual and the FAQ. The wording follows the
// labels on the screens it describes; keep them in step when a screen
// changes. Never put server addresses, tokens or passwords in here.

export type HelpGroupKey = 'manual' | 'faq'

export type HelpBlock =
  | { type: 'p'; text: string }
  | { type: 'steps'; items: string[] }
  | { type: 'list'; items: string[] }
  | { type: 'note'; tone: 'info' | 'warn'; text: string }
  | { type: 'code'; label?: string; text: string }
  | { type: 'link'; text: string; topic: string }

export interface HelpTopic {
  id: string
  group: HelpGroupKey
  title: string
  /** Extra words people search for that the text itself may not contain. */
  keywords?: string[]
  blocks: HelpBlock[]
}

export const helpGroups: { key: HelpGroupKey; label: string }[] = [
  { key: 'manual', label: '操作手册' },
  { key: 'faq', label: '常见问题' },
]

export const helpTopics: HelpTopic[] = [
  {
    id: 'prepare',
    group: 'manual',
    title: '上号前的准备',
    keywords: ['准备', '前置', '条件'],
    blocks: [
      { type: 'p', text: '上号是把一个 Claude 账号的资料和凭证提交到供应商平台。开始前先确认下面三样都已就绪。' },
      {
        type: 'list',
        items: [
          '平台连接：在「平台设置」里有一条状态为「启用」、TOKEN 为「已配置」的连接。没有或停用时，上号弹窗的「上号到」里选不了它。',
          '代理：在「代理池」里有可用的代理。授权和上号都通过所选代理进行。',
          '账号资料：账号邮箱、交付密码、套餐、注册日期、套餐开始和结束日期，以及该账号的设备标识。',
        ],
      },
      {
        type: 'note',
        tone: 'info',
        text: '平台连接和用户权限由管理员维护。看不到某个菜单或按钮，说明当前账号没有对应权限，请联系管理员。',
      },
      { type: 'link', text: '下一步：新建并上号', topic: 'onboard' },
    ],
  },
  {
    id: 'onboard',
    group: 'manual',
    title: '新建并上号（三步）',
    keywords: ['上号', '新建账号', '添加账号', '授权', 'OAuth', '流程'],
    blocks: [
      { type: 'p', text: '入口：「账号管理」右上角「新建账号」。弹窗分三步：连接账号、补充资料、确认上号。' },
      {
        type: 'steps',
        items: [
          '连接账号：在「上号到」选择平台连接，在「授权与上号代理」选择代理，然后点「登录 Claude」生成授权链接。',
          '打开授权链接（点「打开授权页」，或复制链接到已登录该 Claude 账号的浏览器），登录并允许授权。把授权页给出的授权码、code#state 或完整地址粘贴到「授权结果」，点「确认授权结果」。成功后面板会收起为「账号已连接」。',
          '补充资料：授权会自动带回账号邮箱、套餐和注册日期（带「已自动获取」标记）。其余项目需要手动填写：交付密码、套餐开始和结束日期、设备资料。页面上的「还差 N 项」清单会列出没填的项目，点「去填写」可直接跳到对应位置。',
          '确认上号：核对信息无误后点「确认并上号」。提交后账号卡片显示「上号中」，结果会自动更新为「已上号」或「上号失败」。',
        ],
      },
      {
        type: 'note',
        tone: 'info',
        text: '授权链接 30 分钟内有效；重新获取后旧链接立即失效。授权成功本身不会创建账号，要走完第三步才会提交到平台。',
      },
      {
        type: 'note',
        tone: 'warn',
        text: '同一个账号不要重复提交。卡片显示「结果待确认」时，先到平台核实上次是否已接收。',
      },
      { type: 'link', text: '设备资料怎么填：获取设备标识', topic: 'device-id' },
      { type: 'link', text: '提交之后：查看上号结果与重试', topic: 'after-submit' },
    ],
  },
  {
    id: 'device-id',
    group: 'manual',
    title: '获取设备标识',
    keywords: ['设备 ID', '设备资料', 'claude_device_ids', 'userID', 'device'],
    blocks: [
      {
        type: 'p',
        text: '设备标识不在授权返回的内容里，需要手动填写。它来自该账号实际登录 Claude Code 的那台电脑：Claude Code 首次运行时会在用户目录的 .claude.json 文件里保存一个 64 位的标识，字段名是 userID。',
      },
      { type: 'p', text: '在那台电脑上执行下面的命令，把输出的一整行填到「设备资料」里。' },
      {
        type: 'code',
        label: 'macOS / Linux（终端）',
        text: `python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/.claude.json')))['userID'])"`,
      },
      {
        type: 'code',
        label: 'Windows（PowerShell）',
        text: '(Get-Content "$env:USERPROFILE\\.claude.json" | ConvertFrom-Json).userID',
      },
      {
        type: 'list',
        items: [
          '有多个设备标识时，用逗号或换行分隔。',
          '如果手里有该账号完整的凭证 JSON，可以点「从已有凭证导入设备资料」，粘贴后系统只提取其中的 claude_device_ids，不会更换账号凭证。',
          '设备标识属于电脑而不是账号：请填写这个账号实际登录的那台设备的标识。',
        ],
      },
      {
        type: 'note',
        tone: 'info',
        text: '供应商没有在文档里说明设备标识的格式要求。如果提交后平台因为这一项拒绝，请把平台返回的提示发给管理员，由管理员向供应商确认。',
      },
    ],
  },
  {
    id: 'after-submit',
    group: 'manual',
    title: '查看上号结果与重试',
    keywords: ['状态', '失败', '重试', '任务中心', '待上号', '上号中', '已上号'],
    blocks: [
      { type: 'p', text: '账号卡片右上角显示当前状态：' },
      {
        type: 'list',
        items: [
          '待上号：账号已保存在本地，还未提交到平台。点「去上号」继续。',
          '上号中：正在提交到平台，结果会自动更新。',
          '已上号：平台已接收账号。卡片会显示平台上的状态（使用中、已封禁等）和用量。',
          '上号失败：本次未成功，资料已保留。点「处理并重试」查看原因，修正后重新提交。',
          '结果待确认：请求已发出，但没有收到明确结果。先到平台核实，不要重新录入同一个账号。',
        ],
      },
      {
        type: 'p',
        text: '点「处理并重试」会打开提交窗口：选择「上号平台」，系统先做「提交前检查」，通过后才能点「提交上号」。每次提交的记录和平台返回的原因可在「任务中心」查看。',
      },
      { type: 'link', text: '常见失败原因：当前供应商未获授权该厂商', topic: 'faq-supplier-not-authorized' },
      { type: 'link', text: '「结果待确认」是什么意思', topic: 'faq-outcome-unknown' },
    ],
  },
  {
    id: 'platform-profile',
    group: 'manual',
    title: '修改平台上的账号资料',
    keywords: ['修改平台资料', '备注', '套餐日期', '余量'],
    blocks: [
      {
        type: 'p',
        text: '已上号的账号卡片底部有「修改平台资料」按钮（需要相应权限）。它会直接修改供应商平台上的记录，并把本地资料同步成一样的值。',
      },
      {
        type: 'steps',
        items: [
          '点「修改平台资料」，表单里是平台当前的值。可修改套餐、注册日期、套餐开始和结束日期、注册地区、余量、支付方式、注册设备、充值日期、质保期和备注。「用本地资料填充」可以把本地账号的套餐、日期和注册地区带进表单。',
          '点「下一步」，逐项核对「平台当前」和「新值」。只列出有改动的字段。',
          '点「提交到平台」。提交后无法撤销；密码、凭证、标签和代理不受影响。',
        ],
      },
      {
        type: 'note',
        tone: 'info',
        text: '按钮不显示的几种情况：账号还没上号、平台已移除该账号、平台连接没有配置网页登录或网页通道已暂停。',
      },
      { type: 'link', text: '提示「平台资料刚更新，请重新打开」怎么办', topic: 'faq-profile-conflict' },
    ],
  },
  {
    id: 'connections',
    group: 'manual',
    title: '平台连接设置（管理员）',
    keywords: ['平台设置', 'Token', '令牌', '网页登录', '测试登录', '启用', '停用'],
    blocks: [
      {
        type: 'p',
        text: '「平台设置」里的每条平台连接对应供应商的一个账户。上号用其中的 OpenAPI Token；账号状态、用量和按模型账单通过「网页后台登录」定时读取。',
      },
      {
        type: 'steps',
        items: [
          '新建：点「新建连接」，填写名称、Base URL、OpenAPI Token 和时区。',
          '修改：点该连接所在行的「修改」。Token 和登录密码留空表示保持不变，系统不会回显这些内容。',
          '网页登录：填写供应商后台的登录账号和登录密码，保存后重新打开该连接，点「测试登录」确认可用。',
          '停用：关闭「启用状态」即可。停用的连接不能用于上号，也不再同步。',
        ],
      },
      {
        type: 'note',
        tone: 'warn',
        text: '一条连接只对应供应商的一个账户。上号时请确认「上号到」选的是有权限接收该类账号的那条连接。',
      },
      { type: 'link', text: '删除连接时提示仍有关联账号', topic: 'faq-connection-in-use' },
      { type: 'link', text: '网页通道已暂停怎么恢复', topic: 'faq-web-suspended' },
    ],
  },
  {
    id: 'dashboard',
    group: 'manual',
    title: '看懂仪表盘',
    keywords: ['仪表盘', '接近限额', '消费', '需要处理', '同步'],
    blocks: [
      {
        type: 'list',
        items: [
          '平台账号：平台上的账号总数，以及其中使用中和已封禁的数量。',
          '接近限额：5 小时窗口用量达到 80% 的账号数，点击可查看这些账号。',
          '今日消费 / 7 天消费：来自按模型账单。今天的数据约有 2 小时延迟，标为「未出齐」。',
          '需要处理：接近限额、封禁或失败、30 天内到期、平台已移除、资料与平台不一致的账号。点击一行可跳到该账号。',
          '底部一行：网页同步和指标同步的最近时间与状态、启用的代理数、任务情况。',
        ],
      },
      { type: 'link', text: '指标同步显示「平台未开通」', topic: 'faq-metrics-not-enabled' },
    ],
  },
  {
    id: 'faq-supplier-not-authorized',
    group: 'faq',
    title: '上号失败：「当前供应商未获授权该厂商」',
    keywords: ['403', 'PLATFORM_AUTH_FAILED', '身份验证', '未获授权', '平台拒绝'],
    blocks: [
      {
        type: 'p',
        text: '页面提示「平台拒绝了本次请求的身份验证，平台返回：当前供应商未获授权该厂商」（平台返回 403）。',
      },
      {
        type: 'p',
        text: '含义：提交用的那条平台连接所对应的供应商账户，没有被平台授权接收这一类账号。账号本身的密码和凭证与此无关，重新录入或反复重试都不会成功。',
      },
      {
        type: 'steps',
        items: [
          '点「处理并重试」，查看「上号平台」选的是哪一条连接。',
          '如果选错了，换成有权限的那条连接后重新提交。',
          '如果只有这一条连接，请联系管理员：需要供应商为该账户开通对应厂商，或换用正确的平台连接。',
        ],
      },
    ],
  },
  {
    id: 'faq-connection-disabled',
    group: 'faq',
    title: '「上号到」里的平台是灰色，选不了',
    keywords: ['灰色', '选不了', '停用', '未配置', '上号到'],
    blocks: [
      {
        type: 'p',
        text: '只有状态为「启用」的平台连接可以选。灰色表示这条连接在「平台设置」里被停用，或还没有配置 Token。',
      },
      { type: 'p', text: '请管理员在「平台设置」里点该连接的「修改」，填写 OpenAPI Token 并打开「启用状态」。' },
      { type: 'link', text: '平台连接设置', topic: 'connections' },
    ],
  },
  {
    id: 'faq-copy-link',
    group: 'faq',
    title: '授权链接提示「无法自动复制」',
    keywords: ['复制', '剪贴板', '授权链接', '打开授权页'],
    blocks: [
      { type: 'p', text: '少数浏览器或受限环境不允许网页写入剪贴板。出现这个提示时：' },
      {
        type: 'list',
        items: ['直接点「打开授权页」，在新标签页里完成授权；', '或者在「授权链接」输入框里全选后手动复制。'],
      },
    ],
  },
  {
    id: 'faq-link-expired',
    group: 'faq',
    title: '提示「授权链接已失效」',
    keywords: ['OAUTH_SESSION_EXPIRED', '失效', '过期', '重新获取链接'],
    blocks: [
      {
        type: 'p',
        text: '授权链接在以下情况会失效：生成超过 30 分钟、点了「重新获取链接」、已经用过一次、换了代理，或系统刚刚更新重启。',
      },
      { type: 'p', text: '点「重新获取链接」，用新链接重新授权，再粘贴新的授权结果即可。旧链接得到的授权码不能再用。' },
    ],
  },
  {
    id: 'faq-missing-fields',
    group: 'faq',
    title: '授权成功了，为什么还显示「还差 N 项」',
    keywords: ['还差', '待补充', '清单', '缺项'],
    blocks: [
      { type: 'p', text: '授权只能带回账号邮箱、套餐、注册日期和凭证。下面这些授权不提供，必须手动填写：' },
      {
        type: 'list',
        items: [
          '交付密码',
          '套餐开始日期和结束日期（按订阅账单填写）',
          '设备资料（设备标识）',
          '平台连接和交付代理（如果第一步没有选）',
        ],
      },
      { type: 'p', text: '清单里每一项都可以点「去填写」跳到对应位置，填好后自动打勾。全部完成后才能提交。' },
      { type: 'link', text: '获取设备标识', topic: 'device-id' },
    ],
  },
  {
    id: 'faq-outcome-unknown',
    group: 'faq',
    title: '账号显示「结果待确认」',
    keywords: ['SUBMIT_OUTCOME_UNKNOWN', '待确认', '超时', '重复提交'],
    blocks: [
      {
        type: 'p',
        text: '请求已经发给平台，但没有收到可以判定成功或失败的结果（例如网络中断或平台响应超时）。平台可能已经接收，也可能没有。',
      },
      {
        type: 'steps',
        items: [
          '先不要重新提交或重新录入，以免同一个账号在平台上出现两条记录。点「查看详情」可以看到这次提交的任务编号。',
          '到平台核实该账号是否已经存在。「账号管理」里的「平台对账」可以查看平台当前的账号列表。',
          '平台上已有：等待下一次网页同步（约 10 分钟一次），系统会按邮箱自动把它和本地账号关联。平台上没有：请把账号邮箱和任务编号发给管理员处理，不要自己新建同一个账号。',
        ],
      },
    ],
  },
  {
    id: 'faq-connection-in-use',
    group: 'faq',
    title: '删除平台连接时提示「仍有关联账号、任务或历史账单」',
    keywords: ['CONNECTION_IN_USE', '删除', '删不掉'],
    blocks: [
      { type: 'p', text: '这是保护措施：这条连接下还有已上号的账号、提交记录或账单，删除会让这些数据失去归属。' },
      { type: 'p', text: '不再使用的连接请改为停用：点「修改」，关闭「启用状态」后保存。' },
    ],
  },
  {
    id: 'faq-web-suspended',
    group: 'faq',
    title: '提示「网页通道已暂停：登录连续失败」',
    keywords: ['WEB_SUSPENDED', 'WEB_LOGIN_FAILED', '暂停', '网页登录'],
    blocks: [
      {
        type: 'p',
        text: '系统用保存的后台账号密码登录供应商后台连续失败 3 次后，会暂停这条连接的网页同步，避免把后台账号刷到锁定。暂停期间账号状态、用量和账单不再更新，「修改平台资料」按钮也不显示。',
      },
      {
        type: 'p',
        text: '请管理员在「平台设置」里打开该连接，重新填写登录密码并保存，或点「测试登录」。登录成功后自动恢复。',
      },
    ],
  },
  {
    id: 'faq-metrics-not-enabled',
    group: 'faq',
    title: '仪表盘指标同步显示「平台未开通」',
    keywords: ['PLATFORM_QUERY_DENIED', '指标同步', '未开通'],
    blocks: [
      { type: 'p', text: '表示供应商还没有为这条连接开通 OpenAPI 的查询权限。这不是故障，也不影响上号。' },
      { type: 'p', text: '账号状态、用量和消费数据来自「网页同步」，只要网页同步显示正常，仪表盘上的数字就是最新的。' },
    ],
  },
  {
    id: 'faq-usd-amount',
    group: 'faq',
    title: '卡片上的美元金额和汇率是怎么来的',
    keywords: ['美元', '美金', 'USD', '汇率', '人民币', '累计花费', '换算'],
    blocks: [
      {
        type: 'p',
        text: '账号卡片「累计花费」里，人民币是平台报的原始金额；后面带「≈」的美元金额由系统按参考汇率换算得出，下面一行写着所用的汇率和汇率日期。',
      },
      {
        type: 'list',
        items: [
          '汇率是公开的美元兑人民币参考汇率，系统每 6 小时自动更新一次，不需要手动维护。把鼠标停在汇率那一行上可以看到来源。',
          '参考汇率和供应商实际结算用的汇率可能略有出入，所以美元金额仅供参考，对账请以人民币为准。',
          '显示「汇率暂不可用，仅显示人民币」：系统还没有取到汇率（例如刚启动，或服务器暂时访问不了汇率来源），取到后会自动出现美元金额。',
          '日期后面标「可能过期」：汇率已超过 3 天没有更新成功，请联系管理员检查服务器的外网访问。',
        ],
      },
    ],
  },
  {
    id: 'faq-cost-pending',
    group: 'faq',
    title: '今日消费为什么标着「未出齐」',
    keywords: ['未出齐', '延迟', '今日消费', '账单'],
    blocks: [
      {
        type: 'p',
        text: '按模型账单每 2 小时从平台读取一次，平台自身的数据也有延迟，所以当天的金额会随时间增加，到第二天才完整。之前几天的数字是完整的。',
      },
      { type: 'p', text: '需要立即刷新时，可在「平台离线账单」的「按模型消费」里点「立即同步」。' },
    ],
  },
  {
    id: 'faq-profile-conflict',
    group: 'faq',
    title: '修改平台资料时提示「平台资料刚更新，请重新打开」',
    keywords: ['VERSION_CONFLICT', '冲突', '重新打开'],
    blocks: [
      {
        type: 'p',
        text: '在你打开弹窗之后，这个账号在平台上的资料发生了变化（其他人刚修改过，或后台同步带来了新值）。为避免覆盖别人的修改，本次提交被拒绝，平台上的资料没有变化。',
      },
      { type: 'p', text: '关闭弹窗，重新点「修改平台资料」，在最新的值上再改一次即可。' },
    ],
  },
  {
    id: 'faq-login',
    group: 'faq',
    title: '登录不上，或想修改密码',
    keywords: ['登录', '密码', '忘记密码', '修改密码'],
    blocks: [
      {
        type: 'list',
        items: [
          '先确认用户名和密码没有多余的空格，注意大小写。',
          '连续输错后系统会要求等待一段时间再试，等待时间会逐次变长。',
          '忘记密码：请管理员在「用户与权限」里为你重置。',
          '修改自己的密码：点左下角用户名旁边的钥匙图标。修改后其他设备上的登录会失效。',
        ],
      },
    ],
  },
]

/** All searchable text of a topic. */
export function topicText(topic: HelpTopic): string {
  const parts: string[] = [topic.title, ...(topic.keywords || [])]
  for (const b of topic.blocks) {
    if (b.type === 'steps' || b.type === 'list') parts.push(...b.items)
    else if (b.type === 'code') parts.push(b.label || '', b.text)
    else parts.push(b.text)
  }
  return parts.join('\n')
}

/** Topics whose title, keywords or text contain the query (case-insensitive). */
export function searchTopics(topics: HelpTopic[], query: string): HelpTopic[] {
  const q = query.trim().toLowerCase()
  if (!q) return topics
  return topics.filter((t) => topicText(t).toLowerCase().includes(q))
}
