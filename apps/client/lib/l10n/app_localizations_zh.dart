// ignore: unused_import
import 'package:intl/intl.dart' as intl;
import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for Chinese (`zh`).
class AppLocalizationsZh extends AppLocalizations {
  AppLocalizationsZh([String locale = 'zh']) : super(locale);

  @override
  String get appTitle => 'BiuMind';

  @override
  String get navChat => '聊天';

  @override
  String get navWiki => '知识库';

  @override
  String get navGraph => '图谱';

  @override
  String get navMemory => '记忆';

  @override
  String get navCode => '编码';

  @override
  String get navSkills => '技能';

  @override
  String get navSettings => '设置';

  @override
  String get skillsTitle => '技能管理';

  @override
  String get skillsRefresh => '刷新';

  @override
  String get skillsAdd => '添加';

  @override
  String get skillsFilterAll => '全部';

  @override
  String get skillsFilterBundled => '内置';

  @override
  String get skillsFilterOrg => '组织';

  @override
  String get skillsFilterMy => '我的';

  @override
  String get skillsFilterMarketplace => '市场';

  @override
  String get skillsSelectHint => '选一个技能查看详情';

  @override
  String get skillsEmpty => '暂无技能 — 点 + 添加 安装一个';

  @override
  String get skillsConfigureHint => '请先登录后管理云端技能';

  @override
  String get skillsPermissions => '权限';

  @override
  String get skillsAutoAttachPaths => '自动挂载路径';

  @override
  String get skillsBody => 'SKILL.md 主体';

  @override
  String get skillsDelete => '删除';

  @override
  String get skillsCancel => '取消';

  @override
  String get skillsInstall => '安装';

  @override
  String get skillsConfirmDeleteTitle => '删除技能？';

  @override
  String skillsConfirmDeleteBody(Object name) {
    return '永久删除 \"$name\"？此操作无法撤销。';
  }

  @override
  String get skillsInstallURL => 'URL';

  @override
  String get skillsInstallURLHint => '服务端通过 HTTPS 拉取 SKILL.md';

  @override
  String get skillsInstallZip => 'Zip';

  @override
  String get skillsInstallZipPick => '选择 .biuskill / .zip…';

  @override
  String get skillsInstallZipHint => '包以 base64 上传（≤ 8 MB）';

  @override
  String get skillsInstallInline => '手写';

  @override
  String get skillsInlineIdentifier => '标识';

  @override
  String get skillsInlineName => '名称';

  @override
  String get skillsInlineDescription => '描述';

  @override
  String get skillsInlineBody => 'SKILL.md 主体';

  @override
  String get skillsErrURLRequired => '请输入 URL';

  @override
  String get skillsErrZipRequired => '请选择 .biuskill / .zip 文件';

  @override
  String get skillsErrInlineRequired => '标识、名称、描述、主体都必填';

  @override
  String get skillsErrTooLarge => '上传过大 — 服务端上限 8 MB';

  @override
  String get chatPickSkills => '选择技能';

  @override
  String get chatPickSkillsFilter => '筛选…';

  @override
  String get chatPickSkillsEmpty => '没有可用技能 — 去技能管理安装';

  @override
  String get chatPickSkillsClear => '清空';

  @override
  String chatPickSkillsDone(Object count) {
    return '完成（$count）';
  }

  @override
  String get chatComposerSkillsTooltip => '选择技能（@）';

  @override
  String get skillsFilterPending => '待审';

  @override
  String get skillsApprove => '批准';

  @override
  String get skillsReject => '拒绝';

  @override
  String get skillsRejectReason => '原因（可选）';

  @override
  String get codeWorkbenchTitle => '编码工作台';

  @override
  String get codeNoTaskHint => '在左侧选择一个任务，或新建一个开始。';

  @override
  String get codeNewTask => '新建任务';

  @override
  String get codeTabAgent => 'Agent';

  @override
  String get codeTabTerminal => '终端';

  @override
  String get codeTabPr => 'PR 预览';

  @override
  String get codeTabCompare => '任务对比';

  @override
  String get codeRightFiles => '文件';

  @override
  String get codeRightDiff => 'Diff';

  @override
  String get codeRightGit => 'Git';

  @override
  String get codeRightHooks => 'Hooks';

  @override
  String get codeStatusBarHint => '编码工作台';

  @override
  String get codeUsageTooltip => '用量';

  @override
  String get codeUsageLoading => '正在读取用量…';

  @override
  String get codeUsageFailed => '读取用量失败';

  @override
  String get codeUsageNoWindows => '暂无用量数据';

  @override
  String get codeUsageFiveHour => '5 小时';

  @override
  String get codeUsageSevenDay => '7 天';

  @override
  String get codeUsagePrimary => '主额度';

  @override
  String get codeUsageSecondary => '次额度';

  @override
  String get codeUsageLeft => '剩余';

  @override
  String get codeUsageRefresh => '刷新';

  @override
  String get memoryTitle => '记忆';

  @override
  String get memoryProjectMissing => '（未选项目）';

  @override
  String get memoryHintNoCreds => '请先在「设置」中配置 model-relay URL 和令牌。';

  @override
  String get memoryHintNoProject => '请先在「知识库」中打开或创建一个项目。';

  @override
  String get memoryHintEmptyList => '还没有任何记忆 — 在下方添加一条。';

  @override
  String get memoryHintEmptyRecall => '没有匹配结果，换个关键词试试。';

  @override
  String get memoryListTab => '列表';

  @override
  String get memoryRecallTab => '召回';

  @override
  String get memoryFilterAll => '全部';

  @override
  String get memoryKindRecall => '记忆';

  @override
  String get memoryKindPreference => '偏好';

  @override
  String get memoryKindHabit => '习惯';

  @override
  String get memoryRecallQueryHint => '输入召回关键词…';

  @override
  String get memoryAddHint => '记住这条…';

  @override
  String get memoryAddButton => '添加';

  @override
  String get memoryRefresh => '刷新';

  @override
  String get memoryModeHybrid => '混合召回（语义 + 关键词）';

  @override
  String get memoryModeLexical => '仅关键词';

  @override
  String memorySubtitle(
    Object kind,
    Object salience,
    Object scoreSuffix,
    Object when,
  ) {
    return '类型=$kind · 重要度=$salience$scoreSuffix · $when';
  }

  @override
  String relTimeSeconds(Object seconds) {
    return '$seconds 秒前';
  }

  @override
  String relTimeMinutes(Object minutes) {
    return '$minutes 分钟前';
  }

  @override
  String relTimeHours(Object hours) {
    return '$hours 小时前';
  }

  @override
  String relTimeDays(Object days) {
    return '$days 天前';
  }

  @override
  String relTimeMonths(Object months) {
    return '$months 个月前';
  }

  @override
  String get settingsTitle => '设置';

  @override
  String get settingsModeHub => 'model-relay 模式';

  @override
  String get settingsModeBYOK => 'BYOK（自带 API Key）';

  @override
  String get settingsModeOffline => '离线';

  @override
  String get settingsHubUrl => 'model-relay 地址';

  @override
  String get settingsRelayToken => 'model-relay 令牌';

  @override
  String get settingsTestConnection => '测试连接';

  @override
  String get settingsSave => '保存';

  @override
  String get settingsSavedSnack => '设置已保存';

  @override
  String get signInTitle => '登录';

  @override
  String get signInSubtitle => '登录你的 BiuMind 账号。';

  @override
  String get signInIdentityUrl => '服务器地址';

  @override
  String get signInAdvanced => '高级';

  @override
  String get signInVerifyCode => '6 位验证码';

  @override
  String get signInEmail => '邮箱';

  @override
  String get signInPassword => '密码';

  @override
  String get signInSubmit => '登录';

  @override
  String get signInRegister => '注册';

  @override
  String signInSignOut(Object email) {
    return '退出（$email）';
  }

  @override
  String signInOk(Object email) {
    return '✓ 已登录：$email';
  }

  @override
  String registerOk(Object email) {
    return '✓ 注册成功：$email';
  }

  @override
  String signInTokenExpires(Object when) {
    return '令牌过期时间：$when';
  }

  @override
  String get signInErrInvalidCredentials => '邮箱或密码错误。';

  @override
  String get signInErrEmailTaken => '该邮箱已被注册。';

  @override
  String get signInErrInvalidEmail => '邮箱格式不正确。';

  @override
  String get signInErrPasswordTooShort => '密码太短,至少 8 位。';

  @override
  String get signInErrNetwork => '无法连接服务器,请检查网络。';

  @override
  String get signInErrUnknown => '登录失败,请重试。';

  @override
  String signInCodeSent(String email) {
    return '验证码已发送至 $email, 请查收 (10 分钟内有效)';
  }

  @override
  String get signInCodeDevMode => '验证码已生成. 当前为开发模式: SMTP 未配置, 请联系管理员从服务日志获取验证码';

