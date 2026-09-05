// ignore: unused_import
import 'package:intl/intl.dart' as intl;
import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for English (`en`).
class AppLocalizationsEn extends AppLocalizations {
  AppLocalizationsEn([String locale = 'en']) : super(locale);

  @override
  String get appTitle => 'BiuMind';

  @override
  String get navChat => 'Chat';

  @override
  String get navWiki => 'Wiki';

  @override
  String get navGraph => 'Graph';

  @override
  String get navMemory => 'Memory';

  @override
  String get navCode => 'Code';

  @override
  String get navSkills => 'Skills';

  @override
  String get navSettings => 'Settings';

  @override
  String get skillsTitle => 'Skills';

  @override
  String get skillsRefresh => 'Refresh';

  @override
  String get skillsAdd => 'Add';

  @override
  String get skillsFilterAll => 'All';

  @override
  String get skillsFilterBundled => 'Bundled';

  @override
  String get skillsFilterOrg => 'Org';

  @override
  String get skillsFilterMy => 'My';

  @override
  String get skillsFilterMarketplace => 'Marketplace';

  @override
  String get skillsSelectHint => 'Pick a skill to view details';

  @override
  String get skillsEmpty => 'No skills yet — click + Add to install one';

  @override
  String get skillsConfigureHint => 'Sign in to manage cloud skills';

  @override
  String get skillsPermissions => 'Permissions';

  @override
  String get skillsAutoAttachPaths => 'Auto-attach paths';

  @override
  String get skillsBody => 'SKILL.md body';

  @override
  String get skillsDelete => 'Delete';

  @override
  String get skillsCancel => 'Cancel';

  @override
  String get skillsInstall => 'Install';

  @override
  String get skillsConfirmDeleteTitle => 'Delete skill?';

  @override
  String skillsConfirmDeleteBody(Object name) {
    return 'Permanently delete \"$name\"? This cannot be undone.';
  }

  @override
  String get skillsInstallURL => 'URL';

  @override
  String get skillsInstallURLHint => 'Server fetches the SKILL.md over HTTPS';

  @override
  String get skillsInstallZip => 'Zip';

  @override
  String get skillsInstallZipPick => 'Pick .biuskill or .zip…';

  @override
  String get skillsInstallZipHint => 'Bundle is uploaded as base64 (≤ 8 MB)';

  @override
  String get skillsInstallInline => 'Inline';

  @override
  String get skillsInlineIdentifier => 'Identifier';

  @override
  String get skillsInlineName => 'Name';

  @override
  String get skillsInlineDescription => 'Description';

  @override
  String get skillsInlineBody => 'SKILL.md body';

  @override
  String get skillsErrURLRequired => 'URL required';

  @override
  String get skillsErrZipRequired => 'Pick a .biuskill / .zip file';

  @override
  String get skillsErrInlineRequired =>
      'identifier, name, description, body all required';

  @override
  String get skillsErrTooLarge => 'Upload too large — server max is 8 MB';

  @override
  String get chatPickSkills => 'Mention skills';

  @override
  String get chatPickSkillsFilter => 'Filter…';

  @override
  String get chatPickSkillsEmpty => 'No active skills — install some in Skills';

  @override
  String get chatPickSkillsClear => 'Clear all';

  @override
  String chatPickSkillsDone(Object count) {
    return 'Done ($count)';
  }

  @override
  String get chatComposerSkillsTooltip => 'Mention skills (@)';

  @override
  String get skillsFilterPending => 'Pending';

  @override
  String get skillsApprove => 'Approve';

  @override
  String get skillsReject => 'Reject';

  @override
  String get skillsRejectReason => 'Reason (optional)';

  @override
  String get codeWorkbenchTitle => 'Code Workbench';

  @override
  String get codeNoTaskHint => 'Pick a task on the left or start a new one.';

  @override
  String get codeNewTask => 'New task';

  @override
  String get codeTabAgent => 'Agent';

  @override
  String get codeTabTerminal => 'Terminal';

  @override
  String get codeTabPr => 'PR Preview';

  @override
  String get codeTabCompare => 'Compare';

  @override
  String get codeRightFiles => 'Files';

  @override
  String get codeRightDiff => 'Diff';

  @override
  String get codeRightGit => 'Git';

  @override
  String get codeRightHooks => 'Hooks';

  @override
  String get codeStatusBarHint => 'Coding workbench';

  @override
  String get codeUsageTooltip => 'Usage';

  @override
  String get codeUsageLoading => 'Loading usage…';

  @override
  String get codeUsageFailed => 'Failed to load usage';

  @override
  String get codeUsageNoWindows => 'No usage data';

  @override
  String get codeUsageFiveHour => '5-hour';

  @override
  String get codeUsageSevenDay => '7-day';

  @override
  String get codeUsagePrimary => 'Primary';

  @override
  String get codeUsageSecondary => 'Secondary';

  @override
  String get codeUsageLeft => 'left';

  @override
  String get codeUsageRefresh => 'Refresh';

  @override
  String get memoryTitle => 'Memory';

  @override
  String get memoryProjectMissing => '(no project)';

  @override
  String get memoryHintNoCreds =>
      'Configure model-relay URL + token in Settings.';

  @override
  String get memoryHintNoProject =>
      'Open or create a project in the Wiki tab first.';

  @override
  String get memoryHintEmptyList => 'No memories yet — add one below.';

  @override
  String get memoryHintEmptyRecall => 'No matches. Try another query.';

  @override
  String get memoryListTab => 'List';

  @override
  String get memoryRecallTab => 'Recall';

  @override
  String get memoryFilterAll => 'All';

  @override
  String get memoryKindRecall => 'recall';

  @override
  String get memoryKindPreference => 'preference';

  @override
  String get memoryKindHabit => 'habit';

  @override
  String get memoryRecallQueryHint => 'recall query…';

  @override
  String get memoryAddHint => 'remember this…';

  @override
  String get memoryAddButton => 'Add';

  @override
  String get memoryRefresh => 'Refresh';

  @override
  String get memoryModeHybrid => 'hybrid (semantic + lexical)';

  @override
  String get memoryModeLexical => 'lexical-only';

  @override
  String memorySubtitle(
    Object kind,
    Object salience,
    Object scoreSuffix,
    Object when,
  ) {
    return 'kind=$kind · salience=$salience$scoreSuffix · $when';
  }

  @override
  String relTimeSeconds(Object seconds) {
    return '${seconds}s ago';
  }

  @override
  String relTimeMinutes(Object minutes) {
    return '${minutes}m ago';
  }

  @override
  String relTimeHours(Object hours) {
    return '${hours}h ago';
  }

  @override
  String relTimeDays(Object days) {
    return '${days}d ago';
  }

  @override
  String relTimeMonths(Object months) {
    return '${months}mo ago';
  }

  @override
  String get settingsTitle => 'Settings';

  @override
  String get settingsModeHub => 'model-relay';

  @override
  String get settingsModeBYOK => 'BYOK (bring your own key)';

  @override
  String get settingsModeOffline => 'Offline';

  @override
  String get settingsHubUrl => 'model-relay URL';

  @override
  String get settingsRelayToken => 'model-relay token';

  @override
  String get settingsTestConnection => 'Test connection';

  @override
  String get settingsSave => 'Save';

  @override
  String get settingsSavedSnack => 'Settings saved';

  @override
  String get signInTitle => 'Sign in';

  @override
  String get signInSubtitle => 'Sign in to your BiuMind account.';

  @override
  String get signInIdentityUrl => 'Server URL';

  @override
  String get signInAdvanced => 'Advanced';

  @override
  String get signInVerifyCode => '6-digit code';

  @override
  String get signInEmail => 'Email';

  @override
  String get signInPassword => 'Password';

  @override
  String get signInSubmit => 'Sign in';

  @override
  String get signInRegister => 'Register';

  @override
  String signInSignOut(Object email) {
    return 'Sign out ($email)';
  }

  @override
  String signInOk(Object email) {
    return '✓ Signed in as $email';
  }

  @override
  String registerOk(Object email) {
    return '✓ Registered as $email';
  }

  @override
  String signInTokenExpires(Object when) {
    return 'Token expires: $when';
  }

  @override
  String get signInErrInvalidCredentials => 'Email or password is incorrect.';

  @override
  String get signInErrEmailTaken => 'This email is already registered.';

  @override
  String get signInErrInvalidEmail => 'Email format is invalid.';

  @override
  String get signInErrPasswordTooShort =>
      'Password is too short — use at least 8 characters.';

  @override
  String get signInErrNetwork => 'Cannot reach the server. Check your network.';

  @override
  String get signInErrUnknown => 'Sign-in failed. Try again.';

  @override
  String signInCodeSent(String email) {
    return 'Code sent to $email — check your inbox (valid for 10 minutes)';
  }

  @override
  String get signInCodeDevMode =>
      'Code generated. Dev mode: SMTP not configured — ask an admin to read the code from server logs';

  @override
  String signInEnterCodeSentTo(String email) {
    return 'Enter the 6-digit code sent to $email';
  }

  @override
  String get signInEnterCode => 'Enter the 6-digit code';

  @override
  String signInCodeResent(String email) {
    return 'Code re-sent to $email';
  }

