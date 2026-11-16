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
  /** A short sequence shown as connected stages. */
  | { type: 'flow'; steps: { title: string; text: string }[] }
  | { type: 'table'; head: string[]; rows: string[][] }

export type HelpAreaKey = 'onboard' | 'connection' | 'money' | 'account'

export interface HelpTopic {
  id: string
  group: HelpGroupKey
  title: string
  /** One line saying what the topic answers; shown on cards and under the title. */
  summary: string
  /** Icon name for manual topics (mapped to a component in the view). */
  icon?: string
  /** Which group of questions an FAQ topic belongs to. */
  area?: HelpAreaKey
  /** Extra words people search for that the text itself may not contain. */
  keywords?: string[]
  blocks: HelpBlock[]
}

export const helpGroups: { key: HelpGroupKey; label: string }[] = [
  { key: 'manual', label: '操作手册' },
  { key: 'faq', label: '常见问题' },
]

export const helpAreas: { key: HelpAreaKey; label: string }[] = [
  { key: 'onboard', label: '上号' },
  { key: 'connection', label: '平台连接' },
  { key: 'money', label: '金额与仪表盘' },
  { key: 'account', label: '账号与登录' },
]

/** The onboarding route on the help home: each stage opens the topic that covers it. */
export const helpPath: { title: string; text: string; topic: string }[] = [
  { title: '准备', text: '平台连接、代理、账号资料', topic: 'prepare' },
  { title: '连接账号', text: '选平台和代理，登录 Claude 授权', topic: 'onboard' },
  { title: '补充资料', text: '密码、套餐日期、设备标识', topic: 'faq-missing-fields' },
  { title: '确认上号', text: '核对后提交到平台', topic: 'onboard' },
  { title: '查看结果', text: '状态含义、失败原因与重试', topic: 'after-submit' },
]

/** Words people most often come here with. */
export const helpSuggestions = ['设备标识', '403', '复制', '汇率', '结果待确认']