  @override
  String signInEnterCodeSentTo(String email) {
    return '请输入发送至 $email 的 6 位验证码';
  }

  @override
  String get signInEnterCode => '请输入 6 位验证码';

  @override
  String signInCodeResent(String email) {
    return '已重新发送验证码至 $email';
  }

  @override
  String get signInCodeRegenerated => '已生成新验证码. 当前为开发模式: 请从服务日志获取';

  @override
  String get signInForgotHint => '输入注册邮箱, 我们将发送 6 位重置验证码';

  @override
  String get signInErrEmailRequired => '请填写注册邮箱';

  @override
  String signInResetCodeSent(String email) {
    return '若该邮箱已注册, 验证码已发送至 $email (10 分钟内有效)';
  }

  @override
  String get signInResetCodeDevMode => '验证码已生成. 当前为开发模式: 请联系管理员从服务日志获取';

  @override
  String get signInErrNewPasswordShort => '新密码至少 8 位';

  @override
  String get signInPasswordReset => '密码已重置, 请使用新密码登录';

  @override
  String get signInNewPasswordLabel => '新密码 (≥ 8 位)';

  @override
  String get signInForgotPassword => '忘记密码?';

  @override
  String get signInNoAccount => '没有账号?';

  @override
  String get signInSendResetCode => '发送重置验证码';

  @override
  String get signInBackToSignIn => '返回登录';

  @override
  String get signInResetPassword => '重置密码';

  @override
  String signInResendCooldown(int seconds) {
    return '重新发送 (${seconds}s)';
  }

  @override
  String get signInResendCode => '重新发送验证码';

  @override
  String get signInSubtitleVerify => '验证您的邮箱以激活账户';

  @override
  String get signInSubtitleForgot => '忘记密码 — 我们将通过邮件发送重置码';

  @override
  String get signInSubtitleReset => '输入验证码 + 新密码完成重置';

  @override
  String get signInVerifySubmit => '验证并登录';

  @override
  String get signInErrInvalidCode => '验证码错误, 请重试';

  @override
  String get signInErrCodeExpired => '验证码已过期, 请点击重新发送';

  @override
  String get signInErrCodeLocked => '验证错误次数过多, 请重新发送验证码';

  @override
  String get signInErrCodeUsed => '该验证码已被使用, 请重新发送';

  @override
  String get signInErrNoPendingCode => '尚未发送验证码, 请先点击重新发送';

  @override
  String get signInErrRateLimited => '发送过于频繁, 请稍后再试';

  @override
  String get settingsModeSection => '模式';

  @override
  String get settingsModeSectionSubtitle => 'AI 调用走哪条路径。';

  @override
  String get settingsHubSection => 'model-relay 连接';

  @override
  String get settingsHubSectionSubtitle =>
      'Cloud / BYO Endpoint 模式使用。令牌由上方\"登录\"自动填入；';

  @override
  String get settingsBYOKSection => 'BYOK 厂商密钥';

  @override
  String get settingsBYOKSectionSubtitle =>
      '加密存储在系统钥匙串。Direct 模式直连用，或在 model-relay 转发模式下作回退。';

  @override
  String get settingsAccountSection => '账号';

  @override
  String get settingsAccountSectionSubtitle => '登录后会话历史在多设备间自动同步。';

  @override
  String get settingsServiceUrl => '服务地址';

  @override
  String get settingsProvidersSection => '模型供应商';

  @override
  String get settingsProvidersSectionSubtitle =>
      '配置你能调用的模型供应商。API key 加密存储在服务端。';

  @override
  String get settingsChatDefaultsSection => '聊天默认';

  @override
  String get settingsChatDefaultsSectionSubtitle => '新建会话使用的默认模型。';

  @override
  String get settingsDefaultModel => '默认模型';

  @override
  String get settingsAddProvider => '添加供应商';

  @override
  String get settingsAddCustomProvider => '添加自定义供应商';

  @override
  String get settingsCustomProviderId => '供应商 ID (slug)';

  @override
  String get settingsCustomProviderName => '显示名称';

  @override
  String get settingsConfigure => '配置';

  @override
  String get settingsRemove => '移除';

  @override
  String get settingsApiKey => 'API key';

  @override
  String get settingsBaseUrlOptional => 'Base URL (可选)';

  @override
  String get settingsFetchModeLabel => '调用方式';

  @override
  String get settingsFetchModeServer => '服务端中转';

  @override
  String get settingsFetchModeServerDesc => '由 Brain 代为调用,客户端最省事。';

  @override
  String get settingsFetchModeClient => '客户端直连';

  @override
  String get settingsFetchModeClientDesc => '本机直接调用 LLM。Key 仅当本机请求时才下发。';

  @override
  String get settingsProviderConfigured => '已配置';

  @override
  String get settingsProviderNotConfigured => '未配置';

  @override
  String get settingsGroupGeneral => '通用';

  @override
  String get settingsGroupAgent => '智能体';

  @override
  String get settingsGroupSystem => '系统';

  @override
  String get settingsNavStatistics => '数据统计';

  @override
  String get settingsNavAppearance => '外观';

  @override
  String get settingsNavShortcuts => '快捷键';

  @override
  String get settingsNavProviders => 'AI 服务商';

  @override
  String get settingsNavDefaultModel => '服务模型';

  @override
  String get settingsNavSkills => '技能管理';

  @override
  String get settingsNavMemory => '记忆设置';

  @override
  String get settingsNavCredentials => '凭证管理';

  @override
  String get settingsNavCodingWorkbench => '编码工作台';

  @override
  String get settingsNavChat => '聊天平台';

  @override
  String get settingsNavProxy => '网络代理';

  @override
  String get settingsNavStorage => '数据存储';

  @override
  String get settingsNavApiKey => 'API Key';

  @override
  String get settingsNavAdvanced => '高级设置';

  @override
  String get settingsNavAbout => '关于';

  @override
  String get settingsProviderSearchHint => '搜索服务商…';

  @override
  String get settingsProvidersAll => '全部';

  @override
  String get settingsProvidersEnabled => '已启用';

  @override
  String get settingsProvidersDisabled => '未启用';

  @override
  String get settingsConnectivityCheck => '连通性检查';

  @override
  String get settingsCheckButton => '检查';

  @override
  String get settingsCheckOk => '连接正常';

  @override
  String get settingsCheckSaveFirst => '请先保存 API key 再检查。';

  @override
  String get settingsConfigureFirstHint => '此服务商尚未配置,在下方填写 API key 后保存。';

  @override
  String get settingsEncryptionNote => '你的 API key 与代理地址使用 AES-256-GCM 加密存储。';

  @override
  String get settingsModelList => '模型列表';

  @override
  String get settingsModelRefresh => '获取模型列表';

  @override
  String get settingsModelEmpty => '暂无模型,先配置 API key 再刷新。';

  @override
  String get settingsModelTabAll => '全部';

  @override
  String get settingsModelTabChat => '对话';

  @override
  String get settingsModelTabImage => '图片';

  @override
  String get settingsModelTabVideo => '视频';

  @override
  String get settingsOfficialBadge => 'BiuMind 官方渠道';

  @override
  String get settingsOfficialDescription =>
      '使用 BiuMind 精选的平台模型,无需自备 API key,登录即可使用。调用通过我们的资源池,按量计费或订阅会员制。';

  @override
  String get settingsOfficialBilling => '计费方式:按量(按 token 计)或包月会员。在账户中管理订阅。';

  @override
  String get themeSystem => '跟随系统';

  @override
  String get themeLight => '浅色';

  @override
  String get themeDark => '深色';

  @override
  String get settingsAppearanceColorTheme => '颜色主题';

  @override
  String get settingsAppearanceColorThemeRecommended => '推荐';

  @override
  String get settingsAppearanceFontSize => '字体大小';

  @override
  String get settingsAppearanceFontSizeSmall => '小';

  @override
  String get settingsAppearanceFontSizeMedium => '中';

  @override
  String get settingsAppearanceFontSizeLarge => '大';

  @override
  String get settingsAppearanceFontSizeHint => '字体大小同时影响间距和列表密度';

  @override
  String get settingsAppearancePreviewTitle => 'Hello, BiuMind';

  @override
  String get settingsAppearancePreviewBody => '实时预览 — 选你看着最舒服的尺寸';

  @override
  String get settingsAppearanceMode => '模式';

  @override
  String get paletteNamePurpleOrange => '紫 + 橘';

  @override
  String get paletteDescPurpleOrange => '默认 — 智慧紫 + 行动橘';

  @override
  String get paletteNamePurple => '紫';

  @override
  String get paletteDescPurple => '原品牌延续';

  @override
  String get paletteNamePurpleBlue => '紫 + 蓝';