  @override
  String get signInCodeRegenerated =>
      'New code generated. Dev mode — read it from server logs';

  @override
  String get signInForgotHint =>
      'Enter your account email and we\'ll send a 6-digit reset code';

  @override
  String get signInErrEmailRequired => 'Enter your account email';

  @override
  String signInResetCodeSent(String email) {
    return 'If this email is registered, a code was sent to $email (valid for 10 minutes)';
  }

  @override
  String get signInResetCodeDevMode =>
      'Code generated. Dev mode — ask an admin to read it from server logs';

  @override
  String get signInErrNewPasswordShort =>
      'New password must be at least 8 characters';

  @override
  String get signInPasswordReset =>
      'Password reset — sign in with your new password';

  @override
  String get signInNewPasswordLabel => 'New password (≥ 8 chars)';

  @override
  String get signInForgotPassword => 'Forgot password?';

  @override
  String get signInNoAccount => 'No account?';

  @override
  String get signInSendResetCode => 'Send reset code';

  @override
  String get signInBackToSignIn => 'Back to sign in';

  @override
  String get signInResetPassword => 'Reset password';

  @override
  String signInResendCooldown(int seconds) {
    return 'Resend (${seconds}s)';
  }

  @override
  String get signInResendCode => 'Resend code';

  @override
  String get signInSubtitleVerify =>
      'Verify your email to activate your account';

  @override
  String get signInSubtitleForgot =>
      'Forgot password — we\'ll email you a reset code';

  @override
  String get signInSubtitleReset =>
      'Enter the code and a new password to finish the reset';

  @override
  String get signInVerifySubmit => 'Verify & sign in';

  @override
  String get signInErrInvalidCode => 'Incorrect code — try again';

  @override
  String get signInErrCodeExpired => 'Code expired — tap resend';

  @override
  String get signInErrCodeLocked => 'Too many attempts — request a new code';

  @override
  String get signInErrCodeUsed => 'Code already used — request a new one';

  @override
  String get signInErrNoPendingCode => 'No code sent yet — tap resend first';

  @override
  String get signInErrRateLimited => 'Too many requests — try again later';

  @override
  String get settingsModeSection => 'Mode';

  @override
  String get settingsModeSectionSubtitle => 'How AI calls are routed.';

  @override
  String get settingsHubSection => 'model-relay connection';

  @override
  String get settingsHubSectionSubtitle =>
      'Used in cloud / byo_endpoint modes. Bearer token is auto-filled ';

  @override
  String get settingsBYOKSection => 'BYOK provider keys';

  @override
  String get settingsBYOKSectionSubtitle =>
      'Stored encrypted in OS keychain. Used in direct mode and as ';

  @override
  String get settingsAccountSection => 'Account';

  @override
  String get settingsAccountSectionSubtitle =>
      'Sign in to sync your chats across devices.';

  @override
  String get settingsServiceUrl => 'Service URL';

  @override
  String get settingsProvidersSection => 'Model providers';

  @override
  String get settingsProvidersSectionSubtitle =>
      'Configure which model providers you can chat with. Server stores keys encrypted.';

  @override
  String get settingsChatDefaultsSection => 'Chat defaults';

  @override
  String get settingsChatDefaultsSectionSubtitle =>
      'Default model for new conversations.';

  @override
  String get settingsDefaultModel => 'Default model';

  @override
  String get settingsAddProvider => 'Add provider';

  @override
  String get settingsAddCustomProvider => 'Add custom provider';

  @override
  String get settingsCustomProviderId => 'Provider ID (slug)';

  @override
  String get settingsCustomProviderName => 'Display name';

  @override
  String get settingsConfigure => 'Configure';

  @override
  String get settingsRemove => 'Remove';

  @override
  String get settingsApiKey => 'API key';

  @override
  String get settingsBaseUrlOptional => 'Base URL (optional)';

  @override
  String get settingsFetchModeLabel => 'Call routing';

  @override
  String get settingsFetchModeServer => 'Server-side relay';

  @override
  String get settingsFetchModeServerDesc =>
      'Brain proxies the call. Lowest client work, easier debugging.';

  @override
  String get settingsFetchModeClient => 'Client-side direct';

  @override
  String get settingsFetchModeClientDesc =>
      'Your device opens the LLM stream itself. Key only leaves the server when this device asks.';

  @override
  String get settingsProviderConfigured => 'Configured';

  @override
  String get settingsProviderNotConfigured => 'Not configured';

  @override
  String get settingsGroupGeneral => 'GENERAL';

  @override
  String get settingsGroupAgent => 'AGENT';

  @override
  String get settingsGroupSystem => 'SYSTEM';

  @override
  String get settingsNavStatistics => 'Statistics';

  @override
  String get settingsNavAppearance => 'Appearance';

  @override
  String get settingsNavShortcuts => 'Shortcuts';

  @override
  String get settingsNavProviders => 'AI Providers';

  @override
  String get settingsNavDefaultModel => 'Default Model';

  @override
  String get settingsNavSkills => 'Skills';

  @override
  String get settingsNavMemory => 'Memory';

  @override
  String get settingsNavCredentials => 'Credentials';

  @override
  String get settingsNavCodingWorkbench => 'Coding Workbench';

  @override
  String get settingsNavChat => 'Chat';

  @override
  String get settingsNavDocproc => 'Document Processing';

  @override
  String get settingsDocprocSubtitle =>
      'Where imported knowledge-base documents (PDF/DOCX/XLSX/PPTX/EPUB/HTML/MD/TXT) are parsed.';

  @override
  String get settingsDocprocAuto => 'Auto';

  @override
  String get settingsDocprocAutoDesc =>
      'Small files are parsed on-device (free; ≤50MB desktop / ≤10MB mobile); larger files go to the cloud.';

  @override
  String get settingsDocprocPreferLocal => 'Prefer on-device';

  @override
  String get settingsDocprocPreferLocalDesc =>
      'Parse on-device whenever possible (free); only very large files (>200MB / >80MB mobile) go to the cloud.';

  @override
  String get settingsDocprocPreferCloud => 'Prefer cloud';

  @override
  String get settingsDocprocPreferCloudDesc =>
      'Always upload for cloud parsing; billed per page in credits.';

  @override
  String get settingsDocprocUnsupported =>
      'On-device processing is not available on this platform; cloud parsing will always be used.';

  @override
  String get settingsDocprocNote =>
      'On-device parsing is free; cloud parsing costs credits per page. Scanned-document OCR and audio/video transcription are cloud-only.';

  @override
  String get settingsDocprocIngestModelTitle => 'Wiki generation model';

  @override
  String get settingsDocprocIngestModelDesc =>
      'The model used to generate Wiki pages from uploaded documents. Stored in your account and synced across devices. \"Follow platform default\" uses the platform-configured model; a self-selected model is billed to your usage (your own API key is preferred when configured).';

  @override
  String get settingsDocprocIngestModelDefault => 'Follow platform default';

  @override
  String get settingsDocprocIngestModelSaveFailed =>
      'Failed to save. Please try again later.';

  @override
  String get settingsNavProxy => 'Proxy';

  @override
  String get settingsNavStorage => 'Storage';

  @override
  String get settingsNavApiKey => 'API Key';

  @override
  String get settingsNavAdvanced => 'Advanced';

  @override
  String get settingsNavAbout => 'About';

  @override
  String get settingsProviderSearchHint => 'Search providers…';

  @override
  String get settingsProvidersAll => 'All';

  @override
  String get settingsProvidersEnabled => 'Enabled';

  @override
  String get settingsProvidersDisabled => 'Disabled';

  @override
  String get settingsConnectivityCheck => 'Connectivity check';

  @override
  String get settingsCheckButton => 'Check';

  @override
  String get settingsCheckOk => 'Connection OK';

  @override
  String get settingsCheckSaveFirst => 'Save the API key first, then check.';

  @override
  String get settingsConfigureFirstHint =>
      'This provider isn\'t configured yet. Add an API key below and click Save.';

  @override
  String get settingsEncryptionNote =>
      'Your API key + base URL are encrypted at rest with AES-256-GCM.';

  @override
  String get settingsModelList => 'Models';

  @override
  String get settingsModelRefresh => 'Refresh';

  @override
  String get settingsModelEmpty =>
      'No models. Configure a key first, then refresh.';

  @override
  String get settingsModelTabAll => 'All';

  @override
  String get settingsModelTabChat => 'Chat';

  @override
  String get settingsModelTabImage => 'Image';

  @override
  String get settingsModelTabVideo => 'Video';

  @override
  String get settingsOfficialBadge => 'BiuMind Official';

  @override
  String get settingsOfficialDescription =>
      'Use BiuMind\'s curated platform models. No API key needed — sign in and chat. Calls route through our pool with usage-based or subscription billing.';

  @override
  String get settingsOfficialBilling =>
      'Billing: pay-as-you-go (per-token) or monthly subscription. Manage in account.';

  @override
  String get themeSystem => 'System';

  @override
  String get themeLight => 'Light';

  @override
  String get themeDark => 'Dark';

  @override
  String get settingsAppearanceColorTheme => 'Color theme';

  @override
  String get settingsAppearanceColorThemeRecommended => 'Recommended';

  @override
  String get settingsAppearanceFontSize => 'Font size';