export const helpTopics: HelpTopic[] = [
  {
    id: 'prepare',
    group: 'manual',
    title: '上号前的准备',
    summary: '开始前要有：启用的平台连接、可用的代理、账号的资料。',
    icon: 'checklist',
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
    summary: '从「新建账号」开始，三步把一个账号提交到平台。',
    icon: 'onboard',
    keywords: ['上号', '新建账号', '添加账号', '授权', 'OAuth', '流程'],
    blocks: [
      { type: 'p', text: '入口：「账号管理」右上角「新建账号」。弹窗分三步：连接账号、补充资料、确认上号。' },
      {
        type: 'flow',
        steps: [
          { title: '连接账号', text: '选平台和代理，登录 Claude 完成授权' },
          { title: '补充资料', text: '密码、套餐日期、设备资料' },
          { title: '确认上号', text: '核对后提交到平台' },
        ],
      },
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
    summary: '设备标识要手动填，这里说明它从哪里来、怎么取。',
    icon: 'device',
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
    summary: '账号卡片上五种状态的含义，以及失败后怎么重试。',
    icon: 'result',
    keywords: ['状态', '失败', '重试', '任务中心', '待上号', '上号中', '已上号'],
    blocks: [
      { type: 'p', text: '账号卡片右上角显示当前状态：' },
      {
        type: 'table',
        head: ['状态', '含义', '接下来'],
        rows: [
          ['待上号', '账号已保存在本地，还未提交到平台。', '点「去上号」继续。'],
          ['上号中', '正在提交到平台。', '等待，结果会自动更新。'],
          ['已上号', '平台已接收账号，卡片会显示平台上的状态和用量。', '无需操作。'],
          ['上号失败', '本次未成功，资料已保留。', '点「处理并重试」查看原因，修正后重新提交。'],
          ['结果待确认', '请求已发出，但没有收到明确结果。', '先到平台核实；平台没有时可删除本地账号后重新录入。'],
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
    summary: '直接改平台上的套餐、日期、备注等资料，并同步到本地。',
    icon: 'edit',
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
    summary: '新建、修改、停用平台连接，配置网页登录和美元汇率。',
    icon: 'settings',
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
          '美元汇率：平台把美元原价折算成人民币用的固定汇率，默认 7.2。账号卡片用它把花费换算成美元显示，平台调整汇率时在这里改。',
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
    summary: '仪表盘上每一块数字的含义和来源。',
    icon: 'dashboard',
    keywords: ['仪表盘', '接近限额', '消费', '需要处理', '同步'],
    blocks: [
      { type: 'p', text: '仪表盘回答一个问题：账号还够不够用。从上到下：' },
      {
        type: 'table',
        head: ['区域', '看什么'],
        rows: [
          [
            '顶部四个数字',
            '平台账号总数（使用中、已封禁）、接近限额的账号数、今日消费、7 天消费。金额按美元显示；7 天消费下面是日均和昨天相对前一天的变化。',
          ],
          ['累计数据', '所有账号的累计花费、累计 Token、两个窗口的平均占用、30 天内到期的账号数。'],
          ['需要处理', '接近限额、封禁或失败、30 天内到期、平台已移除、资料与平台不一致的账号。点一行跳到该账号。'],
          ['消费', '最近 7 天每天的消费（今天标斜纹，数字未出齐）、近 24 小时每小时的消费，以及按模型、按账号的占比。'],
          [
            '配额看板',
            '每个账号一行：5 小时窗口和 7 天窗口各用了多少、什么时候重置、累计花费。刻线是 80%，超过就算接近限额。点一行跳到该账号。',
          ],
          ['账号构成', '同一批账号按平台状态、上号状态、套餐、到期时间四种方式的分布。'],
          ['最近上号', '最近几次提交到平台的结果。需要「任务中心」的查看权限。'],
          ['系统状态', '网页同步和指标同步的最近时间与状态、代理和任务数量、当前使用的美元汇率。'],
        ],
      },
      {
        type: 'note',
        tone: 'info',
        text: '近 24 小时的消费是由每 10 分钟一次的用量同步推算的，和按模型账单的口径不同，两者会有小的出入。',
      },
      { type: 'link', text: '指标同步显示「平台未开通」', topic: 'faq-metrics-not-enabled' },
      { type: 'link', text: '美元金额是怎么来的', topic: 'faq-usd-amount' },
    ],
  },
  {
    id: 'feedback',
    group: 'manual',
    title: '反馈问题和建议',
    summary: '遇到问题或有想法，随手提一条；处理进度和回复都能看到。',
    icon: 'feedback',
    keywords: ['用户之声', '反馈', '建议', '报错', '提问题', '投诉'],
    blocks: [
      {
        type: 'p',
        text: '使用中遇到报错、卡住、结果不对，或者觉得哪里可以更好，都可以直接在系统里提出来，不用另外找人。',
      },
      {
        type: 'steps',
        items: [
          '点页面顶部右侧的「反馈」。任何页面都可以点。看到报错提示时，也可以点提示里的「反馈这个问题」，错误码和请求编号会自动带上。',
          '选择类型（遇到问题、功能建议、其他），在「描述」里写清楚你在做什么、看到了什么、原本希望怎样。标题可以不填。',
          '点「提交反馈」。提交后在「用户之声」里可以看到这条反馈、它的状态和管理员的回复。',
        ],
      },
      {
        type: 'table',
        head: ['状态', '含义'],
        rows: [
          ['待处理', '已收到，还没有人处理。'],
          ['处理中', '管理员已经在看，可能已有回复。'],
          ['已解决', '问题已处理或建议已采纳，回复里会说明。'],
          ['不处理', '暂时不做或不是问题，回复里会说明原因。'],
        ],
      },
      {
        type: 'note',
        tone: 'info',
        text: '反馈会自动附上你所在的页面、浏览器信息，以及从报错处进入时的错误码和请求编号。不会附带页面上的表单内容、搜索词、密码或凭证。',
      },
      {
        type: 'note',
        tone: 'info',
        text: '普通用户只能看到自己提交的反馈。有「处理用户反馈」权限的人可以看到全部，并回复和修改状态。',
      },
    ],
  },
  {
    id: 'faq-supplier-not-authorized',
    group: 'faq',
    title: '上号失败：「当前供应商未获授权该厂商」',
    summary: '平台没有授权这条连接接收该类账号：换连接，或找供应商开通。',
    area: 'onboard',
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
    summary: '只有启用且配置了 Token 的平台连接才能选。',
    area: 'onboard',
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
    summary: '直接点「打开授权页」，或手动选中链接复制。',
    area: 'onboard',
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
    summary: '链接超过 30 分钟、用过或被替换都会失效，重新获取即可。',
    area: 'onboard',
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
    summary: '密码、套餐日期、设备标识这些授权不提供，需要手动补。',
    area: 'onboard',
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
    summary: '请求发出但没收到明确结果：先到平台核实，不要重复提交。',
    area: 'onboard',
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
          '平台上已有：等待下一次网页同步（约 10 分钟一次），系统会按邮箱自动把它和本地账号关联。平台上没有：在详情里删除本地账号后重新录入上号（删除不会清掉平台侧记录）。',
        ],
      },
    ],
  },
  {
    id: 'faq-connection-in-use',
    group: 'faq',
    title: '删除平台连接时提示「仍有关联账号、任务或历史账单」',
    summary: '连接下还有账号或账单时不能删除，改为停用。',
    area: 'connection',
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
    summary: '后台登录连续失败 3 次会暂停同步，重新保存密码后恢复。',
    area: 'connection',
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
    summary: '供应商还没开通查询权限，不是故障，不影响上号。',
    area: 'money',
    keywords: ['PLATFORM_QUERY_DENIED', '指标同步', '未开通'],
    blocks: [
      { type: 'p', text: '表示供应商还没有为这条连接开通 OpenAPI 的查询权限。这不是故障，也不影响上号。' },
      { type: 'p', text: '账号状态、用量和消费数据来自「网页同步」，只要网页同步显示正常，仪表盘上的数字就是最新的。' },
    ],
  },
  {
    id: 'faq-usd-amount',
    group: 'faq',
    title: '卡片上的美元金额是怎么来的',
    summary: '美元 = 平台报的人民币金额 ÷ 平台固定汇率，目前是 7.2。',
    area: 'money',
    keywords: ['美元', '美金', 'USD', '汇率', '人民币', '累计花费', '换算', '7.2'],
    blocks: [
      {
        type: 'p',
        text: '账号卡片的「累计花费」和仪表盘上的金额按美元显示。平台报给系统的是人民币金额，系统用汇率换算：美元 = 人民币 ÷ 汇率；还没有配置汇率时显示「—」。「平台离线账单」里仍然是平台报的人民币金额。',
      },
      {
        type: 'list',
        items: [
          '汇率是供应商平台自己的固定汇率，不是市场汇率。平台按美元原价计费，再乘以固定汇率折算成人民币，所以按同一个汇率除回去就是美元原价。平台后台的卡片也是这样显示的。',
          '目前平台的汇率是 1 美元 = 7.2 人民币（2026 年 9 月由 7.3 调整为 7.2）。',
          '平台的接口不返回汇率，所以它保存在「平台设置」里每条连接的「美元汇率」上，默认 7.2。平台再次调整时，请管理员在那里修改，卡片会立即按新汇率换算。',
        ],
      },
      { type: 'link', text: '平台连接设置', topic: 'connections' },
    ],
  },
  {
    id: 'faq-cost-pending',
    group: 'faq',
    title: '今日消费为什么标着「未出齐」',
    summary: '账单约有 2 小时延迟，当天的金额第二天才完整。',
    area: 'money',
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
    summary: '平台上的资料在你打开之后变了，重新打开再改一次。',
    area: 'account',
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
    summary: '先检查账号和密码；忘记密码请管理员重置。',
    area: 'account',
    keywords: ['登录', '密码', '忘记密码', '修改密码'],
    blocks: [
      {
        type: 'list',
        items: [
          '先确认账号和密码没有多余的空格，注意大小写。',
          '连续输错后系统会要求等待一段时间再试，等待时间会逐次变长。',
          '忘记密码：请管理员在「用户与权限」里为你重置。',
          '修改自己的密码：点左下角账号旁边的钥匙图标。修改后其他设备上的登录会失效。',
        ],
      },
    ],
  },
]

function blockTexts(block: HelpBlock): string[] {
  switch (block.type) {
    case 'steps':
    case 'list':
      return block.items
    case 'code':
      return [block.label || '', block.text]
    case 'flow':
      return block.steps.flatMap((step) => [step.title, step.text])
    case 'table':
      return [...block.head, ...block.rows.flat()]
    default:
      return [block.text]
  }
}

/** All searchable text of a topic. */
export function topicText(topic: HelpTopic): string {
  return [topic.title, topic.summary, ...(topic.keywords || []), ...topic.blocks.flatMap(blockTexts)].join('\n')
}

/** Topics whose title, keywords or text contain the query (case-insensitive). */
export function searchTopics(topics: HelpTopic[], query: string): HelpTopic[] {
  const q = query.trim().toLowerCase()
  if (!q) return topics
  return topics.filter((t) => topicText(t).toLowerCase().includes(q))
}

export interface SearchHit {
  topic: HelpTopic
  /** Text around the first match in the body; `match` is empty when only the title or a keyword matched. */
  before: string
  match: string
  after: string
}

const HIT_BEFORE = 28
const HIT_AFTER = 40

/** Matching topics with the text around the first match, for a results list. */
export function searchHits(topics: HelpTopic[], query: string): SearchHit[] {
  const q = query.trim().toLowerCase()
  if (!q) return []
  return searchTopics(topics, q).map((topic) => {
    for (const text of [topic.summary, ...topic.blocks.flatMap(blockTexts)]) {
      const at = text.toLowerCase().indexOf(q)
      if (at < 0) continue
      const start = Math.max(0, at - HIT_BEFORE)
      const end = Math.min(text.length, at + q.length + HIT_AFTER)
      return {
        topic,
        before: (start > 0 ? '…' : '') + text.slice(start, at),
        match: text.slice(at, at + q.length),
        after: text.slice(at + q.length, end) + (end < text.length ? '…' : ''),
      }
    }
    return { topic, before: topic.summary, match: '', after: '' }
  })
}

/** Splits text on 「…」 so the names of buttons and fields can be set apart. */
export function richParts(text: string): { text: string; ui: boolean }[] {
  const parts: { text: string; ui: boolean }[] = []
  let last = 0
  for (const m of text.matchAll(/「([^「」]+)」/g)) {
    if (m.index > last) parts.push({ text: text.slice(last, m.index), ui: false })
    parts.push({ text: m[1]!, ui: true })
    last = m.index + m[0].length
  }
  if (last < text.length) parts.push({ text: text.slice(last), ui: false })
  return parts
}

/** The topics before and after `id` in reading order. */
export function neighbours(id: string): { previous: HelpTopic | null; next: HelpTopic | null } {
  const at = helpTopics.findIndex((t) => t.id === id)
  if (at < 0) return { previous: null, next: null }
  return { previous: helpTopics[at - 1] ?? null, next: helpTopics[at + 1] ?? null }
}