  @override
  String get paletteDescPurpleBlue => '理性,工程师风';

  @override
  String get paletteNamePurplePink => '紫 + 粉';

  @override
  String get paletteDescPurplePink => '暖,表达力强';

  @override
  String get paletteNamePurpleEmerald => '紫 + 翡翠';

  @override
  String get paletteDescPurpleEmerald => '平衡稀有组合';

  @override
  String get paletteNameAurora => '极光';

  @override
  String get paletteDescAurora => '青-紫-粉,记忆点最强';

  @override
  String get paletteNameSunset => '日落';

  @override
  String get paletteDescSunset => '橙-粉-紫,暖能量';

  @override
  String get paletteNameCyber => '赛博';

  @override
  String get paletteDescCyber => '青-紫-品红,科技未来感';

  @override
  String get paletteNameOcean => '海洋';

  @override
  String get paletteDescOcean => '蓝-青,冷静 B 端';

  @override
  String get paletteNameEmeraldGold => '翡翠金';

  @override
  String get paletteDescEmeraldGold => '高端,金融感';

  @override
  String get paletteNameRose => '玫瑰';

  @override
  String get paletteDescRose => '感性,活力';

  @override
  String get paletteNameOnyx => '黑曜青';

  @override
  String get paletteDescOnyx => '极简专业';

  @override
  String get paletteNameInkblueOrange => '墨蓝 + 信号橙';

  @override
  String get paletteDescInkblueOrange => 'Vercel 风极简';

  @override
  String get paletteNameQuantumTitanium => '量子紫 + 钛金';

  @override
  String get paletteDescQuantumTitanium => 'Vision Pro 风高端';

  @override
  String get paletteNameClaudeWarm => 'Claude 暖';

  @override
  String get paletteDescClaudeWarm => '反共识温暖';

  @override
  String get paletteNameGraphiteCyan => '石墨 + 量子青';

  @override
  String get paletteDescGraphiteCyan => 'Cursor 风工程师';

  @override
  String get paletteNameIndigoSand => '靛青 + 暖砂';

  @override
  String get paletteDescIndigoSand => '2026 趋势,平衡';

  @override
  String get paletteNameWikiGreen => 'Wiki 翠绿';

  @override
  String get paletteDescWikiGreen => '沉稳绿调,适合笔记阅读';

  @override
  String get aboutSubtitle => '关于 BiuMind';

  @override
  String get aboutVersion => '0.1.0';

  @override
  String get aboutBuild => '构建';

  @override
  String get aboutTagline => '你的 AI 工作台 — 聊天、知识库、记忆,一站式。';

  @override
  String get settingsCheckUpdate => '检查更新';

  @override
  String get settingsCheckUpdateLatest => '已是最新版本';

  @override
  String get settingsCheckUpdateChecking => '检查中…';

  @override
  String get settingsCheckUpdateAvailable => '发现新版本';

  @override
  String get settingsFetchNightly => '获取开发版';

  @override
  String get settingsFetchNightlySubtitle => '未签名·可能不稳定·数据请先备份';

  @override
  String get settingsAppearanceSection => '外观';

  @override
  String get settingsAppearanceSectionSubtitle => '当前主题跟随系统；主题切换器留在 P3.6。';

  @override
  String get settingsBearerToken => 'Bearer 令牌';

  @override
  String get settingsBearerTokenHint => 'JWT 或虚拟密钥（bk-live-…）';

  @override
  String get settingsAnthropicKey => 'Anthropic API key';

  @override
  String get settingsOpenAIKey => 'OpenAI API key';

  @override
  String get settingsTheme => '主题';

  @override
  String get settingsModeCloudTitle => '云端（默认）';

  @override
  String get settingsModeCloudDesc =>
      '全部 AI 调用走 BiuMind model-relay。平台计费 + 审计。';

  @override
  String get settingsModeBYOEndpointTitle => '自带 Endpoint';

  @override
  String get settingsModeBYOEndpointDesc =>
      '通过 model-relay，但 model-relay 转发到你的私有端点（Azure / Vertex / Ollama）。';

  @override
  String get settingsModeDirectTitle => '直连（独立）';

  @override
  String get settingsModeDirectDesc => '绕过 model-relay，使用上方 BYOK 密钥直接从设备调 LLM。';

  @override
  String get chatTitle => '聊天';

  @override
  String get chatHint => '输入消息…';

  @override
  String get chatSend => '发送';

  @override
  String get chatNewThread => '新会话';

  @override
  String get chatUntitled => '(未命名)';

  @override
  String get chatErrNotSignedIn => '未登录,请到设置中连接服务。';

  @override
  String get chatErrSettingsLoading => '设置加载中,稍后再试。';

  @override
  String get chatErrDirectUnsupported => '尚未支持直连模式。';

  @override
  String get chatErrDirectNoKey => '未配置 Anthropic API key,请到设置中填写。';

  @override
  String get chatErrNetwork => '网络错误,检查连接。';

  @override
  String get chatErrAuth => '登录已过期,请重新登录。';

  @override
  String get chatV2SettingsTitle => 'Chat 偏好';

  @override
  String get chatV2SettingsResetAll => '恢复默认';

  @override
  String get chatV2SettingsResetConfirmTitle => '恢复默认偏好';

  @override
  String get chatV2SettingsResetConfirmBody =>
      '字号 / 默认模式 / 默认模型 / 语言 都会恢复到出厂值。这次重置不可撤销。';

  @override
  String get chatV2SettingsResetButton => '恢复';

  @override
  String get chatV2SettingsCancel => '取消';

  @override
  String get chatV2SettingsClose => '关闭';

  @override
  String get chatV2SettingsFontScale => '字号';

  @override
  String get chatV2SettingsFontScaleHint => '影响消息气泡内文字的大小';

  @override
  String get chatV2SettingsDefaultMode => '默认对话模式';

  @override
  String get chatV2SettingsAutoRenameTitle => '自动从首句 prompt 起标题';

  @override
  String get chatV2SettingsAutoRenameSubtitle => '关掉后侧边栏会保留\"新对话\"占位，等你手动改名';

  @override
  String get chatV2SettingsDefaultModel => '默认模型（chat 模式）';

  @override
  String get chatV2SettingsDefaultModelDefault => 'BiuMind 默认（不指定）';

  @override
  String get chatV2SettingsTtsTitle => '语音朗读';

  @override
  String get chatV2SettingsTtsHint =>
      '选一个云端语音模型，朗读走 model-relay 高质量合成；未配置或云端失败时回落设备本地语音。';

  @override
  String get chatV2SettingsTtsModel => '朗读模型';

  @override
  String get chatV2SettingsTtsModelLocal => '设备本地语音（离线、免费）';

  @override
  String get chatV2SettingsTtsVoice => '音色 ID';

  @override
  String get chatV2SettingsTtsVoiceHint => '如 longanyang（cosyvoice 系统音色）';

  @override
  String get chatV2SettingsTtsNoModels => '尚无语音模型。请先在渠道里添加一个 audio_speech 模型。';

  @override
  String get chatV2SettingsLanguage => '界面语言';

  @override
  String get chatV2SettingsLanguageSystem => '跟随系统';

  @override
  String get chatV2SettingsLanguageZh => '中文';

  @override
  String get chatV2SettingsLanguageEn => 'English';

  @override
  String get chatV2AppBarPinTooltip => '置顶';

  @override
  String get chatV2AppBarUnpinTooltip => '取消置顶';

  @override
  String get chatV2AppBarStopTooltip => '停止生成';

  @override
  String get chatV2AppBarStoppingTooltip => '停止中…';

  @override
  String get chatV2AppBarSearchTooltip => '在对话中搜索 (Cmd/Ctrl+F)';

  @override
  String get chatV2AppBarMultiSelectTooltip => '多选消息';

  @override
  String get chatV2AppBarShortcutsTooltip => '键盘快捷键 (?)';

  @override
  String get chatV2AppBarSettingsTooltip => '对话设置（System Prompt 等）';

  @override
  String get chatV2AppBarMore => '更多';

  @override
  String get chatV2AppBarStreaming => '生成中';

  @override
  String get chatV2AppBarStopping => '停止中…';

  @override
  String get chatV2HeroSubtitle => '今天想做点什么？挑一个起点开始，或者 ';

  @override
  String get chatV2HeroNewBlank => '新建空白对话';

  @override
  String get chatV2HeroSkillsLabel => '我的技能';

  @override
  String get chatV2HeroRecentLabel => '最近对话';

  @override
  String get chatV2HeroRecentModelsLabel => '最近用过';

  @override
  String get chatV2HeroKvLabel => '本月数据';

  @override
  String get chatV2HeroKvMonthMessages => '本月对话';

  @override
  String get chatV2HeroKvCredits => '余下积分';

  @override
  String get chatV2HeroKvStreak => '连击天数';