  @override
  String get settingsAppearanceFontSizeSmall => 'Small';

  @override
  String get settingsAppearanceFontSizeMedium => 'Medium';

  @override
  String get settingsAppearanceFontSizeLarge => 'Large';

  @override
  String get settingsAppearanceFontSizeHint =>
      'Font size also drives spacing and list density.';

  @override
  String get settingsAppearancePreviewTitle => 'Hello, BiuMind';

  @override
  String get settingsAppearancePreviewBody =>
      'Live preview — pick the size that feels right.';

  @override
  String get settingsAppearanceMode => 'Mode';

  @override
  String get paletteNamePurpleOrange => 'Purple + Orange';

  @override
  String get paletteDescPurpleOrange =>
      'Default — wisdom purple + action orange';

  @override
  String get paletteNamePurple => 'Purple';

  @override
  String get paletteDescPurple => 'Original brand purple';

  @override
  String get paletteNamePurpleBlue => 'Purple + Blue';

  @override
  String get paletteDescPurpleBlue => 'Rational, technical';

  @override
  String get paletteNamePurplePink => 'Purple + Pink';

  @override
  String get paletteDescPurplePink => 'Warm, expressive';

  @override
  String get paletteNamePurpleEmerald => 'Purple + Emerald';

  @override
  String get paletteDescPurpleEmerald => 'Balanced, rare combo';

  @override
  String get paletteNameAurora => 'Aurora';

  @override
  String get paletteDescAurora => 'Cyan-purple-pink mesh, max memorability';

  @override
  String get paletteNameSunset => 'Sunset';

  @override
  String get paletteDescSunset => 'Orange-pink-purple, warm energy';

  @override
  String get paletteNameCyber => 'Cyber';

  @override
  String get paletteDescCyber => 'Cyan-purple-magenta, futuristic';

  @override
  String get paletteNameOcean => 'Ocean';

  @override
  String get paletteDescOcean => 'Blue-cyan, calm B2B';

  @override
  String get paletteNameEmeraldGold => 'Emerald Gold';

  @override
  String get paletteDescEmeraldGold => 'Premium, financial';

  @override
  String get paletteNameRose => 'Rose';

  @override
  String get paletteDescRose => 'Emotive, vibrant';

  @override
  String get paletteNameOnyx => 'Onyx';

  @override
  String get paletteDescOnyx => 'Minimalist, professional';

  @override
  String get paletteNameInkblueOrange => 'Inkblue + Signal Orange';

  @override
  String get paletteDescInkblueOrange => 'Vercel-style minimalism';

  @override
  String get paletteNameQuantumTitanium => 'Quantum + Titanium';

  @override
  String get paletteDescQuantumTitanium => 'Vision Pro feel, premium';

  @override
  String get paletteNameClaudeWarm => 'Claude Warm';

  @override
  String get paletteDescClaudeWarm => 'Counter-trend warmth';

  @override
  String get paletteNameGraphiteCyan => 'Graphite + Cyan';

  @override
  String get paletteDescGraphiteCyan => 'Cursor-style engineering';

  @override
  String get paletteNameIndigoSand => 'Indigo + Sand';

  @override
  String get paletteDescIndigoSand => '2026 trend, balanced';

  @override
  String get paletteNameWikiGreen => 'Wiki Emerald';

  @override
  String get paletteDescWikiGreen => 'Calm green for note-taking';

  @override
  String get aboutSubtitle => 'About BiuMind';

  @override
  String get aboutBuild => 'Build';

  @override
  String get aboutTagline =>
      'Your AI workspace — chats, knowledge, and memory in one place.';

  @override
  String get settingsCheckUpdate => 'Check for updates';

  @override
  String get settingsCheckUpdateLatest => 'Up to date';

  @override
  String get settingsCheckUpdateChecking => 'Checking…';

  @override
  String get settingsCheckUpdateAvailable => 'Update available';

  @override
  String get settingsCheckUpdateFailed => 'Check failed — try again later';

  @override
  String get settingsCheckUpdateNow => 'Recheck';

  @override
  String settingsCheckUpdateFound(String version) {
    return 'New version $version available';
  }

  @override
  String get settingsCheckUpdateDownload => 'Download';

  @override
  String get settingsFetchNightly => 'Get nightly builds';

  @override
  String get settingsFetchNightlySubtitle =>
      'Unsigned · may be unstable · back up your data first';

  @override
  String get settingsAppearanceSection => 'Appearance';

  @override
  String get settingsAppearanceSectionSubtitle =>
      'Theme is currently locked to system; theme switcher in P3.6.';

  @override
  String get settingsBearerToken => 'Bearer token';

  @override
  String get settingsBearerTokenHint => 'JWT or virtual key (bk-live-…)';

  @override
  String get settingsAnthropicKey => 'Anthropic API key';

  @override
  String get settingsOpenAIKey => 'OpenAI API key';

  @override
  String get settingsTheme => 'Theme';

  @override
  String get settingsModeCloudTitle => 'Cloud (default)';

  @override
  String get settingsModeCloudDesc =>
      'All AI calls go through your BiuMind model-relay. Platform billing + audit.';

  @override
  String get settingsModeBYOEndpointTitle => 'BYO Endpoint';

  @override
  String get settingsModeBYOEndpointDesc =>
      'Through model-relay, but model-relay forwards to your private endpoint ';

  @override
  String get settingsModeDirectTitle => 'Direct (standalone)';

  @override
  String get settingsModeDirectDesc =>
      'Bypass model-relay. AI calls go directly from your device to the LLM ';

  @override
  String get chatTitle => 'Chat';

  @override
  String get chatHint => 'Type a message…';

  @override
  String get chatSend => 'Send';

  @override
  String get chatNewThread => 'New chat';

  @override
  String get chatUntitled => '(untitled)';

  @override
  String get chatErrNotSignedIn => 'Not signed in. Open Settings to connect.';

  @override
  String get chatErrSettingsLoading =>
      'Settings still loading — try again in a moment.';

  @override
  String get chatErrDirectUnsupported => 'Direct mode is not supported yet.';

  @override
  String get chatErrDirectNoKey =>
      'Anthropic API key not configured. Set it in Settings.';

  @override
  String get chatErrNetwork => 'Network error. Check your connection.';

  @override
  String get chatErrAuth => 'Session expired. Sign in again.';

  @override
  String get chatV2SettingsTitle => 'Chat preferences';

  @override
  String get chatV2SettingsResetAll => 'Reset';

  @override
  String get chatV2SettingsResetConfirmTitle => 'Reset preferences';

  @override
  String get chatV2SettingsResetConfirmBody =>
      'Font size / default mode / default model / language will all be reset to factory defaults. This is irreversible.';

  @override
  String get chatV2SettingsResetButton => 'Reset';

  @override
  String get chatV2SettingsCancel => 'Cancel';

  @override
  String get chatV2SettingsClose => 'Close';

  @override
  String get chatV2SettingsFontScale => 'Font size';

  @override
  String get chatV2SettingsFontScaleHint =>
      'Scales text inside message bubbles';

  @override
  String get chatV2SettingsDefaultMode => 'Default conversation mode';

  @override
  String get chatV2SettingsAutoRenameTitle => 'Auto-title from first prompt';

  @override
  String get chatV2SettingsAutoRenameSubtitle =>
      'Off keeps \"New chat\" as placeholder until you rename manually';

  @override
  String get chatV2SettingsDefaultModel => 'Default model (chat mode)';

  @override
  String get chatV2SettingsDefaultModelDefault =>
      'BiuMind default (unspecified)';

  @override
  String get chatV2SettingsTtsTitle => 'Read aloud (text-to-speech)';

  @override
  String get chatV2SettingsTtsHint =>
      'Pick a cloud voice model for high-quality narration via model-relay. ';

  @override
  String get chatV2SettingsTtsModel => 'Voice model';

  @override
  String get chatV2SettingsTtsModelLocal => 'Device voice (offline, free)';

  @override
  String get chatV2SettingsTtsVoice => 'Voice ID';

  @override
  String get chatV2SettingsTtsVoiceHint =>
      'e.g. longanyang (cosyvoice system voice)';

  @override
  String get chatV2SettingsTtsNoModels =>
      'No voice models configured. Add an audio_speech model in a channel first.';

  @override
  String get chatV2SettingsLanguage => 'Language';

  @override
  String get chatV2SettingsLanguageSystem => 'System';

  @override
  String get chatV2SettingsLanguageZh => '中文';

  @override
  String get chatV2SettingsLanguageEn => 'English';

  @override
  String get chatV2AppBarPinTooltip => 'Pin';

  @override
  String get chatV2AppBarUnpinTooltip => 'Unpin';

  @override
  String get chatV2AppBarStopTooltip => 'Stop generating';

  @override
  String get chatV2AppBarStoppingTooltip => 'Stopping…';

  @override
  String get chatV2AppBarSearchTooltip => 'Search in conversation (Cmd/Ctrl+F)';

  @override
  String get chatV2AppBarMultiSelectTooltip => 'Multi-select messages';

  @override
  String get chatV2AppBarShortcutsTooltip => 'Keyboard shortcuts (?)';

  @override
  String get chatV2AppBarSettingsTooltip =>
      'Conversation settings (system prompt, etc)';