  @override
  String chatV2HeroSetDefaultModel(Object model) {
    return '已设默认模型：$model';
  }

  @override
  String chatV2HeroStatsThreads(Object messages, Object threads) {
    return '已聊 $messages 条 · $threads 个对话';
  }

  @override
  String chatV2HeroStatsThisWeek(Object active, Object messages) {
    return '本周 $messages 条 · $active 活跃';
  }

  @override
  String chatV2HeroStatsRecentDays(
    Object active,
    Object days,
    Object messages,
  ) {
    return '近 $days 天 $messages 条 · $active 活跃';
  }

  @override
  String chatV2HeroStreakChip(Object n) {
    return '$n 天连击';
  }

  @override
  String chatV2HeroStreakTooltip(Object n) {
    return '连续 $n 天有对话活动';
  }

  @override
  String chatV2HeroStatsSwitchTooltip(
    Object active,
    Object days,
    Object messages,
  ) {
    return '最近 $days 天发了 $messages 条消息，分布在 $active 个对话\n点击切换 7 / 30 天视图';
  }

  @override
  String get chatV2ComposerHint => '今天帮你做些什么？@ 引用技能，/ 调用指令';

  @override
  String get chatV2ComposerDisclaimer => '内容由 AI 生成，请核实重要信息';

  @override
  String get chatV2ComposerHintStreaming => '生成中…';

  @override
  String get chatV2ComposerAttachTooltip => '附加图片（也可拖入 / 粘贴）';

  @override
  String get chatV2ComposerAttachNeedThread => '需先选对话';

  @override
  String get chatV2ComposerAttachCamera => '拍照';

  @override
  String get chatV2ComposerAttachGallery => '从相册选择';

  @override
  String get chatV2ComposerAttachFile => '选择文件';

  @override
  String get chatV2ComposerWebOn => '联网搜索：已开启（一次性）';

  @override
  String get chatV2ComposerWebOff => '联网搜索：关闭 / 单击开启';

  @override
  String get chatV2ComposerWebSnack => '联网搜索：本轮发送后自动关闭';

  @override
  String get chatV2ComposerSendTooltip => '发送';

  @override
  String get chatV2ComposerCancelTooltip => '取消';

  @override
  String chatV2ComposerCharTokens(Object chars, Object tokens) {
    return '$chars 字  ·  ~$tokens tokens';
  }

  @override
  String get chatV2ComposerStopping => '正在停止…';

  @override
  String get chatV2ComposerStoppingTooltip => '正在停止…';

  @override
  String chatV2ComposerErrAttachOnlyImage(Object name) {
    return '暂只支持图片附件，跳过 $name';
  }

  @override
  String get chatV2ComposerErrAttachTooLarge => '单张图片大小 10MB 上限';

  @override
  String chatV2ComposerErrAttach(Object err) {
    return '附件错误: $err';
  }

  @override
  String get chatV2ComposerSlashDialogTitle => '斜杠命令';

  @override
  String get chatV2DialogOk => '好的';

  @override
  String get chatV2ComposerModelSwitchTooltip => '切换模型';

  @override
  String get chatV2ComposerModelDefault => 'BiuMind 默认';

  @override
  String get chatV2ComposerModelRefresh => '刷新模型列表';

  @override
  String get chatV2NewThreadFallback => '新对话';

  @override
  String get chatV2SidebarTitle => '对话';

  @override
  String get chatV2SidebarFilterHint => '过滤对话…';

  @override
  String get chatV2SidebarPaletteTooltip => '命令面板 (Cmd/Ctrl+K)';

  @override
  String get chatV2SidebarStarredTooltip => '收藏的消息';

  @override
  String get chatV2SidebarCrossSearchTooltip => '搜索全部对话 (Cmd/Ctrl+Shift+F)';

  @override
  String get chatV2SidebarImportTooltip => '导入对话 JSON';

  @override
  String get chatV2SidebarNewTooltip => '新建对话';

  @override
  String get chatV2SidebarSectionPinned => '置顶';

  @override
  String get chatV2SidebarSectionOthers => '其它';

  @override
  String get chatV2SidebarEmptyNew => '还没有对话\n点上方 + 新建';

  @override
  String get chatV2SidebarEmptyFiltered => '没有匹配的对话';

  @override
  String chatV2SidebarArchivedFooter(Object count) {
    return '已归档 $count';
  }

  @override
  String get chatV2SidebarBatchTooltip => '批量管理';

  @override
  String chatV2BatchSelectedCount(Object count) {
    return '已选 $count 个对话';
  }

  @override
  String get chatV2BatchSelectAll => '全选';

  @override
  String get chatV2BatchSelectNone => '取消全选';

  @override
  String get chatV2BatchDelete => '删除';

  @override
  String get chatV2BatchExitTooltip => '退出批量管理';

  @override
  String get chatV2BatchDeleteTitle => '批量删除对话';

  @override
  String chatV2BatchDeleteBody(Object count) {
    return '删除选中的 $count 个对话？该操作不可恢复。';
  }

  @override
  String chatV2BatchDeletedCount(Object count) {
    return '已删除 $count 个对话';
  }

  @override
  String chatV2LoadError(Object err) {
    return '加载失败：$err';
  }

  @override
  String chatV2ExportSuccess(Object name) {
    return '已导出 $name';
  }

  @override
  String chatV2ExportAllSuccess(Object name) {
    return '已导出全部对话 → $name';
  }

  @override
  String chatV2ExportFailed(Object err) {
    return '导出失败: $err';
  }

  @override
  String chatV2ImportSuccessCount(Object count) {
    return '已导入 $count 个对话';
  }

  @override
  String get chatV2ImportSuccess => '已导入对话';

  @override
  String chatV2ImportFailed(Object err) {
    return '导入失败: $err';
  }

  @override
  String chatV2ApplyTemplate(Object name) {
    return '已套用模板「$name」';
  }

  @override
  String get chatV2PaletteGroupOps => '操作';

  @override
  String get chatV2PaletteGroupCurrent => '当前对话';

  @override
  String get chatV2PaletteGroupSwitch => '切换对话';

  @override
  String get chatV2PaletteNewThread => '新建对话';

  @override
  String get chatV2PaletteNewThreadHint => '打开新建对话向导';

  @override
  String get chatV2PaletteCrossSearch => '搜索全部对话';

  @override
  String get chatV2PaletteStarred => '查看收藏的消息';

  @override
  String get chatV2PaletteStarredHint => '所有 ⭐ 消息跨 thread';

  @override
  String get chatV2PaletteDrafts => '查看草稿（半成品）';

  @override
  String get chatV2PaletteDraftsHint => '所有 thread 当前未发的输入';

  @override
  String get chatV2PaletteArchived => '查看归档对话';

  @override
  String get chatV2PaletteArchivedHint => '解归档 / 永久删除';

  @override
  String get chatV2PaletteExportAll => '导出全部对话';

  @override
  String get chatV2PaletteExportAllHint => '一次性 JSON 备份（含归档）';

  @override
  String get chatV2PaletteShortcuts => '查看键盘快捷键';

  @override
  String get chatV2PaletteShortcutsHint => 'Shift+? 也可';

  @override
  String get chatV2PaletteSettings => '打开 Chat 偏好设置';

  @override
  String get chatV2PaletteSettingsHint => '字号 / 默认模式 / 默认模型';

  @override
  String get chatV2PaletteMultiSelect => '多选当前对话消息';

  @override
  String get chatV2PaletteApplyTemplate => '应用 system prompt 模板';

  @override
  String get chatV2PaletteApplyTemplateHint => '从收藏挑一个套到当前对话';

  @override
  String get chatV2PaletteManageTemplates => '管理 System Prompt 模板';

  @override
  String get chatV2PaletteManageTemplatesHint => '增 / 改 / 删常用提示';

  @override
  String get chatV2PaletteSwitchHint => '切换到此对话';

  @override
  String get chatV2ThreadStatusGenerating => '生成中';

  @override
  String get chatV2ThreadStatusStopping => '停止中…';

  @override
  String get chatV2OverflowShareCopied => '已复制分享链接';

  @override
  String chatV2OverflowShareCopiedUrl(Object url) {
    return '已复制 $url';
  }

  @override
  String get chatV2OverflowIdCopied => '已复制 thread ID';

  @override
  String get chatV2NewDialogTitle => '新对话';

  @override
  String get chatV2NewDialogTitleField => '标题（可选）';

  @override
  String chatV2NewDialogTitleSuggested(Object suggested) {
    return '建议：$suggested';
  }

  @override
  String get chatV2NewDialogModelLabel => '模型';

  @override
  String get chatV2NewDialogRefreshTooltip => '刷新';

  @override
  String get chatV2NewDialogModelOfficial => 'BiuMind（官方默认）';

  @override
  String get chatV2NewDialogModelEmpty => '（无可用模型，请联系管理员）';

  @override
  String chatV2NewDialogModelLoadFailed(Object err) {
    return '模型列表加载失败：$err';
  }

  @override
  String get chatV2NewDialogSystemPromptLabel => '系统提示（可选）';

  @override
  String get chatV2NewDialogSystemPromptHint => '\"你是一个…\"，留空走默认';

  @override
  String get chatV2NewDialogPickWorker => '选择 worker';

  @override
  String get chatV2NewDialogNoOnlineDaemon => '当前无在线 daemon';

  @override
  String get chatV2NewDialogEmptyEnvAuto =>
      'biumind 桌面端会自动启动本机 biu serve，请稍候或检查 biu CLI 是否安装；也可手动 `biu serve` 起一台 daemon。';

  @override
  String chatV2NewDialogEmptyEnvHistory(Object count) {
    return 'Agent 模式需要 worker_kind=biu_daemon 的本机进程。历史 $count 台都不是 daemon 或离线。';
  }

  @override
  String chatV2NewDialogEnvLoadFailed(Object err) {
    return 'environment 列表拉取失败：$err';
  }

  @override
  String get chatV2NewDialogPoolTagLabel => 'runtime 池标签（可选）';

  @override
  String get chatV2NewDialogPoolTagHint =>
      '留空走默认池；填如 \"gpu\" / \"high-mem\" 选指定池';

  @override
  String get chatV2NewDialogTaskModeHint =>
      'Task 模式让 brain 把任务调度到匹配 pool_tag 的 runtime worker。没匹配的 worker 时任务会排队。';

  @override
  String get chatV2NewDialogCreate => '创建';

  @override
  String get chatV2NewDialogModeChat => 'Chat';

  @override
  String get chatV2NewDialogModeChatHint => '直接跟模型聊';

  @override
  String get chatV2NewDialogModeAgent => 'Agent';

  @override
  String get chatV2NewDialogModeAgentHint => '指定 worker 跑工具';

  @override
  String get chatV2NewDialogModeTask => 'Task';

  @override
  String get chatV2NewDialogModeTaskHint => '后台任务执行';

  @override
  String get chatV2ShortcutsTitle => '键盘快捷键';

  @override
  String get chatV2ShortcutsSectionInput => '输入框';

  @override
  String get chatV2ShortcutsSectionMessages => '消息流';

  @override
  String get chatV2ShortcutsSectionGlobal => '全局';

  @override
  String get chatV2ShortcutsSend => '发送消息';

  @override
  String get chatV2ShortcutsNewline => '换行（不发送）';

  @override
  String get chatV2ShortcutsHistoryUp => '调出上一条历史指令（输入为空时）';

  @override
  String get chatV2ShortcutsHistoryDown => '回到下一条 / 退出浏览';

  @override
  String get chatV2ShortcutsSlash => '弹出斜杠命令面板';

  @override
  String get chatV2ShortcutsEsc => '关闭命令面板 / 搜索栏';

  @override
  String get chatV2ShortcutsInThreadSearch => '在当前对话中搜索';

  @override
  String get chatV2ShortcutsSearchNext => '搜索栏：跳到下一条命中';

  @override
  String get chatV2ShortcutsSearchPrev => '搜索栏：跳到上一条命中';

  @override
  String get chatV2ShortcutsPalette => '打开命令面板';

  @override
  String get chatV2ShortcutsNewThread => '新建对话';

  @override
  String get chatV2ShortcutsCrossSearch => '搜索全部对话';

  @override
  String get chatV2ShortcutsModelPicker => '切换当前对话模型';

  @override
  String get chatV2ShortcutsHelp => '打开本帮助面板';

  @override
  String get chatV2ArchivedTitle => '归档对话';

  @override
  String get chatV2ArchivedClose => '关闭';

  @override
  String get chatV2ArchivedEmpty => '没有归档的对话';

  @override
  String get chatV2ArchivedUnarchive => '解归档';

  @override
  String get chatV2ArchivedHardDelete => '永久删除';

  @override
  String get chatV2ArchivedHardDeleteTitle => '永久删除';

  @override
  String chatV2ArchivedHardDeleteBody(Object title) {
    return '永久删除「$title」？该操作不可恢复。';
  }

  @override
  String get chatV2DraftsTitle => '草稿（半成品）';

  @override
  String get chatV2DraftsEmpty => '没有草稿。在任何对话里输入的文字会自动保存。';

  @override
  String get chatV2DraftsUnnamed => '(未命名 / 已删除)';

  @override
  String chatV2DraftsCharCount(Object count) {
    return '$count 字';
  }

  @override
  String get chatV2DraftsDiscard => '丢弃此草稿';

  @override
  String get chatV2StarredTitle => '收藏的消息';

  @override
  String get chatV2StarredEmpty => '还没有收藏的消息。点 assistant 消息底部的 ⭐ 按钮收藏。';

  @override
  String get chatV2StarredNoText => '(无文本内容)';

  @override
  String get chatV2CrossSearchHint => '在所有对话里搜索…';

  @override
  String chatV2CrossSearchHitCount(Object count) {
    return '$count 条';
  }

  @override
  String get chatV2CrossSearchCloseTooltip => '关闭 (Esc)';

  @override
  String get chatV2CrossSearchEmptyHint => '输入关键词搜索全部历史对话\n（按 Esc 关闭）';

  @override
  String get chatV2CrossSearchNoMatch => '没有命中';

  @override
  String get chatV2PaletteSearchHint => '搜索命令…';

  @override
  String get chatV2PaletteNoMatch => '没有匹配的命令';

  @override
  String get chatV2InThreadSearchHint => '在当前对话中搜索…';

  @override
  String get chatV2InThreadSearchPrev => '上一条 (Shift+Enter)';

  @override
  String get chatV2InThreadSearchNext => '下一条 (Enter)';

  @override
  String get chatV2HintIntro => '小贴士：按 ';

  @override
  String get chatV2HintBeforeCrossSearch => ' 召唤命令面板，按 ';

  @override
  String get chatV2HintAfterCrossSearch => ' 跨会话搜索；';

  @override
  String get chatV2HintAfterHelp => ' 看全部快捷键。';

  @override
  String get chatV2ChangelogSubtitle => '命令面板 · 跨会话搜索 · 草稿箱 · 收藏夹 · 模型小贴士';

  @override
  String get chatV2ChangelogBullet1 =>
      '🔍 跨会话搜索（Cmd/Ctrl+Shift+F）+ 命令面板（Cmd/Ctrl+K）';

  @override
  String get chatV2ChangelogBullet2 => '⭐ 收藏消息侧栏 + 草稿索引 + Prompt 模板';

  @override
  String get chatV2ChangelogBullet3 => '📤 一键导入导出（含 bulk 备份）';

  @override
  String get chatV2ChangelogBullet4 => '🎨 代码块行号 / 换行 / 语言切换 / 保存文件';

  @override
  String get chatV2ChangelogBullet5 => '⚡ 多模态附件 / 斜杠技能调用 / 流式 token/s';

  @override
  String get chatV2ChangelogDetails => '详情';

  @override
  String chatV2SelectionSelectedCount(Object count) {
    return '已选 $count 条';
  }

  @override
  String get chatV2SelectionSelectAll => '全选';

  @override
  String get chatV2SelectionCopy => '复制';

  @override
  String get chatV2SelectionTranslate => '翻译';

  @override
  String get chatV2SelectionExportMd => '导出 MD';

  @override
  String get chatV2SelectionDelete => '删除';

  @override
  String get chatV2SelectionCancel => '取消';

  @override
  String chatV2SelectionCopiedCount(Object count) {
    return '已复制 $count 条消息';
  }

  @override
  String get chatV2SelectionTruncated => '文本过长，已截断到 4500 字';

  @override
  String chatV2SelectionTranslateFailed(Object err) {
    return '打开翻译失败: $err';
  }

  @override
  String chatV2SelectionExportedCount(Object count) {
    return '已导出 $count 条消息';
  }

  @override
  String get chatV2SelectionDeleteTitle => '删除消息';

  @override
  String chatV2SelectionDeleteBody(Object count) {
    return '删除选中的 $count 条消息？该操作不可恢复。';
  }

  @override
  String get chatV2SelectionMdUnnamed => '(未命名)';

  @override
  String get chatV2SelectionMdModelUnset => '(未设置)';

  @override
  String get chatV2TemplatesTitle => 'System Prompt 模板';

  @override
  String get chatV2TemplatesNew => '新建';

  @override
  String get chatV2TemplatesEmpty => '还没有模板。点右上\"新建\"收藏一个常用 system prompt。';

  @override
  String get chatV2TemplatesApply => '应用';

  @override
  String get chatV2TemplatesEdit => '编辑';