  @override
  String get chatV2AppBarMore => 'More';

  @override
  String get chatV2AppBarStreaming => 'Generating';

  @override
  String get chatV2AppBarStopping => 'Stopping…';

  @override
  String get chatV2HeroSubtitle =>
      'What do you want to do today? Pick a starter, or ';

  @override
  String get chatV2HeroNewBlank => 'new blank conversation';

  @override
  String get chatV2HeroSkillsLabel => 'My skills';

  @override
  String get chatV2HeroRecentLabel => 'Recent conversations';

  @override
  String get chatV2HeroRecentModelsLabel => 'Recent models';

  @override
  String get chatV2HeroKvLabel => 'This month';

  @override
  String get chatV2HeroKvMonthMessages => 'Conversations';

  @override
  String get chatV2HeroKvCredits => 'Credits left';

  @override
  String get chatV2HeroKvStreak => 'Streak';

  @override
  String chatV2HeroSetDefaultModel(Object model) {
    return 'Default model set: $model';
  }

  @override
  String chatV2HeroStatsThreads(Object messages, Object threads) {
    return '$messages messages · $threads conversations';
  }

  @override
  String chatV2HeroStatsThisWeek(Object active, Object messages) {
    return 'This week $messages · $active active';
  }

  @override
  String chatV2HeroStatsRecentDays(
    Object active,
    Object days,
    Object messages,
  ) {
    return 'Last ${days}d $messages · $active active';
  }

  @override
  String chatV2HeroStreakChip(Object n) {
    return '$n-day streak';
  }

  @override
  String chatV2HeroStreakTooltip(Object n) {
    return '$n consecutive days of conversations';
  }

  @override
  String chatV2HeroStatsSwitchTooltip(
    Object active,
    Object days,
    Object messages,
  ) {
    return 'Last $days days: $messages messages across $active conversations\nTap to switch 7 / 30 day view';
  }

  @override
  String get chatV2ComposerHint =>
      'What can I help with? @ to mention a skill, / for commands';

  @override
  String get chatV2ComposerDisclaimer =>
      'AI-generated content — please verify important information';

  @override
  String get chatV2ComposerHintStreaming => 'Generating…';

  @override
  String get chatV2ComposerAttachTooltip =>
      'Attach image (drag-drop or paste also works)';

  @override
  String get chatV2ComposerAttachNeedThread => 'Pick a conversation first';

  @override
  String get chatV2ComposerAttachCamera => 'Take photo';

  @override
  String get chatV2ComposerAttachGallery => 'Choose from gallery';

  @override
  String get chatV2ComposerAttachFile => 'Choose file';

  @override
  String get chatV2ComposerWebOn => 'Web search: ON (single-shot)';

  @override
  String get chatV2ComposerWebOff => 'Web search: off — click to enable';

  @override
  String get chatV2ComposerWebSnack =>
      'Web search: auto-disables after this send';

  @override
  String get chatV2ComposerSendTooltip => 'Send';

  @override
  String get chatV2ComposerCancelTooltip => 'Cancel';

  @override
  String chatV2ComposerCharTokens(Object chars, Object tokens) {
    return '$chars chars  ·  ~$tokens tokens';
  }

  @override
  String get chatV2ComposerStopping => 'Stopping…';

  @override
  String get chatV2ComposerStoppingTooltip => 'Stopping…';

  @override
  String chatV2ComposerErrAttachOnlyImage(Object name) {
    return 'Only image attachments supported, skipped $name';
  }

  @override
  String get chatV2ComposerErrAttachTooLarge => 'Image must be under 10MB';

  @override
  String chatV2ComposerErrAttach(Object err) {
    return 'Attachment error: $err';
  }

  @override
  String get chatV2ComposerSlashDialogTitle => 'Slash command';

  @override
  String get chatV2DialogOk => 'OK';

  @override
  String get chatV2ComposerModelSwitchTooltip => 'Switch model';

  @override
  String get chatV2ComposerModelDefault => 'BiuMind default';

  @override
  String get chatV2ComposerModelRefresh => 'Refresh model list';

  @override
  String get chatV2NewThreadFallback => 'New conversation';

  @override
  String get chatV2SidebarTitle => 'Conversations';

  @override
  String get chatV2SidebarFilterHint => 'Filter conversations…';

  @override
  String get chatV2SidebarPaletteTooltip => 'Command palette (Cmd/Ctrl+K)';

  @override
  String get chatV2SidebarStarredTooltip => 'Starred messages';

  @override
  String get chatV2SidebarCrossSearchTooltip => 'Search all (Cmd/Ctrl+Shift+F)';

  @override
  String get chatV2SidebarImportTooltip => 'Import JSON';

  @override
  String get chatV2SidebarNewTooltip => 'New conversation';

  @override
  String get chatV2SidebarSectionPinned => 'Pinned';

  @override
  String get chatV2SidebarSectionOthers => 'Others';

  @override
  String get chatV2SidebarEmptyNew =>
      'No conversations yet\nClick + above to start';

  @override
  String get chatV2SidebarEmptyFiltered => 'No matching conversations';

  @override
  String chatV2SidebarArchivedFooter(Object count) {
    return 'Archived $count';
  }

  @override
  String get chatV2SidebarBatchTooltip => 'Batch manage';

  @override
  String chatV2BatchSelectedCount(Object count) {
    return '$count selected';
  }

  @override
  String get chatV2BatchSelectAll => 'Select all';

  @override
  String get chatV2BatchSelectNone => 'Clear';

  @override
  String get chatV2BatchDelete => 'Delete';

  @override
  String get chatV2BatchExitTooltip => 'Exit batch manage';

  @override
  String get chatV2BatchDeleteTitle => 'Delete conversations';

  @override
  String chatV2BatchDeleteBody(Object count) {
    return 'Delete the $count selected conversations? This cannot be undone.';
  }

  @override
  String chatV2BatchDeletedCount(Object count) {
    return 'Deleted $count conversations';
  }

  @override
  String chatV2LoadError(Object err) {
    return 'Load failed: $err';
  }

  @override
  String chatV2ExportSuccess(Object name) {
    return 'Exported $name';
  }

  @override
  String chatV2ExportAllSuccess(Object name) {
    return 'Exported all conversations → $name';
  }

  @override
  String chatV2ExportFailed(Object err) {
    return 'Export failed: $err';
  }

  @override
  String chatV2ImportSuccessCount(Object count) {
    return 'Imported $count conversations';
  }

  @override
  String get chatV2ImportSuccess => 'Conversation imported';

  @override
  String chatV2ImportFailed(Object err) {
    return 'Import failed: $err';
  }

  @override
  String chatV2ApplyTemplate(Object name) {
    return 'Applied template \"$name\"';
  }

  @override
  String get chatV2PaletteGroupOps => 'Actions';

  @override
  String get chatV2PaletteGroupCurrent => 'Current conversation';

  @override
  String get chatV2PaletteGroupSwitch => 'Switch conversation';

  @override
  String get chatV2PaletteNewThread => 'New conversation';

  @override
  String get chatV2PaletteNewThreadHint => 'Open new conversation wizard';

  @override
  String get chatV2PaletteCrossSearch => 'Search all conversations';

  @override
  String get chatV2PaletteStarred => 'View starred messages';

  @override
  String get chatV2PaletteStarredHint => 'All ⭐ messages across threads';

  @override
  String get chatV2PaletteDrafts => 'View drafts';

  @override
  String get chatV2PaletteDraftsHint => 'Unsent input across all threads';

  @override
  String get chatV2PaletteArchived => 'View archived';

  @override
  String get chatV2PaletteArchivedHint => 'Unarchive / permanently delete';

  @override
  String get chatV2PaletteExportAll => 'Export all conversations';

  @override
  String get chatV2PaletteExportAllHint =>
      'One-shot JSON backup (incl. archived)';

  @override
  String get chatV2PaletteShortcuts => 'View keyboard shortcuts';

  @override
  String get chatV2PaletteShortcutsHint => 'Or Shift+?';

  @override
  String get chatV2PaletteSettings => 'Open chat preferences';

  @override
  String get chatV2PaletteSettingsHint => 'Font / default mode / default model';

  @override
  String get chatV2PaletteMultiSelect => 'Multi-select messages here';

  @override
  String get chatV2PaletteApplyTemplate => 'Apply system prompt template';

  @override
  String get chatV2PaletteApplyTemplateHint =>
      'Pick a saved one for current conversation';

  @override
  String get chatV2PaletteManageTemplates => 'Manage system prompt templates';

  @override
  String get chatV2PaletteManageTemplatesHint =>
      'Add / edit / delete saved prompts';

  @override
  String get chatV2PaletteSwitchHint => 'Switch to this conversation';

  @override
  String get chatV2ThreadStatusGenerating => 'Generating';

  @override
  String get chatV2ThreadStatusStopping => 'Stopping…';

  @override
  String get chatV2OverflowShareCopied => 'Share link copied';

  @override
  String chatV2OverflowShareCopiedUrl(Object url) {
    return 'Copied $url';
  }

  @override
  String get chatV2OverflowIdCopied => 'Thread ID copied';

  @override
  String get chatV2NewDialogTitle => 'New conversation';