  @override
  String get chatV2TemplatesDelete => '删除';

  @override
  String get chatV2TemplatesDeleteTitle => '删除模板';

  @override
  String chatV2TemplatesDeleteBody(Object name) {
    return '删除「$name」？该操作不可恢复。';
  }

  @override
  String get chatV2TemplatesEditDialogNew => '新建模板';

  @override
  String get chatV2TemplatesEditDialogEdit => '编辑模板';

  @override
  String get chatV2TemplatesNameLabel => '名称';

  @override
  String get chatV2TemplatesNameHint => '例如：Flutter 架构师';

  @override
  String get chatV2TemplatesContentLabel => 'System Prompt';

  @override
  String get chatV2TemplatesContentHint => '输入 system prompt 全文…';

  @override
  String get chatV2SettingsSheetTitle => '对话设置';

  @override
  String chatV2SettingsSheetSaveFailed(Object err) {
    return '保存失败: $err';
  }

  @override
  String get chatV2SettingsSheetNotFound => '未找到对话信息';

  @override
  String get chatV2SettingsSheetMode => '模式';

  @override
  String get chatV2SettingsSheetModel => '模型';

  @override
  String get chatV2SettingsSheetModelDefault => '默认';

  @override
  String get chatV2SettingsSheetCreated => '创建';

  @override
  String get chatV2SettingsSheetUpdated => '更新';

  @override
  String get chatV2SettingsSheetFromTemplate => '从模板选';

  @override
  String get chatV2SettingsSheetClear => '清空';

  @override
  String get chatV2SettingsSheetHint => '会作为系统消息附加到每次请求；改完点右上\"保存\"。';

  @override
  String get chatV2SettingsSheetPromptHint =>
      '例如：你是一个 Flutter 资深架构师，回答务必带具体文件 + 行号。';

  @override
  String get chatV2AttachRemove => '移除';

  @override
  String get chatV2OverflowMore => '更多';

  @override
  String get chatV2OverflowPin => '置顶';

  @override
  String get chatV2OverflowUnpin => '取消置顶';

  @override
  String get chatV2OverflowRename => '重命名';

  @override
  String get chatV2OverflowArchive => '归档';

  @override
  String get chatV2OverflowExportJson => '导出 JSON';

  @override
  String get chatV2OverflowShareLink => '复制分享链接';

  @override
  String get chatV2OverflowCopyId => '复制 thread ID';

  @override
  String get chatV2OverflowDelete => '删除';

  @override
  String get chatV2OverflowDeleteConfirmTitle => '删除对话';

  @override
  String chatV2OverflowDeleteConfirmBody(Object title) {
    return '删除「$title」？该操作不可恢复。';
  }

  @override
  String get chatV2RenameDialogTitle => '重命名对话';

  @override
  String get chatV2RenameDialogHint => '输入新名称';

  @override
  String get chatV2DialogCancel => '取消';

  @override
  String get chatV2DialogSave => '保存';

  @override
  String get chatV2DialogDelete => '删除';

  @override
  String chatV2ApprovalTitle(Object toolName) {
    return '允许执行 $toolName？';
  }

  @override
  String get chatV2ApprovalAllow => '允许';

  @override
  String get chatV2ApprovalDeny => '拒绝';

  @override
  String get chatV2ApprovalAlways => '始终允许';

  @override
  String get chatV2ApprovalShowMore => '展开完整入参 ▾';

  @override
  String get chatV2ComposerModeChat => '对话';

  @override
  String get chatV2ComposerModeAgent => '智能';

  @override
  String get chatV2ComposerModeChatHint => '纯模型对话，不调工具';

  @override
  String get chatV2ComposerModeAgentHint => '通过 daemon 调用工具';

  @override
  String get chatV2ComposerModeNoDaemon => '无 daemon 在线';

  @override
  String get chatV2ComposerWorkdirSet => '设置工作目录';

  @override
  String get chatV2ComposerWorkdirNone => '未设置工作目录';

  @override
  String get chatV2ComposerWorkdirClear => '清空工作目录';

  @override
  String get chatV2ComposerAutoApproveAuto => '自动批准';

  @override
  String get chatV2ComposerAutoApproveWhitelist => '白名单';

  @override
  String get chatV2ComposerAutoApproveManual => '手动批准';

  @override
  String get chatV2ComposerAutoApproveTooltip => '工具调用批准模式';

  @override
  String get chatV2ModelPickerSearchHint => '搜索模型…';

  @override
  String get chatV2ModelPickerSettings => '设置';

  @override
  String get chatV2ModelPickerEmpty => '无可用模型';

  @override
  String get chatV2ModelPickerEmptyAction => '前往 AI 服务商';

  @override
  String get chatV2ModelPickerNoMatch => '没有匹配的模型';

  @override
  String get chatV2ReasoningStreaming => '思考中…';

  @override
  String get chatV2ReasoningClosed => '推理过程';

  @override
  String get chatV2ReasoningExpand => '展开';

  @override
  String get chatV2ReasoningCollapse => '收起';

  @override
  String get chatV2ComposerAttachNoVision => '当前模型不支持图片输入,请切换到支持视觉的模型';

  @override
  String chatV2CtxBarTooltip(Object pct, Object total, Object used) {
    return '上下文：$used / $total tokens · $pct%';
  }

  @override
  String get wikiTitle => '知识库';

  @override
  String get wikiNoCreds => '尚未配置 model-relay 凭证。';

  @override
  String get wikiOpenSettings => '打开设置';

  @override
  String get wikiNoProjects => '还没有项目 — 先创建一个。';

  @override
  String get wikiCreateProject => '创建项目';

  @override
  String get wikiNewPageButton => '新建页面';

  @override
  String get wikiNewPageDialogTitle => '新建页面';

  @override
  String get wikiSelectPageHint => '选择或新建一个页面开始。';

  @override
  String get graphTitle => '图谱';

  @override
  String graphErrorPrefix(Object message) {
    return '图谱错误：$message';
  }

  @override
  String get graphAliasesLabel => '别名';

  @override
  String get graphSummaryLabel => '摘要';

  @override
  String get graphPathLabel => '路径';

  @override
  String commonError(Object message) {
    return '错误：$message';
  }

  @override
  String get commonNotFound => '未找到。';

  @override
  String get commonOk => '确定';

  @override
  String get commonCancel => '取消';

  @override
  String get commonDelete => '删除';

  @override
  String get commonCreate => '创建';

  @override
  String get commonRetry => '重试';

  @override
  String get navAdmin => '管理';

  @override
  String get adminTitle => '管理后台';

  @override
  String get adminTabUsers => '用户';

  @override
  String get adminTabAudit => '审计日志';

  @override
  String get adminSearchHint => '按邮箱或 ID 搜索…';

  @override
  String get adminEmptyUsers => '没有匹配的用户。';

  @override
  String get adminEmptyAudit => '暂无审计事件。';

  @override
  String get adminColEmail => '邮箱';

  @override
  String get adminColPlan => '套餐';

  @override
  String get adminColCreated => '加入时间';

  @override
  String get adminUserDetails => '用户详情';

  @override
  String get adminLimitsTitle => '套餐配额';

  @override
  String get adminFieldRPM => 'model-relay 每分钟请求数';

  @override
  String get adminFieldTPM => 'model-relay 每分钟 Token 数';

  @override
  String get adminFieldSandboxDaily => '沙箱日上限';

  @override
  String get adminFieldSandboxConcurrent => '沙箱并发数';

  @override
  String get adminFieldMemoryQuota => '每项目记忆数';

  @override
  String get adminFieldBrainProjects => '每用户项目数';

  @override
  String get adminChangePlan => '更改套餐';

  @override
  String get adminPlanReason => '原因（写入审计）';

  @override
  String get adminPlanApply => '应用';

  @override
  String get adminPlanApplied => '套餐已更新';

  @override
  String get adminAuditAt => '时间';

  @override
  String get adminAuditActor => '操作者';

  @override
  String get adminAuditAction => '动作';

  @override
  String get adminAuditTarget => '目标';

  @override
  String get adminAuditDetail => '详情';

  @override
  String get appsTitle => '应用中心';

  @override
  String get appsManage => '管理';

  @override
  String get appsManageTitle => '应用管理';

  @override
  String get appsRefresh => '刷新';

  @override
  String get appsSearchHint => '搜索应用';

  @override
  String get appsEmpty => '没有匹配的应用。';

  @override
  String get appsNoInstalls => '尚未安装任何应用。从应用中心选一个吧。';

  @override
  String get appsInstall => '安装';

  @override
  String get appsUninstall => '卸载';

  @override
  String get appsOpen => '打开';

  @override
  String get appsCancel => '取消';

  @override
  String get appsInstalled => '已安装';

  @override
  String get appsCategoryAll => '全部';

  @override
  String appsCategoryInstalled(Object count) {
    return '已安装 ($count)';
  }

  @override
  String get appsCategoryProductivity => '生产力';

  @override
  String get appsCategoryContent => '内容';

  @override
  String get appsCategoryData => '数据';

  @override
  String get appsCategoryComm => '沟通';

  @override
  String get appsCategoryDev => '开发者';

  @override
  String get appsCategoryUtility => '工具';

  @override
  String get appsConfigureFirst =>
      '请先在「设置」中配置 BiuMind model-relay 凭据，应用中心才会上线。';

  @override
  String appsInstallTitle(Object name, Object version) {
    return '安装 $name v$version';
  }

  @override
  String get appsNoPermissionRequested => '此应用未请求任何权限。';

  @override
  String appsInstalledToast(Object name) {
    return '$name 已安装。';
  }

  @override
  String appsUninstalledToast(Object name) {
    return '$name 已卸载。';
  }

  @override
  String get appsUninstallTitle => '卸载应用？';

  @override
  String appsUninstallConfirm(Object identifier) {
    return '确认卸载 $identifier？应用产生的数据会保留，可在管理页单独清理。';
  }

  @override
  String get appsSectionPermissions => '权限';

  @override
  String get appsSectionViews => '视图';

  @override
  String get appsSectionTriggers => '触发器';

  @override
  String get appsSectionSkills => '附带技能';

  @override
  String get appsErrNetwork => '网络异常，请检查连接后重试。';

  @override
  String get appsErrUnauthorized => '登录已过期，请重新登录。';

  @override
  String get appsErrNotInstalled => '请先安装该应用再调用其功能。';

  @override
  String get appsErrInstallDisabled => '该应用已停用 — 请在应用管理中重新启用。';

  @override
  String get appsErrNotFound => '对象不存在或已被删除。';

  @override
  String get appsErrConflict => '操作与最新状态冲突，请刷新后重试。';

  @override
  String appsErrValidation(Object detail) {
    return '请求参数有误：$detail';
  }

  @override
  String get appsErrRateLimit => '请求过于频繁，请稍后再试。';

  @override
  String appsErrServer(Object status) {
    return '服务暂时不可用（$status），请稍后重试。';
  }

  @override
  String appsErrUnknown(Object detail) {
    return '操作失败：$detail';
  }

  @override
  String get permNetOutbound => '访问指定外部网络。仅限 manifest 列出的域名。';

  @override
  String get permHubInvoke => '调用大语言模型（计入你的 model-relay 配额）。';

  @override
  String get permGraphRead => '读取知识图谱节点 / 边。';

  @override
  String get permGraphWrite => '写入知识图谱节点 / 边。';

  @override
  String get permMemoryRead => '读取你的多层记忆。';

  @override
  String get permMemoryWrite => '写入你的多层记忆。';

  @override
  String get permFilesRead => '读取 Files 命名空间下属于本应用的内容。';

  @override
  String get permFilesWrite => '写入 Files 命名空间。';

  @override
  String get permCronRegister => '注册定时任务（cron）。';

  @override
  String get permWebhookRegister => '注册 webhook 接收点。';

  @override
  String get permNotifySend => '向你发送通知。';

  @override
  String get permSandboxExec => '在隔离沙箱中执行命令（高风险）。';

  @override
  String get permOauth => '通过 OAuth 接入第三方账号。';

  @override
  String get permSecretsRead => '读取 vault 凭据（高风险，仅企业版可用）。';

  @override
  String get sidebarCustomizeTitle => '侧边栏定制';

  @override
  String get sidebarCollapse => '收起侧边栏';

  @override
  String get sidebarExpand => '展开侧边栏';

  @override
  String get sidebarModeHidden => '收起侧边栏';

  @override
  String get sidebarModeIconsOnly => '只显示图标';

  @override
  String get sidebarModeExpanded => '显示图标和文字';

  @override
  String get sidebarRestoreDefaults => '恢复默认';

  @override
  String get sidebarSave => '保存';

  @override
  String get sidebarSaving => '保存中…';

  @override
  String get sidebarSaved => '侧边栏已保存。';

  @override
  String get sidebarConflict => '另一设备已改动侧边栏，已重新载入最新版本。';

  @override
  String get sidebarSectionSystem => '系统（控制是否显示）';

  @override
  String get sidebarSectionPinned => '已固定的应用';

  @override
  String get sidebarSectionAvailable => '可固定的应用';

  @override
  String get sidebarHidden => '已从侧边栏隐藏';

  @override
  String get sidebarPin => '固定';

  @override
  String get sidebarPinnedEmpty => '尚未固定任何应用，从下方列表选一个。';

  @override
  String get sidebarPinnedOrphan => '该应用已卸载，请移除此项。';

  @override
  String get sidebarMoveUp => '上移';

  @override
  String get sidebarMoveDown => '下移';

  @override
  String get sidebarRemove => '移除';

  @override
  String get sidebarPinAction => '固定到侧边栏';

  @override
  String get sidebarUnpinAction => '取消固定';

  @override
  String get sidebarCustomizeAction => '自定义侧边栏…';

  @override
  String get sidebarPinnedToast => '已添加到侧边栏。';

  @override
  String get sidebarUnpinnedToast => '已从侧边栏移除。';

  @override
  String get sidebarPinNeedsInstall => '请先安装应用再固定到侧边栏。';

  @override
  String get sidebarPinSuggestionAction => '添加到侧边栏';

  @override
  String get sidebarPinSuggestionDismiss => '暂不';

  @override
  String get sidebarQueuedOffline => '网络异常 — 编辑已暂存，重连后自动同步。';

  @override
  String get sidebarOutboxBanner => '侧边栏编辑等待同步（离线中）。';

  @override
  String upgradeTitle(Object from, Object name, Object to) {
    return '升级 $name：v$from → v$to';
  }

  @override
  String get upgradeNoNewPerms => '本次升级不要求新权限，可直接升级。';

  @override
  String get upgradeNeedsApproval => '本次升级请求新权限，请逐项检查后再应用。';

  @override
  String get upgradeSectionAdded => '新增权限';

  @override
  String get upgradeSectionRemoved => '不再请求';

  @override
  String get upgradeSectionUnchanged => '已授予';

  @override
  String get upgradeCancel => '暂不升级';

  @override
  String get upgradeApply => '升级';

  @override
  String get upgradeBannerTitle => '可用升级';

  @override
  String upgradeBannerSubtitle(Object count) {
    return '$count 个应用有新版本';
  }

  @override
  String upgradeRowVersion(Object from, Object to) {
    return 'v$from → v$to';
  }

  @override
  String get upgradeAvailable => '有更新';

  @override
  String get upgradeAppliedToast => '已升级。';

  @override
  String get heroGreetingMorning => '早上好';

  @override
  String get heroGreetingNoon => '中午好';

  @override
  String get heroGreetingAfternoon => '下午好';

  @override
  String get heroGreetingEvening => '晚上好';

  @override
  String get heroGreetingNight => '还在工作?';

  @override
  String get heroSubtitleNoThread => '今天想聊点什么?';

  @override
  String get heroSubtitleEmptyThread => '开始你的对话';

  @override
  String get heroRecentSection => '最近会话';

  @override
  String get heroRecentEmpty => '还没有会话';

  @override
  String heroRelativeMinutes(Object n) {
    return '$n 分钟前';
  }

  @override
  String heroRelativeHours(Object n) {
    return '$n 小时前';
  }

  @override
  String heroRelativeDays(Object n) {
    return '$n 天前';
  }

  @override
  String get heroRelativeJustNow => '刚刚';

  @override
  String heroCurrentModel(Object model) {
    return '当前模型: $model';
  }

  @override
  String get heroSignInBanner => '未登录 — 点这里前往登录';

  @override
  String get starterPromptWritingTitle => '写作助手';

  @override
  String get starterPromptWritingHint => '帮我润色一段文字';

  @override
  String get starterPromptWritingPrompt => '请帮我润色以下文字, 让它更专业:\n\n';

  @override
  String get starterPromptCodeTitle => '代码 Review';

  @override
  String get starterPromptCodeHint => '审查我贴的代码';

  @override
  String get starterPromptCodePrompt =>
      '请帮我 review 以下代码, 找出可改进的地方:\n\n```\n\n```';

  @override
  String get starterPromptResearchTitle => '深度研究';

  @override
  String get starterPromptResearchHint => '展开一个主题';

  @override
  String get starterPromptResearchPrompt => '请深入分析以下主题, 给出多角度观点:\n\n';

  @override
  String get starterPromptTranslateTitle => '翻译';

  @override
  String get starterPromptTranslateHint => '中英互译';

  @override
  String get starterPromptTranslatePrompt => '请翻译以下内容, 保持原意和语气:\n\n';

  @override
  String get starterPromptDataTitle => '数据分析';