  @override
  String get chatV2NewDialogTitleField => 'Title (optional)';

  @override
  String chatV2NewDialogTitleSuggested(Object suggested) {
    return 'Suggested: $suggested';
  }

  @override
  String get chatV2NewDialogModelLabel => 'Model';

  @override
  String get chatV2NewDialogRefreshTooltip => 'Refresh';

  @override
  String get chatV2NewDialogModelOfficial => 'BiuMind (official default)';

  @override
  String get chatV2NewDialogModelEmpty =>
      '(No models available — contact admin)';

  @override
  String chatV2NewDialogModelLoadFailed(Object err) {
    return 'Failed to load models: $err';
  }

  @override
  String get chatV2NewDialogSystemPromptLabel => 'System prompt (optional)';

  @override
  String get chatV2NewDialogSystemPromptHint =>
      '\"You are a…\" — leave blank for default';

  @override
  String get chatV2NewDialogPickWorker => 'Pick worker';

  @override
  String get chatV2NewDialogNoOnlineDaemon => 'No online daemon';

  @override
  String get chatV2NewDialogEmptyEnvAuto =>
      'BiuMind desktop auto-launches local biu serve — please wait or check that biu CLI is installed; or run `biu serve` manually.';

  @override
  String chatV2NewDialogEmptyEnvHistory(Object count) {
    return 'Agent mode requires a worker_kind=biu_daemon local process. $count historical worker(s) are not daemons or are offline.';
  }

  @override
  String chatV2NewDialogEnvLoadFailed(Object err) {
    return 'Environment list failed: $err';
  }

  @override
  String get chatV2NewDialogPoolTagLabel => 'runtime pool tag (optional)';

  @override
  String get chatV2NewDialogPoolTagHint =>
      'Empty = default pool; or e.g. \"gpu\" / \"high-mem\"';

  @override
  String get chatV2NewDialogTaskModeHint =>
      'Task mode lets brain dispatch the task to a runtime worker matching pool_tag. Tasks queue when no worker matches.';

  @override
  String get chatV2NewDialogCreate => 'Create';

  @override
  String get chatV2NewDialogModeChat => 'Chat';

  @override
  String get chatV2NewDialogModeChatHint => 'Talk to the model directly';

  @override
  String get chatV2NewDialogModeAgent => 'Agent';

  @override
  String get chatV2NewDialogModeAgentHint => 'Run tools on a specific worker';

  @override
  String get chatV2NewDialogModeTask => 'Task';

  @override
  String get chatV2NewDialogModeTaskHint => 'Background task execution';

  @override
  String get chatV2ShortcutsTitle => 'Keyboard shortcuts';

  @override
  String get chatV2ShortcutsSectionInput => 'Input box';

  @override
  String get chatV2ShortcutsSectionMessages => 'Messages';

  @override
  String get chatV2ShortcutsSectionGlobal => 'Global';

  @override
  String get chatV2ShortcutsSend => 'Send message';

  @override
  String get chatV2ShortcutsNewline => 'Newline (no send)';

  @override
  String get chatV2ShortcutsHistoryUp => 'Previous draft (when input empty)';

  @override
  String get chatV2ShortcutsHistoryDown => 'Next draft / leave history';

  @override
  String get chatV2ShortcutsSlash => 'Open slash command palette';

  @override
  String get chatV2ShortcutsEsc => 'Close palette / search bar';

  @override
  String get chatV2ShortcutsInThreadSearch => 'Search in current conversation';

  @override
  String get chatV2ShortcutsSearchNext => 'Search bar: next match';

  @override
  String get chatV2ShortcutsSearchPrev => 'Search bar: previous match';

  @override
  String get chatV2ShortcutsPalette => 'Open command palette';

  @override
  String get chatV2ShortcutsNewThread => 'New conversation';

  @override
  String get chatV2ShortcutsPinThread => 'Pin / unpin current conversation';

  @override
  String get chatV2ShortcutsCrossSearch => 'Search all conversations';

  @override
  String get chatV2ShortcutsModelPicker =>
      'Switch model for current conversation';

  @override
  String get chatV2ShortcutsHelp => 'Open this help panel';

  @override
  String get chatV2ArchivedTitle => 'Archived conversations';

  @override
  String get chatV2ArchivedClose => 'Close';

  @override
  String get chatV2ArchivedEmpty => 'No archived conversations';

  @override
  String get chatV2ArchivedUnarchive => 'Unarchive';

  @override
  String get chatV2ArchivedHardDelete => 'Delete forever';

  @override
  String get chatV2ArchivedHardDeleteTitle => 'Delete forever';

  @override
  String chatV2ArchivedHardDeleteBody(Object title) {
    return 'Permanently delete \"$title\"? This cannot be undone.';
  }

  @override
  String get chatV2DraftsTitle => 'Drafts';

  @override
  String get chatV2DraftsEmpty =>
      'No drafts. Anything you type in any conversation is auto-saved.';

  @override
  String get chatV2DraftsUnnamed => '(Unnamed / deleted)';

  @override
  String chatV2DraftsCharCount(Object count) {
    return '$count chars';
  }

  @override
  String get chatV2DraftsDiscard => 'Discard this draft';

  @override
  String get chatV2StarredTitle => 'Starred messages';

  @override
  String get chatV2StarredEmpty =>
      'No starred messages yet. Tap the ⭐ at the bottom of an assistant message to save it.';

  @override
  String get chatV2StarredNoText => '(No text content)';

  @override
  String get chatV2CrossSearchHint => 'Search all conversations…';

  @override
  String chatV2CrossSearchHitCount(Object count) {
    return '$count hits';
  }

  @override
  String get chatV2CrossSearchCloseTooltip => 'Close (Esc)';

  @override
  String get chatV2CrossSearchEmptyHint =>
      'Type a keyword to search all history\n(press Esc to close)';

  @override
  String get chatV2CrossSearchNoMatch => 'No matches';

  @override
  String get chatV2PaletteSearchHint => 'Search commands…';

  @override
  String get chatV2PaletteNoMatch => 'No matching commands';

  @override
  String get chatV2InThreadSearchHint => 'Search in current conversation…';

  @override
  String get chatV2InThreadSearchPrev => 'Previous (Shift+Enter)';

  @override
  String get chatV2InThreadSearchNext => 'Next (Enter)';

  @override
  String get chatV2HintIntro => 'Tip: press ';

  @override
  String get chatV2HintBeforeCrossSearch =>
      ' to summon command palette; press ';

  @override
  String get chatV2HintAfterCrossSearch => ' for cross-thread search; ';

  @override
  String get chatV2HintAfterHelp => ' for all shortcuts.';

  @override
  String get chatV2ChangelogSubtitle =>
      'Command palette · Cross-thread search · Drafts · Favorites · Model tips';

  @override
  String get chatV2ChangelogBullet1 =>
      '🔍 Cross-thread search (Cmd/Ctrl+Shift+F) + command palette (Cmd/Ctrl+K)';

  @override
  String get chatV2ChangelogBullet2 =>
      '⭐ Starred-message sidebar + draft index + prompt templates';

  @override
  String get chatV2ChangelogBullet3 =>
      '📤 One-click import/export (incl. bulk backup)';

  @override
  String get chatV2ChangelogBullet4 =>
      '🎨 Code blocks: line numbers / wrap / language switch / save to file';

  @override
  String get chatV2ChangelogBullet5 =>
      '⚡ Multimodal attachments / slash skill invocation / streaming token/s';

  @override
  String get chatV2ChangelogDetails => 'Details';

  @override
  String chatV2SelectionSelectedCount(Object count) {
    return '$count selected';
  }

  @override
  String get chatV2SelectionSelectAll => 'Select all';

  @override
  String get chatV2SelectionCopy => 'Copy';

  @override
  String get chatV2SelectionTranslate => 'Translate';

  @override
  String get chatV2SelectionExportMd => 'Export MD';

  @override
  String get chatV2SelectionDelete => 'Delete';

  @override
  String get chatV2SelectionCancel => 'Cancel';

  @override
  String chatV2SelectionCopiedCount(Object count) {
    return 'Copied $count messages';
  }

  @override
  String get chatV2SelectionTruncated =>
      'Text too long, truncated to 4500 chars';

  @override
  String chatV2SelectionTranslateFailed(Object err) {
    return 'Translate open failed: $err';
  }

  @override
  String chatV2SelectionExportedCount(Object count) {
    return 'Exported $count messages';
  }

  @override
  String get chatV2SelectionDeleteTitle => 'Delete messages';

  @override
  String chatV2SelectionDeleteBody(Object count) {
    return 'Delete the $count selected messages? This cannot be undone.';
  }

  @override
  String get chatV2SelectionMdUnnamed => '(Untitled)';

  @override
  String get chatV2SelectionMdModelUnset => '(unset)';

  @override
  String get chatV2TemplatesTitle => 'System prompt templates';

  @override
  String get chatV2TemplatesNew => 'New';

  @override
  String get chatV2TemplatesEmpty =>
      'No templates yet. Tap \"New\" in the top-right to save a frequently-used system prompt.';

  @override
  String get chatV2TemplatesApply => 'Apply';

  @override
  String get chatV2TemplatesEdit => 'Edit';

  @override
  String get chatV2TemplatesDelete => 'Delete';

  @override
  String get chatV2TemplatesDeleteTitle => 'Delete template';

  @override
  String chatV2TemplatesDeleteBody(Object name) {
    return 'Delete \"$name\"? This cannot be undone.';
  }

  @override
  String get chatV2TemplatesEditDialogNew => 'New template';

  @override
  String get chatV2TemplatesEditDialogEdit => 'Edit template';

  @override
  String get chatV2TemplatesNameLabel => 'Name';

  @override
  String get chatV2TemplatesNameHint => 'e.g. Flutter architect';

  @override
  String get chatV2TemplatesContentLabel => 'System prompt';

  @override
  String get chatV2TemplatesContentHint => 'Enter the full system prompt…';

  @override
  String get chatV2SettingsSheetTitle => 'Conversation settings';

  @override
  String chatV2SettingsSheetSaveFailed(Object err) {
    return 'Save failed: $err';
  }

  @override
  String get chatV2SettingsSheetNotFound => 'Conversation not found';

  @override
  String get chatV2SettingsSheetMode => 'Mode';

  @override
  String get chatV2SettingsSheetModel => 'Model';

  @override
  String get chatV2SettingsSheetModelDefault => 'Default';

  @override
  String get chatV2SettingsSheetCreated => 'Created';

  @override
  String get chatV2SettingsSheetUpdated => 'Updated';

  @override
  String get chatV2SettingsSheetFromTemplate => 'From template';

  @override
  String get chatV2SettingsSheetClear => 'Clear';

  @override
  String get chatV2SettingsSheetHint =>
      'Attached as a system message to every request; tap \"Save\" top-right.';

  @override
  String get chatV2SettingsSheetPromptHint =>
      'e.g. You are a senior Flutter architect; always cite file + line number.';

  @override
  String get chatV2AttachRemove => 'Remove';

  @override
  String get chatV2OverflowMore => 'More';

  @override
  String get chatV2OverflowPin => 'Pin';

  @override
  String get chatV2OverflowUnpin => 'Unpin';

  @override
  String get chatV2OverflowRename => 'Rename';

  @override
  String get chatV2OverflowArchive => 'Archive';

  @override
  String get chatV2OverflowExportJson => 'Export JSON';

  @override
  String get chatV2OverflowShareLink => 'Copy share link';

  @override
  String get chatV2OverflowCopyId => 'Copy thread ID';

  @override
  String get chatV2OverflowDelete => 'Delete';

  @override
  String get chatV2OverflowDeleteConfirmTitle => 'Delete conversation';

  @override
  String chatV2OverflowDeleteConfirmBody(Object title) {
    return 'Delete \"$title\"? This cannot be undone.';
  }

  @override
  String get chatV2RenameDialogTitle => 'Rename conversation';

  @override
  String get chatV2RenameDialogHint => 'Enter new name';

  @override
  String get chatV2DialogCancel => 'Cancel';

  @override
  String get chatV2DialogSave => 'Save';

  @override
  String get chatV2DialogDelete => 'Delete';

  @override
  String chatV2ApprovalTitle(Object toolName) {
    return 'Allow $toolName?';
  }

  @override
  String get chatV2ApprovalAllow => 'Allow';

  @override
  String get chatV2ApprovalDeny => 'Deny';

  @override
  String get chatV2ApprovalAlways => 'Always allow';

  @override
  String get chatV2ApprovalShowMore => 'Show full input ▾';

  @override
  String get chatV2ComposerModeChat => 'Chat';

  @override
  String get chatV2ComposerModeAgent => 'Agent';

  @override
  String get chatV2ComposerModeChatHint => 'Pure model — no tools';

  @override
  String get chatV2ComposerModeAgentHint => 'With tools via daemon';

  @override
  String get chatV2ComposerModeNoDaemon => 'No daemon online';

  @override
  String get chatV2ComposerWorkdirSet => 'Set working directory';

  @override
  String get chatV2ComposerWorkdirNone => 'No workdir';

  @override
  String get chatV2ComposerWorkdirClear => 'Clear workdir';

  @override
  String get chatV2ComposerAutoApproveAuto => 'Auto approve';

  @override
  String get chatV2ComposerAutoApproveWhitelist => 'Whitelist';

  @override
  String get chatV2ComposerAutoApproveManual => 'Manual';

  @override
  String get chatV2ComposerAutoApproveTooltip => 'Tool-call approval mode';

  @override
  String get chatV2ModelPickerSearchHint => 'Search models…';

  @override
  String get chatV2ModelPickerSettings => 'Settings';

  @override
  String get chatV2ModelPickerEmpty => 'No models available';

  @override
  String get chatV2ModelPickerEmptyAction => 'Open AI providers';

  @override
  String get chatV2ModelPickerNoMatch => 'No models match';

  @override
  String get chatV2ReasoningStreaming => 'Thinking…';

  @override
  String get chatV2ReasoningClosed => 'Reasoning';

  @override
  String get chatV2ReasoningExpand => 'Expand';

  @override
  String get chatV2ReasoningCollapse => 'Collapse';

  @override
  String get chatV2ComposerAttachNoVision =>
      'Current model does not support image input — switch to a vision-capable model';

  @override
  String chatV2CtxBarTooltip(Object pct, Object total, Object used) {
    return 'Context: $used / $total tokens · $pct%';
  }

  @override
  String get wikiTitle => 'Wiki';

  @override
  String get wikiNoCreds => 'No model-relay credentials configured.';

  @override
  String get wikiOpenSettings => 'Open Settings';

  @override
  String get wikiNoProjects => 'No projects yet — create one to start.';

  @override
  String get wikiCreateProject => 'Create project';

  @override
  String get wikiNewPageButton => 'New page';

  @override
  String get wikiNewPageDialogTitle => 'New page';

  @override
  String get wikiSelectPageHint => 'Select a page or create one to start.';

  @override
  String get graphTitle => 'Graph';

  @override
  String graphErrorPrefix(Object message) {
    return 'Graph error: $message';
  }

  @override
  String get graphAliasesLabel => 'Aliases';

  @override
  String get graphSummaryLabel => 'Summary';

  @override
  String get graphPathLabel => 'Path';

  @override
  String commonError(Object message) {
    return 'Error: $message';
  }

  @override
  String get commonNotFound => 'Not found.';

  @override
  String get commonOk => 'OK';

  @override
  String get commonCancel => 'Cancel';

  @override
  String get commonDelete => 'Delete';

  @override
  String get commonCreate => 'Create';

  @override
  String get commonRetry => 'Retry';

  @override
  String get navAdmin => 'Admin';

  @override
  String get adminTitle => 'Admin';

  @override
  String get adminTabUsers => 'Users';

  @override
  String get adminTabAudit => 'Audit log';

  @override
  String get adminSearchHint => 'Search by email or id…';

  @override
  String get adminEmptyUsers => 'No users match this filter.';

  @override
  String get adminEmptyAudit => 'No audit events yet.';

  @override
  String get adminColEmail => 'Email';

  @override
  String get adminColPlan => 'Plan';

  @override
  String get adminColCreated => 'Joined';

  @override
  String get adminUserDetails => 'User details';

  @override
  String get adminLimitsTitle => 'Plan limits';

  @override
  String get adminFieldRPM => 'model-relay RPM';

  @override
  String get adminFieldTPM => 'model-relay TPM';

  @override
  String get adminFieldSandboxDaily => 'Sandbox / day';

  @override
  String get adminFieldSandboxConcurrent => 'Sandbox concurrent';

  @override
  String get adminFieldMemoryQuota => 'Memories / project';

  @override
  String get adminFieldBrainProjects => 'Projects / user';

  @override
  String get adminChangePlan => 'Change plan';

  @override
  String get adminPlanReason => 'Reason (audit log)';

  @override
  String get adminPlanApply => 'Apply';

  @override
  String get adminPlanApplied => 'Plan updated';

  @override
  String get adminAuditAt => 'When';

  @override
  String get adminAuditActor => 'Actor';

  @override
  String get adminAuditAction => 'Action';

  @override
  String get adminAuditTarget => 'Target';

  @override
  String get adminAuditDetail => 'Detail';

  @override
  String get appsTitle => 'App Center';

  @override
  String get appsManage => 'Manage';

  @override
  String get appsManageTitle => 'App management';

  @override
  String get appsRefresh => 'Refresh';

  @override
  String get appsSearchHint => 'Search apps';

  @override
  String get appsEmpty => 'No apps match your filters.';

  @override
  String get appsNoInstalls =>
      'No apps installed yet — pick one from the App Center.';

  @override
  String get appsInstall => 'Install';

  @override
  String get appsUninstall => 'Uninstall';

  @override
  String get appsOpen => 'Open';

  @override
  String get appsCancel => 'Cancel';

  @override
  String get appsInstalled => 'Installed';

  @override
  String get appsCategoryAll => 'All';

  @override
  String appsCategoryInstalled(Object count) {
    return 'Installed ($count)';
  }

  @override
  String get appsCategoryProductivity => 'Productivity';

  @override
  String get appsCategoryContent => 'Content';

  @override
  String get appsCategoryData => 'Data';

  @override
  String get appsCategoryComm => 'Comm';

  @override
  String get appsCategoryDev => 'Dev';