  @override
  String get starterPromptDataHint => '分析数据';

  @override
  String get starterPromptDataPrompt => '请帮我分析以下数据, 给出关键洞察:\n\n';

  @override
  String get starterPromptIdeasTitle => '头脑风暴';

  @override
  String get starterPromptIdeasHint => '生成想法';

  @override
  String get starterPromptIdeasPrompt => '请就以下话题给我 10 个创意点子:\n\n';

  @override
  String get navCreation => '创作';

  @override
  String get navProfile => '我的';

  @override
  String get creationInspiration => '创作灵感';

  @override
  String get creationStudio => '创作中心';

  @override
  String get creationWorks => '我的作品';

  @override
  String get creationGallery => '创意广场';

  @override
  String get creationRecharge => '积分充值';

  @override
  String get creationHeroTitle => '创作';

  @override
  String get creationHeroSubtitle => '多模态 AIGC 引擎 · 让创意触手可及';

  @override
  String get creationTabImage => '图片';

  @override
  String get creationTabVideo => '视频';

  @override
  String get creationTabDigitalHuman => '数字人';

  @override
  String get creationTabHotparse => '爆款解析';

  @override
  String get creationPromptHint => '描述你想生成的画面...';

  @override
  String get creationFirstFrame => '首帧';

  @override
  String get creationLastFrame => '尾帧';

  @override
  String get creationReferenceImage => '参考图';

  @override
  String get creationAiOptimize => 'AI 优化';

  @override
  String get creationSharePublic => '共享作品';

  @override
  String get creationSubmit => '生成';

  @override
  String get creationCardPending => '待处理';

  @override
  String get creationCardQueued => '排队中';

  @override
  String get creationCardRunning => '生成中';

  @override
  String get creationCardCompleted => '已完成';

  @override
  String get creationCardFailed => '生成失败';

  @override
  String get creationCardBlocked => '内容审核未通过';

  @override
  String get creationCardCancelled => '已取消';

  @override
  String get creationActionRetry => '重新生成';

  @override
  String get creationActionRedo => '再次生成';

  @override
  String get creationActionEdit => '重新编辑';

  @override
  String get creationActionDelete => '删除';

  @override
  String get creationActionDownload => '下载';

  @override
  String get creationActionMakeSimilar => '做同款';

  @override
  String get creationActionShare => '分享';

  @override
  String get creationActionPublic => '公开';

  @override
  String get creationActionPrivate => '私有';

  @override
  String get creationActionCancel => '取消任务';

  @override
  String creationCreditCost(Object n) {
    return '$n 积分/条';
  }

  @override
  String creationCreditRefunded(Object n) {
    return '已退还 $n 积分';
  }

  @override
  String get creationCreditInsufficient => '积分不足 — 请先充值';

  @override
  String get creationErrorEmptyPrompt => '请描述你想生成的画面';

  @override
  String get creationErrorModelNotFound => '模型不可用';

  @override
  String get creationOfflineBanner => '网络异常 — 已暂停同步';

  @override
  String get membershipCenterTitle => '会员中心';

  @override
  String get membershipCurrentPlan => '当前方案';

  @override
  String get membershipChoosePlan => '选择套餐';

  @override
  String get membershipPlanCompareTitle => '套餐对比';

  @override
  String get membershipOrdersTitle => '订单历史';

  @override
  String get membershipOrdersEmpty => '暂无订单';

  @override
  String get membershipCheckoutTitle => '支付';

  @override
  String get membershipNotLoggedIn => '请先登录后查看会员状态';

  @override
  String get membershipBadgeCurrent => '当前';

  @override
  String get membershipCtaSelect => '选择';

  @override
  String get membershipCtaCurrent => '当前方案';

  @override
  String get membershipCtaUpgrade => '升级';

  @override
  String get membershipCtaDowngrade => '降级';

  @override
  String get membershipPriceFree => '免费';

  @override
  String get membershipPricePerMonth => '/ 月';

  @override
  String get membershipPricePerYear => '/ 年';

  @override
  String get membershipQuotaChat => 'Chat 月度配额';

  @override
  String get membershipQuotaAIGC => 'AIGC 月度配额';

  @override
  String get membershipActionCancel => '取消订阅';

  @override
  String get membershipActionResume => '恢复订阅';

  @override
  String get membershipResumed => '已恢复订阅';

  @override
  String get membershipCancelTitle => '取消订阅';

  @override
  String get membershipCancelOptionPeriodEnd => '周期结束时停止';

  @override
  String get membershipCancelOptionImmediate => '立即停止 + 按比例退款';

  @override
  String get membershipCancelHint => '取消后随时可在 period_end 前点 \"恢复\" 撤销操作';

  @override
  String get membershipCancelDeny => '再想想';

  @override
  String get membershipCancelConfirm => '确认取消';

  @override
  String get membershipCanceledImmediate => '订阅已立即取消';

  @override
  String get membershipCanceledPeriodEnd => '订阅将于周期末取消';

  @override
  String get membershipUpgradeImmediate => '立即生效, 按比例补差';

  @override
  String get membershipUpgradeRefund => '旧方案剩余抵扣';

  @override
  String get membershipUpgradeNewCharge => '新方案补差';

  @override
  String get membershipUpgradeNetCharge => '本次需支付';

  @override
  String get membershipDowngradeAt => '降级生效时间: 当前周期末';

  @override
  String get membershipUpgradeContinue => '继续支付';

  @override
  String get membershipDowngradeConfirm => '确认降级';

  @override
  String get membershipPaymentMethodTitle => '选择支付方式';

  @override
  String get membershipPaymentWechatNative => '微信支付 (扫码)';

  @override
  String get membershipPaymentWechatH5 => '微信支付 (H5)';

  @override
  String get membershipPaymentAlipayPC => '支付宝 (网页)';

  @override
  String get membershipPaymentAlipayWap => '支付宝 (手机)';

  @override
  String get membershipPaymentStripe => '国际信用卡';

  @override
  String get membershipCheckoutOrderTitle => '订单详情';

  @override
  String get membershipCheckoutPay => '立即支付';

  @override
  String get membershipCheckoutWechatScan => '请用微信扫一扫支付';

  @override
  String get membershipCheckoutH5Opened => '已打开微信 H5 支付';

  @override
  String get membershipCheckoutRedirected => '已跳转支付页面, 请完成支付后回到客户端';

  @override
  String get membershipOrderProviderWechat => '微信支付';

  @override
  String get membershipOrderProviderAlipay => '支付宝';

  @override
  String get membershipOrderProviderStripe => 'Stripe';

  @override
  String get membershipOrderStatusPaid => '已支付';

  @override
  String get membershipOrderStatusPending => '待支付';

  @override
  String get membershipOrderStatusRefunded => '已退款';

  @override
  String get membershipOrderStatusFailed => '失败';

  @override
  String get membershipOrderStatusCanceled => '已取消';

  @override
  String get membershipCouponTitle => '兑换码';

  @override
  String get membershipCouponHint => '输入兑换码立即领取奖励';

  @override
  String get membershipCouponSubmit => '兑换';

  @override
  String get membershipCouponSuccess => '兑换成功';

  @override
  String get membershipCouponNotFound => '兑换码无效';

  @override
  String get membershipCouponExpired => '兑换码已过期';

  @override
  String get membershipCouponInactive => '兑换码已停用';

  @override
  String get membershipCouponAlreadyUsed => '此兑换码您已使用过';

  @override
  String get membershipReferralTitle => '邀请奖励';

  @override
  String get membershipReferralYourCode => '你的邀请码';

  @override
  String get membershipReferralStats => '邀请统计';

  @override
  String get membershipReferralStatTotal => '总邀请';

  @override
  String get membershipReferralStatRewarded => '已奖励';

  @override
  String get membershipReferralStatPending => '待生效';

  @override
  String get membershipReferralStatReverted => '已撤销';

  @override
  String get membershipReferralShare => '分享';

  @override
  String get membershipReferralCopyCode => '邀请码已复制';

  @override
  String get membershipReferralCopyLink => '邀请链接已复制';

  @override
  String get membershipReferralRulesTitle => '奖励规则';

  @override
  String get chatV2HintDismiss => '不再提示';

  @override
  String get chatV2ChangelogHeadline => 'BiuMind Chat 又添 5 件趁手装备';

  @override
  String get commonForbidden => '无权限 — 此资源不属于你。';

  @override
  String get appsPermissionRequestIntro => '该应用请求以下权限。可逐项取消同意；服务端会用授予子集判断每个调用。';

  @override
  String get appsErrForbidden => '没有权限执行该操作。';

  @override
  String get permWikiRead => '读取你 Wiki 中的内容（仅限本应用 namespace）。';

  @override
  String get permWikiWrite => '写入你的 Wiki（仅限本应用 namespace）。';
}