  @override
  String get appsCategoryUtility => 'Utility';

  @override
  String get appsConfigureFirst =>
      'Configure BiuMind model-relay credentials in Settings first to unlock the App Center.';

  @override
  String appsInstallTitle(Object name, Object version) {
    return 'Install $name v$version';
  }

  @override
  String get appsNoPermissionRequested => 'This app requests no permissions.';

  @override
  String appsInstalledToast(Object name) {
    return '$name installed.';
  }

  @override
  String appsUninstalledToast(Object name) {
    return '$name uninstalled.';
  }

  @override
  String get appsUninstallTitle => 'Uninstall app?';

  @override
  String appsUninstallConfirm(Object identifier) {
    return 'Uninstall $identifier? Data created by the app is retained; clean it up separately from the management page.';
  }

  @override
  String get appsSectionPermissions => 'Permissions';

  @override
  String get appsSectionViews => 'Views';

  @override
  String get appsSectionTriggers => 'Triggers';

  @override
  String get appsSectionSkills => 'Bundled skills';

  @override
  String get appsErrNetwork =>
      'Network error. Check your connection and try again.';

  @override
  String get appsErrUnauthorized => 'Session expired. Please sign in again.';

  @override
  String get appsErrNotInstalled =>
      'Install the app first to invoke its actions.';

  @override
  String get appsErrInstallDisabled =>
      'This app is currently disabled — enable it from app settings.';

  @override
  String get appsErrNotFound => 'The target no longer exists.';

  @override
  String get appsErrConflict =>
      'Conflict with the latest state — refresh and retry.';

  @override
  String appsErrValidation(Object detail) {
    return 'Invalid request: $detail';
  }

  @override
  String get appsErrRateLimit => 'Too many requests. Please try again shortly.';

  @override
  String appsErrServer(Object status) {
    return 'Service temporarily unavailable ($status). Please retry.';
  }

  @override
  String appsErrUnknown(Object detail) {
    return 'Operation failed: $detail';
  }

  @override
  String get permNetOutbound =>
      'Outbound network access. Limited to domains listed in the manifest.';

  @override
  String get permHubInvoke =>
      'Call language models (counts against your model-relay quota).';

  @override
  String get permGraphRead => 'Read knowledge-graph nodes / edges.';

  @override
  String get permGraphWrite => 'Write knowledge-graph nodes / edges.';

  @override
  String get permMemoryRead => 'Read your multi-tier memory.';

  @override
  String get permMemoryWrite => 'Write to your multi-tier memory.';

  @override
  String get permFilesRead => 'Read Files content scoped to this app.';

  @override
  String get permFilesWrite => 'Write Files content scoped to this app.';

  @override
  String get permCronRegister => 'Register scheduled jobs (cron).';

  @override
  String get permWebhookRegister => 'Register webhook receiver paths.';

  @override
  String get permNotifySend => 'Send notifications to you.';

  @override
  String get permSandboxExec =>
      'Execute commands in an isolated sandbox (high risk).';

  @override
  String get permOauth => 'Sign in to third-party accounts via OAuth.';

  @override
  String get permSecretsRead =>
      'Read vault credentials (high risk, enterprise only).';

  @override
  String get sidebarCustomizeTitle => 'Customize sidebar';

  @override
  String get sidebarCollapse => 'Collapse sidebar';

  @override
  String get sidebarExpand => 'Expand sidebar';

  @override
  String get sidebarModeHidden => 'Hide sidebar';

  @override
  String get sidebarModeIconsOnly => 'Icons only';

  @override
  String get sidebarModeExpanded => 'Icons and text';

  @override
  String get sidebarRestoreDefaults => 'Restore defaults';

  @override
  String get sidebarSave => 'Save';

  @override
  String get sidebarSaving => 'Saving…';

  @override
  String get sidebarSaved => 'Sidebar saved.';

  @override
  String get sidebarConflict =>
      'Another device updated this sidebar — reloaded latest.';

  @override
  String get sidebarSectionSystem => 'System (toggle visibility)';

  @override
  String get sidebarSectionPinned => 'Pinned apps';

  @override
  String get sidebarSectionAvailable => 'Installed apps you can pin';

  @override
  String get sidebarHidden => 'Hidden from sidebar';

  @override
  String get sidebarPin => 'Pin';

  @override
  String get sidebarPinnedEmpty =>
      'No apps pinned yet — pin one from the list below.';

  @override
  String get sidebarPinnedOrphan =>
      'App no longer installed — remove this entry.';

  @override
  String get sidebarMoveUp => 'Move up';

  @override
  String get sidebarMoveDown => 'Move down';

  @override
  String get sidebarRemove => 'Remove';

  @override
  String get sidebarPinAction => 'Pin to sidebar';

  @override
  String get sidebarUnpinAction => 'Unpin from sidebar';

  @override
  String get sidebarCustomizeAction => 'Customize sidebar…';

  @override
  String get sidebarPinnedToast => 'Pinned to sidebar.';

  @override
  String get sidebarUnpinnedToast => 'Removed from sidebar.';

  @override
  String get sidebarPinNeedsInstall => 'Install the app first to pin it.';

  @override
  String get sidebarPinSuggestionAction => 'Add to sidebar';

  @override
  String get sidebarPinSuggestionDismiss => 'Not now';

  @override
  String get sidebarQueuedOffline =>
      'Network unreachable — edit queued, will sync on reconnect.';

  @override
  String get sidebarOutboxBanner => 'Sidebar edit pending sync (offline).';

  @override
  String upgradeTitle(Object from, Object name, Object to) {
    return 'Upgrade $name: v$from → v$to';
  }

  @override
  String get upgradeNoNewPerms =>
      'No new permissions required — safe to upgrade.';

  @override
  String get upgradeNeedsApproval =>
      'This upgrade requests new permissions. Review and check each one before applying.';

  @override
  String get upgradeSectionAdded => 'New permissions';

  @override
  String get upgradeSectionRemoved => 'No longer requested';

  @override
  String get upgradeSectionUnchanged => 'Already granted';

  @override
  String get upgradeCancel => 'Not now';

  @override
  String get upgradeApply => 'Upgrade';

  @override
  String get upgradeBannerTitle => 'Upgrades available';

  @override
  String upgradeBannerSubtitle(Object count) {
    return '$count app(s) have a newer version waiting';
  }

  @override
  String upgradeRowVersion(Object from, Object to) {
    return 'v$from → v$to';
  }

  @override
  String get upgradeAvailable => 'Upgrade available';

  @override
  String get upgradeAppliedToast => 'Upgraded.';

  @override
  String get repoUpgradeConfirmTitle => 'Update GitHub app';

  @override
  String repoUpgradeConfirmBody(Object version) {
    return 'Will update to $version. The app will be briefly unavailable while updating.';
  }

  @override
  String get repoUpgradeLatestVersion => 'the latest version';

  @override
  String get repoUpgradeUnsupportedPlatform =>
      'Updating GitHub apps is not supported on this platform (available in the macOS / Linux client).';

  @override
  String repoUpgradeBadRepoUrl(Object url) {
    return 'Cannot derive the app name from the repo URL: $url';
  }

  @override
  String get heroGreetingMorning => 'Good morning';

  @override
  String get heroGreetingNoon => 'Good day';

  @override
  String get heroGreetingAfternoon => 'Good afternoon';

  @override
  String get heroGreetingEvening => 'Good evening';

  @override
  String get heroGreetingNight => 'Still up?';

  @override
  String get heroSubtitleNoThread => 'What would you like to chat about today?';

  @override
  String get heroSubtitleEmptyThread => 'Start your conversation';

  @override
  String get heroRecentSection => 'Recent conversations';

  @override
  String get heroRecentEmpty => 'No conversations yet';

  @override
  String heroRelativeMinutes(Object n) {
    return '${n}m ago';
  }

  @override
  String heroRelativeHours(Object n) {
    return '${n}h ago';
  }

  @override
  String heroRelativeDays(Object n) {
    return '${n}d ago';
  }

  @override
  String get heroRelativeJustNow => 'Just now';

  @override
  String heroCurrentModel(Object model) {
    return 'Current model: $model';
  }

  @override
  String get heroSignInBanner => 'Not signed in — tap to go to login';

  @override
  String get starterPromptWritingTitle => 'Writing assistant';

  @override
  String get starterPromptWritingHint => 'Polish a piece of text';

  @override
  String get starterPromptWritingPrompt =>
      'Please polish the following text to make it more professional:\n\n';

  @override
  String get starterPromptCodeTitle => 'Code review';

  @override
  String get starterPromptCodeHint => 'Review the code I paste';

  @override
  String get starterPromptCodePrompt =>
      'Please review the following code and point out improvements:\n\n```\n\n```';

  @override
  String get starterPromptResearchTitle => 'Deep research';

  @override
  String get starterPromptResearchHint => 'Expand on a topic';

  @override
  String get starterPromptResearchPrompt =>
      'Please analyze the following topic in depth, offering multiple perspectives:\n\n';

  @override
  String get starterPromptTranslateTitle => 'Translate';

  @override
  String get starterPromptTranslateHint => 'EN ⇄ ZH';

  @override
  String get starterPromptTranslatePrompt =>
      'Please translate the following content while preserving meaning and tone:\n\n';

  @override
  String get starterPromptDataTitle => 'Data analysis';

  @override
  String get starterPromptDataHint => 'Analyze data';

  @override
  String get starterPromptDataPrompt =>
      'Please analyze the following data and surface key insights:\n\n';

  @override
  String get starterPromptIdeasTitle => 'Brainstorm';

  @override
  String get starterPromptIdeasHint => 'Generate ideas';

  @override
  String get starterPromptIdeasPrompt =>
      'Please give me 10 creative ideas around the following topic:\n\n';

  @override
  String get navCreation => 'Create';

  @override
  String get navProfile => 'Me';

  @override
  String get creationInspiration => 'Inspiration';

  @override
  String get creationStudio => 'Studio';

  @override
  String get creationWorks => 'My Works';

  @override
  String get creationGallery => 'Gallery';

  @override
  String get creationRecharge => 'Credits';

  @override
  String get creationHeroTitle => 'Create';

  @override
  String get creationHeroSubtitle =>
      'Multimodal AIGC engine — make ideas tangible';

  @override
  String get creationTabImage => 'Image';

  @override
  String get creationTabVideo => 'Video';

  @override
  String get creationTabDigitalHuman => 'Digital Human';

  @override
  String get creationTabHotparse => 'Hot Parse';

  @override
  String get creationPromptHint => 'Describe what you want to generate...';

  @override
  String get creationFirstFrame => 'First Frame';

  @override
  String get creationLastFrame => 'Last Frame';

  @override
  String get creationReferenceImage => 'Reference';

  @override
  String get creationAiOptimize => 'AI Optimize';

  @override
  String get creationSharePublic => 'Share Publicly';

  @override
  String get creationSubmit => 'Generate';

  @override
  String get creationCardPending => 'Pending';

  @override
  String get creationCardQueued => 'Queued';

  @override
  String get creationCardRunning => 'Generating';

  @override
  String get creationCardCompleted => 'Completed';

  @override
  String get creationCardFailed => 'Failed';

  @override
  String get creationCardBlocked => 'Blocked by moderation';

  @override
  String get creationCardCancelled => 'Cancelled';

  @override
  String get creationActionRetry => 'Retry';

  @override
  String get creationActionRedo => 'Regenerate';

  @override
  String get creationActionEdit => 'Edit';

  @override
  String get creationActionDelete => 'Delete';

  @override
  String get creationActionDownload => 'Download';

  @override
  String get creationActionMakeSimilar => 'Make Similar';

  @override
  String get creationActionShare => 'Share';

  @override
  String get creationActionPublic => 'Make Public';

  @override
  String get creationActionPrivate => 'Make Private';

  @override
  String get creationActionCancel => 'Cancel';

  @override
  String creationCreditCost(Object n) {
    return '$n credits';
  }

  @override
  String creationCreditRefunded(Object n) {
    return 'Refunded $n credits';
  }

  @override
  String get creationCreditInsufficient =>
      'Not enough credits — top up to continue';

  @override
  String get creationErrorEmptyPrompt =>
      'Please describe what you want to generate';

  @override
  String get creationErrorModelNotFound => 'Model not available';

  @override
  String get creationOfflineBanner => 'Offline — sync paused';

  @override
  String get membershipCenterTitle => 'Membership';

  @override
  String get membershipCurrentPlan => 'Current plan';

  @override
  String get membershipChoosePlan => 'Choose a plan';

  @override
  String get membershipPlanCompareTitle => 'Compare plans';

  @override
  String get membershipOrdersTitle => 'Order history';

  @override
  String get membershipOrdersEmpty => 'No orders yet';

  @override
  String get membershipCheckoutTitle => 'Checkout';

  @override
  String get membershipNotLoggedIn => 'Sign in to view membership status';

  @override
  String get membershipBadgeCurrent => 'Current';

  @override
  String get membershipCtaSelect => 'Select';

  @override
  String get membershipCtaCurrent => 'Current plan';

  @override
  String get membershipCtaUpgrade => 'Upgrade';

  @override
  String get membershipCtaDowngrade => 'Downgrade';

  @override
  String get membershipPriceFree => 'Free';

  @override
  String get membershipPricePerMonth => '/ mo';

  @override
  String get membershipPricePerYear => '/ yr';

  @override
  String get membershipQuotaChat => 'Chat monthly quota';

  @override
  String get membershipQuotaAIGC => 'AIGC monthly quota';

  @override
  String get membershipActionCancel => 'Cancel subscription';

  @override
  String get membershipActionResume => 'Resume subscription';

  @override
  String get membershipResumed => 'Subscription resumed';

  @override
  String get membershipCancelTitle => 'Cancel subscription';

  @override
  String get membershipCancelOptionPeriodEnd => 'Cancel at period end';

  @override
  String get membershipCancelOptionImmediate => 'Cancel now + prorated refund';

  @override
  String get membershipCancelHint =>
      'Tap \"Resume\" before period_end to undo cancellation';

  @override
  String get membershipCancelDeny => 'Not now';

  @override
  String get membershipCancelConfirm => 'Confirm cancel';

  @override
  String get membershipCanceledImmediate => 'Subscription canceled immediately';

  @override
  String get membershipCanceledPeriodEnd =>
      'Subscription will cancel at period end';

  @override
  String get membershipUpgradeImmediate => 'Effective immediately, prorated';

  @override
  String get membershipUpgradeRefund => 'Old plan unused credit';

  @override
  String get membershipUpgradeNewCharge => 'New plan prorated charge';

  @override
  String get membershipUpgradeNetCharge => 'Total due now';

  @override
  String get membershipDowngradeAt => 'Downgrade effective at period end';

  @override
  String get membershipUpgradeContinue => 'Continue to pay';

  @override
  String get membershipDowngradeConfirm => 'Confirm downgrade';

  @override
  String get membershipPaymentMethodTitle => 'Payment method';

  @override
  String get membershipPaymentWechatNative => 'WeChat Pay (QR)';

  @override
  String get membershipPaymentWechatH5 => 'WeChat Pay (H5)';

  @override
  String get membershipPaymentAlipayPC => 'Alipay (Web)';

  @override
  String get membershipPaymentAlipayWap => 'Alipay (Mobile)';

  @override
  String get membershipPaymentStripe => 'Credit card';

  @override
  String get membershipCheckoutOrderTitle => 'Order details';

  @override
  String get membershipCheckoutPay => 'Pay now';

  @override
  String get membershipCheckoutWechatScan => 'Scan with WeChat to pay';

  @override
  String get membershipCheckoutH5Opened => 'WeChat H5 payment opened';

  @override
  String get membershipCheckoutRedirected =>
      'Redirected to payment page, return after completion';

  @override
  String get membershipOrderProviderWechat => 'WeChat Pay';

  @override
  String get membershipOrderProviderAlipay => 'Alipay';

  @override
  String get membershipOrderProviderStripe => 'Stripe';

  @override
  String get membershipOrderStatusPaid => 'Paid';

  @override
  String get membershipOrderStatusPending => 'Pending';

  @override
  String get membershipOrderStatusRefunded => 'Refunded';

  @override
  String get membershipOrderStatusFailed => 'Failed';

  @override
  String get membershipOrderStatusCanceled => 'Canceled';

  @override
  String get membershipCouponTitle => 'Redeem code';

  @override
  String get membershipCouponHint => 'Enter your code to redeem instantly';

  @override
  String get membershipCouponSubmit => 'Redeem';

  @override
  String get membershipCouponSuccess => 'Redeemed';

  @override
  String get membershipCouponNotFound => 'Invalid code';

  @override
  String get membershipCouponExpired => 'Code expired';

  @override
  String get membershipCouponInactive => 'Code disabled';

  @override
  String get membershipCouponAlreadyUsed => 'You have already used this code';

  @override
  String get membershipReferralTitle => 'Invite friends';

  @override
  String get membershipReferralYourCode => 'Your invite code';

  @override
  String get membershipReferralStats => 'Stats';

  @override
  String get membershipReferralStatTotal => 'Total';

  @override
  String get membershipReferralStatRewarded => 'Rewarded';

  @override
  String get membershipReferralStatPending => 'Pending';

  @override
  String get membershipReferralStatReverted => 'Reverted';

  @override
  String get membershipReferralShare => 'Share';

  @override
  String get membershipReferralCopyCode => 'Code copied';

  @override
  String get membershipReferralCopyLink => 'Link copied';

  @override
  String get membershipReferralRulesTitle => 'How rewards work';

  @override
  String get chatV2HintDismiss => 'Don\'t show again';

  @override
  String get chatV2ChangelogHeadline => 'BiuMind Chat picked up 5 new tools';

  @override
  String get commonForbidden => 'Forbidden — you don\'t own this resource.';

  @override
  String get appsPermissionRequestIntro =>
      'This app requests the following permissions. Uncheck any you don\'t want to grant; the server enforces only the granted subset.';

  @override
  String get appsErrForbidden =>
      'You don\'t have permission to perform this action.';

  @override
  String get permWikiRead =>
      'Read your Wiki content (limited to this app\'s namespace).';

  @override
  String get permWikiWrite =>
      'Write to your Wiki (limited to this app\'s namespace).';
}
