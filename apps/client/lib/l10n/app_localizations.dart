import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:intl/intl.dart' as intl;

import 'app_localizations_en.dart';
import 'app_localizations_zh.dart';

// ignore_for_file: type=lint

/// Callers can lookup localized strings with an instance of AppLocalizations
/// returned by `AppLocalizations.of(context)`.
///
/// Applications need to include `AppLocalizations.delegate()` in their app's
/// `localizationDelegates` list, and the locales they support in the app's
/// `supportedLocales` list. For example:
///
/// ```dart
/// import 'l10n/app_localizations.dart';
///
/// return MaterialApp(
///   localizationsDelegates: AppLocalizations.localizationsDelegates,
///   supportedLocales: AppLocalizations.supportedLocales,
///   home: MyApplicationHome(),
/// );
/// ```
///
/// ## Update pubspec.yaml
///
/// Please make sure to update your pubspec.yaml to include the following
/// packages:
///
/// ```yaml
/// dependencies:
///   # Internationalization support.
///   flutter_localizations:
///     sdk: flutter
///   intl: any # Use the pinned version from flutter_localizations
///
///   # Rest of dependencies
/// ```
///
/// ## iOS Applications
///
/// iOS applications define key application metadata, including supported
/// locales, in an Info.plist file that is built into the application bundle.
/// To configure the locales supported by your app, you’ll need to edit this
/// file.
///
/// First, open your project’s ios/Runner.xcworkspace Xcode workspace file.
/// Then, in the Project Navigator, open the Info.plist file under the Runner
/// project’s Runner folder.
///
/// Next, select the Information Property List item, select Add Item from the
/// Editor menu, then select Localizations from the pop-up menu.
///
/// Select and expand the newly-created Localizations item then, for each
/// locale your application supports, add a new item and select the locale
/// you wish to add from the pop-up menu in the Value field. This list should
/// be consistent with the languages listed in the AppLocalizations.supportedLocales
/// property.
abstract class AppLocalizations {
  AppLocalizations(String locale)
    : localeName = intl.Intl.canonicalizedLocale(locale.toString());

  final String localeName;

  static AppLocalizations? of(BuildContext context) {
    return Localizations.of<AppLocalizations>(context, AppLocalizations);
  }

  static const LocalizationsDelegate<AppLocalizations> delegate =
      _AppLocalizationsDelegate();

  /// A list of this localizations delegate along with the default localizations
  /// delegates.
  ///
  /// Returns a list of localizations delegates containing this delegate along with
  /// GlobalMaterialLocalizations.delegate, GlobalCupertinoLocalizations.delegate,
  /// and GlobalWidgetsLocalizations.delegate.
  ///
  /// Additional delegates can be added by appending to this list in
  /// MaterialApp. This list does not have to be used at all if a custom list
  /// of delegates is preferred or required.
  static const List<LocalizationsDelegate<dynamic>> localizationsDelegates =
      <LocalizationsDelegate<dynamic>>[
        delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
      ];

  /// A list of this localizations delegate's supported locales.
  static const List<Locale> supportedLocales = <Locale>[
    Locale('en'),
    Locale('zh'),
  ];

  /// No description provided for @appTitle.
  ///
  /// In en, this message translates to:
  /// **'BiuMind'**
  String get appTitle;

  /// No description provided for @navChat.
  ///
  /// In en, this message translates to:
  /// **'Chat'**
  String get navChat;

  /// No description provided for @navWiki.
  ///
  /// In en, this message translates to:
  /// **'Wiki'**
  String get navWiki;

  /// No description provided for @navGraph.
  ///
  /// In en, this message translates to:
  /// **'Graph'**
  String get navGraph;

  /// No description provided for @navMemory.
  ///
  /// In en, this message translates to:
  /// **'Memory'**
  String get navMemory;

  /// No description provided for @navCode.
  ///
  /// In en, this message translates to:
  /// **'Code'**
  String get navCode;

  /// No description provided for @navSkills.
  ///
  /// In en, this message translates to:
  /// **'Skills'**
  String get navSkills;

  /// No description provided for @navSettings.
  ///
  /// In en, this message translates to:
  /// **'Settings'**
  String get navSettings;

  /// No description provided for @skillsTitle.
  ///
  /// In en, this message translates to:
  /// **'Skills'**
  String get skillsTitle;

  /// No description provided for @skillsRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get skillsRefresh;

  /// No description provided for @skillsAdd.
  ///
  /// In en, this message translates to:
  /// **'Add'**
  String get skillsAdd;

  /// No description provided for @skillsFilterAll.
  ///
  /// In en, this message translates to:
  /// **'All'**
  String get skillsFilterAll;

  /// No description provided for @skillsFilterBundled.
  ///
  /// In en, this message translates to:
  /// **'Bundled'**
  String get skillsFilterBundled;

  /// No description provided for @skillsFilterOrg.
  ///
  /// In en, this message translates to:
  /// **'Org'**
  String get skillsFilterOrg;

  /// No description provided for @skillsFilterMy.
  ///
  /// In en, this message translates to:
  /// **'My'**
  String get skillsFilterMy;

  /// No description provided for @skillsFilterMarketplace.
  ///
  /// In en, this message translates to:
  /// **'Marketplace'**
  String get skillsFilterMarketplace;

  /// No description provided for @skillsSelectHint.
  ///
  /// In en, this message translates to:
  /// **'Pick a skill to view details'**
  String get skillsSelectHint;

  /// No description provided for @skillsEmpty.
  ///
  /// In en, this message translates to:
  /// **'No skills yet — click + Add to install one'**
  String get skillsEmpty;

  /// No description provided for @skillsConfigureHint.
  ///
  /// In en, this message translates to:
  /// **'Sign in to manage cloud skills'**
  String get skillsConfigureHint;

  /// No description provided for @skillsPermissions.
  ///
  /// In en, this message translates to:
  /// **'Permissions'**
  String get skillsPermissions;

  /// No description provided for @skillsAutoAttachPaths.
  ///
  /// In en, this message translates to:
  /// **'Auto-attach paths'**
  String get skillsAutoAttachPaths;

  /// No description provided for @skillsBody.
  ///
  /// In en, this message translates to:
  /// **'SKILL.md body'**
  String get skillsBody;

  /// No description provided for @skillsDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get skillsDelete;

  /// No description provided for @skillsCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get skillsCancel;

  /// No description provided for @skillsInstall.
  ///
  /// In en, this message translates to:
  /// **'Install'**
  String get skillsInstall;

  /// No description provided for @skillsConfirmDeleteTitle.
  ///
  /// In en, this message translates to:
  /// **'Delete skill?'**
  String get skillsConfirmDeleteTitle;

  /// No description provided for @skillsConfirmDeleteBody.
  ///
  /// In en, this message translates to:
  /// **'Permanently delete \"{name}\"? This cannot be undone.'**
  String skillsConfirmDeleteBody(Object name);

  /// No description provided for @skillsInstallURL.
  ///
  /// In en, this message translates to:
  /// **'URL'**
  String get skillsInstallURL;

  /// No description provided for @skillsInstallURLHint.
  ///
  /// In en, this message translates to:
  /// **'Server fetches the SKILL.md over HTTPS'**
  String get skillsInstallURLHint;

  /// No description provided for @skillsInstallZip.
  ///
  /// In en, this message translates to:
  /// **'Zip'**
  String get skillsInstallZip;

  /// No description provided for @skillsInstallZipPick.
  ///
  /// In en, this message translates to:
  /// **'Pick .biuskill or .zip…'**
  String get skillsInstallZipPick;

  /// No description provided for @skillsInstallZipHint.
  ///
  /// In en, this message translates to:
  /// **'Bundle is uploaded as base64 (≤ 8 MB)'**
  String get skillsInstallZipHint;

  /// No description provided for @skillsInstallInline.
  ///
  /// In en, this message translates to:
  /// **'Inline'**
  String get skillsInstallInline;

  /// No description provided for @skillsInlineIdentifier.
  ///
  /// In en, this message translates to:
  /// **'Identifier'**
  String get skillsInlineIdentifier;

  /// No description provided for @skillsInlineName.
  ///
  /// In en, this message translates to:
  /// **'Name'**
  String get skillsInlineName;

  /// No description provided for @skillsInlineDescription.
  ///
  /// In en, this message translates to:
  /// **'Description'**
  String get skillsInlineDescription;

  /// No description provided for @skillsInlineBody.
  ///
  /// In en, this message translates to:
  /// **'SKILL.md body'**
  String get skillsInlineBody;

  /// No description provided for @skillsErrURLRequired.
  ///
  /// In en, this message translates to:
  /// **'URL required'**
  String get skillsErrURLRequired;

  /// No description provided for @skillsErrZipRequired.
  ///
  /// In en, this message translates to:
  /// **'Pick a .biuskill / .zip file'**
  String get skillsErrZipRequired;

  /// No description provided for @skillsErrInlineRequired.
  ///
  /// In en, this message translates to:
  /// **'identifier, name, description, body all required'**
  String get skillsErrInlineRequired;

  /// No description provided for @skillsErrTooLarge.
  ///
  /// In en, this message translates to:
  /// **'Upload too large — server max is 8 MB'**
  String get skillsErrTooLarge;

  /// No description provided for @chatPickSkills.
  ///
  /// In en, this message translates to:
  /// **'Mention skills'**
  String get chatPickSkills;

  /// No description provided for @chatPickSkillsFilter.
  ///
  /// In en, this message translates to:
  /// **'Filter…'**
  String get chatPickSkillsFilter;

  /// No description provided for @chatPickSkillsEmpty.
  ///
  /// In en, this message translates to:
  /// **'No active skills — install some in Skills'**
  String get chatPickSkillsEmpty;

  /// No description provided for @chatPickSkillsClear.
  ///
  /// In en, this message translates to:
  /// **'Clear all'**
  String get chatPickSkillsClear;

  /// No description provided for @chatPickSkillsDone.
  ///
  /// In en, this message translates to:
  /// **'Done ({count})'**
  String chatPickSkillsDone(Object count);

  /// No description provided for @chatComposerSkillsTooltip.
  ///
  /// In en, this message translates to:
  /// **'Mention skills (@)'**
  String get chatComposerSkillsTooltip;

  /// No description provided for @skillsFilterPending.
  ///
  /// In en, this message translates to:
  /// **'Pending'**
  String get skillsFilterPending;

  /// No description provided for @skillsApprove.
  ///
  /// In en, this message translates to:
  /// **'Approve'**
  String get skillsApprove;

  /// No description provided for @skillsReject.
  ///
  /// In en, this message translates to:
  /// **'Reject'**
  String get skillsReject;

  /// No description provided for @skillsRejectReason.
  ///
  /// In en, this message translates to:
  /// **'Reason (optional)'**
  String get skillsRejectReason;

  /// No description provided for @codeWorkbenchTitle.
  ///
  /// In en, this message translates to:
  /// **'Code Workbench'**
  String get codeWorkbenchTitle;

  /// No description provided for @codeNoTaskHint.
  ///
  /// In en, this message translates to:
  /// **'Pick a task on the left or start a new one.'**
  String get codeNoTaskHint;

  /// No description provided for @codeNewTask.
  ///
  /// In en, this message translates to:
  /// **'New task'**
  String get codeNewTask;

  /// No description provided for @codeTabAgent.
  ///
  /// In en, this message translates to:
  /// **'Agent'**
  String get codeTabAgent;

  /// No description provided for @codeTabTerminal.
  ///
  /// In en, this message translates to:
  /// **'Terminal'**
  String get codeTabTerminal;

  /// No description provided for @codeTabPr.
  ///
  /// In en, this message translates to:
  /// **'PR Preview'**
  String get codeTabPr;

  /// No description provided for @codeTabCompare.
  ///
  /// In en, this message translates to:
  /// **'Compare'**
  String get codeTabCompare;

  /// No description provided for @codeRightFiles.
  ///
  /// In en, this message translates to:
  /// **'Files'**
  String get codeRightFiles;

  /// No description provided for @codeRightDiff.
  ///
  /// In en, this message translates to:
  /// **'Diff'**
  String get codeRightDiff;

  /// No description provided for @codeRightGit.
  ///
  /// In en, this message translates to:
  /// **'Git'**
  String get codeRightGit;

  /// No description provided for @codeRightHooks.
  ///
  /// In en, this message translates to:
  /// **'Hooks'**
  String get codeRightHooks;

  /// No description provided for @codeStatusBarHint.
  ///
  /// In en, this message translates to:
  /// **'Coding workbench'**
  String get codeStatusBarHint;

  /// No description provided for @codeUsageTooltip.
  ///
  /// In en, this message translates to:
  /// **'Usage'**
  String get codeUsageTooltip;

  /// No description provided for @codeUsageLoading.
  ///
  /// In en, this message translates to:
  /// **'Loading usage…'**
  String get codeUsageLoading;

  /// No description provided for @codeUsageFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load usage'**
  String get codeUsageFailed;

  /// No description provided for @codeUsageNoWindows.
  ///
  /// In en, this message translates to:
  /// **'No usage data'**
  String get codeUsageNoWindows;

  /// No description provided for @codeUsageFiveHour.
  ///
  /// In en, this message translates to:
  /// **'5-hour'**
  String get codeUsageFiveHour;

  /// No description provided for @codeUsageSevenDay.
  ///
  /// In en, this message translates to:
  /// **'7-day'**
  String get codeUsageSevenDay;

  /// No description provided for @codeUsagePrimary.
  ///
  /// In en, this message translates to:
  /// **'Primary'**
  String get codeUsagePrimary;

  /// No description provided for @codeUsageSecondary.
  ///
  /// In en, this message translates to:
  /// **'Secondary'**
  String get codeUsageSecondary;

  /// No description provided for @codeUsageLeft.
  ///
  /// In en, this message translates to:
  /// **'left'**
  String get codeUsageLeft;

  /// No description provided for @codeUsageRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get codeUsageRefresh;

  /// No description provided for @memoryTitle.
  ///
  /// In en, this message translates to:
  /// **'Memory'**
  String get memoryTitle;

  /// No description provided for @memoryProjectMissing.
  ///
  /// In en, this message translates to:
  /// **'(no project)'**
  String get memoryProjectMissing;

  /// No description provided for @memoryHintNoCreds.
  ///
  /// In en, this message translates to:
  /// **'Configure model-relay URL + token in Settings.'**
  String get memoryHintNoCreds;

  /// No description provided for @memoryHintNoProject.
  ///
  /// In en, this message translates to:
  /// **'Open or create a project in the Wiki tab first.'**
  String get memoryHintNoProject;

  /// No description provided for @memoryHintEmptyList.
  ///
  /// In en, this message translates to:
  /// **'No memories yet — add one below.'**
  String get memoryHintEmptyList;

  /// No description provided for @memoryHintEmptyRecall.
  ///
  /// In en, this message translates to:
  /// **'No matches. Try another query.'**
  String get memoryHintEmptyRecall;

  /// No description provided for @memoryListTab.
  ///
  /// In en, this message translates to:
  /// **'List'**
  String get memoryListTab;

  /// No description provided for @memoryRecallTab.
  ///
  /// In en, this message translates to:
  /// **'Recall'**
  String get memoryRecallTab;

  /// No description provided for @memoryFilterAll.
  ///
  /// In en, this message translates to:
  /// **'All'**
  String get memoryFilterAll;

  /// No description provided for @memoryKindRecall.
  ///
  /// In en, this message translates to:
  /// **'recall'**
  String get memoryKindRecall;

  /// No description provided for @memoryKindPreference.
  ///
  /// In en, this message translates to:
  /// **'preference'**
  String get memoryKindPreference;

  /// No description provided for @memoryKindHabit.
  ///
  /// In en, this message translates to:
  /// **'habit'**
  String get memoryKindHabit;

  /// No description provided for @memoryRecallQueryHint.
  ///
  /// In en, this message translates to:
  /// **'recall query…'**
  String get memoryRecallQueryHint;

  /// No description provided for @memoryAddHint.
  ///
  /// In en, this message translates to:
  /// **'remember this…'**
  String get memoryAddHint;

  /// No description provided for @memoryAddButton.
  ///
  /// In en, this message translates to:
  /// **'Add'**
  String get memoryAddButton;

  /// No description provided for @memoryRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get memoryRefresh;

  /// No description provided for @memoryModeHybrid.
  ///
  /// In en, this message translates to:
  /// **'hybrid (semantic + lexical)'**
  String get memoryModeHybrid;

  /// No description provided for @memoryModeLexical.
  ///
  /// In en, this message translates to:
  /// **'lexical-only'**
  String get memoryModeLexical;

  /// No description provided for @memorySubtitle.
  ///
  /// In en, this message translates to:
  /// **'kind={kind} · salience={salience}{scoreSuffix} · {when}'**
  String memorySubtitle(
    Object kind,
    Object salience,
    Object scoreSuffix,
    Object when,
  );

  /// No description provided for @relTimeSeconds.
  ///
  /// In en, this message translates to:
  /// **'{seconds}s ago'**
  String relTimeSeconds(Object seconds);

  /// No description provided for @relTimeMinutes.
  ///
  /// In en, this message translates to:
  /// **'{minutes}m ago'**
  String relTimeMinutes(Object minutes);

  /// No description provided for @relTimeHours.
  ///
  /// In en, this message translates to:
  /// **'{hours}h ago'**
  String relTimeHours(Object hours);

  /// No description provided for @relTimeDays.
  ///
  /// In en, this message translates to:
  /// **'{days}d ago'**
  String relTimeDays(Object days);

  /// No description provided for @relTimeMonths.
  ///
  /// In en, this message translates to:
  /// **'{months}mo ago'**
  String relTimeMonths(Object months);

  /// No description provided for @settingsTitle.
  ///
  /// In en, this message translates to:
  /// **'Settings'**
  String get settingsTitle;

  /// No description provided for @settingsModeHub.
  ///
  /// In en, this message translates to:
  /// **'model-relay'**
  String get settingsModeHub;

  /// No description provided for @settingsModeBYOK.
  ///
  /// In en, this message translates to:
  /// **'BYOK (bring your own key)'**
  String get settingsModeBYOK;

  /// No description provided for @settingsModeOffline.
  ///
  /// In en, this message translates to:
  /// **'Offline'**
  String get settingsModeOffline;

  /// No description provided for @settingsHubUrl.
  ///
  /// In en, this message translates to:
  /// **'model-relay URL'**
  String get settingsHubUrl;

  /// No description provided for @settingsRelayToken.
  ///
  /// In en, this message translates to:
  /// **'model-relay token'**
  String get settingsRelayToken;

  /// No description provided for @settingsTestConnection.
  ///
  /// In en, this message translates to:
  /// **'Test connection'**
  String get settingsTestConnection;

  /// No description provided for @settingsSave.
  ///
  /// In en, this message translates to:
  /// **'Save'**
  String get settingsSave;

  /// No description provided for @settingsSavedSnack.
  ///
  /// In en, this message translates to:
  /// **'Settings saved'**
  String get settingsSavedSnack;

  /// No description provided for @signInTitle.
  ///
  /// In en, this message translates to:
  /// **'Sign in'**
  String get signInTitle;

  /// No description provided for @signInSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Sign in to your BiuMind account.'**
  String get signInSubtitle;

  /// No description provided for @signInIdentityUrl.
  ///
  /// In en, this message translates to:
  /// **'Server URL'**
  String get signInIdentityUrl;

  /// No description provided for @signInAdvanced.
  ///
  /// In en, this message translates to:
  /// **'Advanced'**
  String get signInAdvanced;

  /// No description provided for @signInVerifyCode.
  ///
  /// In en, this message translates to:
  /// **'6-digit code'**
  String get signInVerifyCode;

  /// No description provided for @signInEmail.
  ///
  /// In en, this message translates to:
  /// **'Email'**
  String get signInEmail;

  /// No description provided for @signInPassword.
  ///
  /// In en, this message translates to:
  /// **'Password'**
  String get signInPassword;

  /// No description provided for @signInSubmit.
  ///
  /// In en, this message translates to:
  /// **'Sign in'**
  String get signInSubmit;

  /// No description provided for @signInRegister.
  ///
  /// In en, this message translates to:
  /// **'Register'**
  String get signInRegister;

  /// No description provided for @signInSignOut.
  ///
  /// In en, this message translates to:
  /// **'Sign out ({email})'**
  String signInSignOut(Object email);

  /// No description provided for @signInOk.
  ///
  /// In en, this message translates to:
  /// **'✓ Signed in as {email}'**
  String signInOk(Object email);

  /// No description provided for @registerOk.
  ///
  /// In en, this message translates to:
  /// **'✓ Registered as {email}'**
  String registerOk(Object email);

  /// No description provided for @signInTokenExpires.
  ///
  /// In en, this message translates to:
  /// **'Token expires: {when}'**
  String signInTokenExpires(Object when);

  /// No description provided for @signInErrInvalidCredentials.
  ///
  /// In en, this message translates to:
  /// **'Email or password is incorrect.'**
  String get signInErrInvalidCredentials;

  /// No description provided for @signInErrEmailTaken.
  ///
  /// In en, this message translates to:
  /// **'This email is already registered.'**
  String get signInErrEmailTaken;

  /// No description provided for @signInErrInvalidEmail.
  ///
  /// In en, this message translates to:
  /// **'Email format is invalid.'**
  String get signInErrInvalidEmail;

  /// No description provided for @signInErrPasswordTooShort.
  ///
  /// In en, this message translates to:
  /// **'Password is too short — use at least 8 characters.'**
  String get signInErrPasswordTooShort;

  /// No description provided for @signInErrNetwork.
  ///
  /// In en, this message translates to:
  /// **'Cannot reach the server. Check your network.'**
  String get signInErrNetwork;

  /// No description provided for @signInErrUnknown.
  ///
  /// In en, this message translates to:
  /// **'Sign-in failed. Try again.'**
  String get signInErrUnknown;

  /// No description provided for @signInCodeSent.
  ///
  /// In en, this message translates to:
  /// **'Code sent to {email} — check your inbox (valid for 10 minutes)'**
  String signInCodeSent(String email);

  /// No description provided for @signInCodeDevMode.
  ///
  /// In en, this message translates to:
  /// **'Code generated. Dev mode: SMTP not configured — ask an admin to read the code from server logs'**
  String get signInCodeDevMode;

  /// No description provided for @signInEnterCodeSentTo.
  ///
  /// In en, this message translates to:
  /// **'Enter the 6-digit code sent to {email}'**
  String signInEnterCodeSentTo(String email);

  /// No description provided for @signInEnterCode.
  ///
  /// In en, this message translates to:
  /// **'Enter the 6-digit code'**
  String get signInEnterCode;

  /// No description provided for @signInCodeResent.
  ///
  /// In en, this message translates to:
  /// **'Code re-sent to {email}'**
  String signInCodeResent(String email);

  /// No description provided for @signInCodeRegenerated.
  ///
  /// In en, this message translates to:
  /// **'New code generated. Dev mode — read it from server logs'**
  String get signInCodeRegenerated;

  /// No description provided for @signInForgotHint.
  ///
  /// In en, this message translates to:
  /// **'Enter your account email and we\'ll send a 6-digit reset code'**
  String get signInForgotHint;

  /// No description provided for @signInErrEmailRequired.
  ///
  /// In en, this message translates to:
  /// **'Enter your account email'**
  String get signInErrEmailRequired;

  /// No description provided for @signInResetCodeSent.
  ///
  /// In en, this message translates to:
  /// **'If this email is registered, a code was sent to {email} (valid for 10 minutes)'**
  String signInResetCodeSent(String email);

  /// No description provided for @signInResetCodeDevMode.
  ///
  /// In en, this message translates to:
  /// **'Code generated. Dev mode — ask an admin to read it from server logs'**
  String get signInResetCodeDevMode;

  /// No description provided for @signInErrNewPasswordShort.
  ///
  /// In en, this message translates to:
  /// **'New password must be at least 8 characters'**
  String get signInErrNewPasswordShort;

  /// No description provided for @signInPasswordReset.
  ///
  /// In en, this message translates to:
  /// **'Password reset — sign in with your new password'**
  String get signInPasswordReset;

  /// No description provided for @signInNewPasswordLabel.
  ///
  /// In en, this message translates to:
  /// **'New password (≥ 8 chars)'**
  String get signInNewPasswordLabel;

  /// No description provided for @signInForgotPassword.
  ///
  /// In en, this message translates to:
  /// **'Forgot password?'**
  String get signInForgotPassword;

  /// No description provided for @signInNoAccount.
  ///
  /// In en, this message translates to:
  /// **'No account?'**
  String get signInNoAccount;

  /// No description provided for @signInSendResetCode.
  ///
  /// In en, this message translates to:
  /// **'Send reset code'**
  String get signInSendResetCode;

  /// No description provided for @signInBackToSignIn.
  ///
  /// In en, this message translates to:
  /// **'Back to sign in'**
  String get signInBackToSignIn;

  /// No description provided for @signInResetPassword.
  ///
  /// In en, this message translates to:
  /// **'Reset password'**
  String get signInResetPassword;

  /// No description provided for @signInResendCooldown.
  ///
  /// In en, this message translates to:
  /// **'Resend ({seconds}s)'**
  String signInResendCooldown(int seconds);

  /// No description provided for @signInResendCode.
  ///
  /// In en, this message translates to:
  /// **'Resend code'**
  String get signInResendCode;

  /// No description provided for @signInSubtitleVerify.
  ///
  /// In en, this message translates to:
  /// **'Verify your email to activate your account'**
  String get signInSubtitleVerify;

  /// No description provided for @signInSubtitleForgot.
  ///
  /// In en, this message translates to:
  /// **'Forgot password — we\'ll email you a reset code'**
  String get signInSubtitleForgot;

  /// No description provided for @signInSubtitleReset.
  ///
  /// In en, this message translates to:
  /// **'Enter the code and a new password to finish the reset'**
  String get signInSubtitleReset;

  /// No description provided for @signInVerifySubmit.
  ///
  /// In en, this message translates to:
  /// **'Verify & sign in'**
  String get signInVerifySubmit;

  /// No description provided for @signInErrInvalidCode.
  ///
  /// In en, this message translates to:
  /// **'Incorrect code — try again'**
  String get signInErrInvalidCode;

  /// No description provided for @signInErrCodeExpired.
  ///
  /// In en, this message translates to:
  /// **'Code expired — tap resend'**
  String get signInErrCodeExpired;

  /// No description provided for @signInErrCodeLocked.
  ///
  /// In en, this message translates to:
  /// **'Too many attempts — request a new code'**
  String get signInErrCodeLocked;

  /// No description provided for @signInErrCodeUsed.
  ///
  /// In en, this message translates to:
  /// **'Code already used — request a new one'**
  String get signInErrCodeUsed;

  /// No description provided for @signInErrNoPendingCode.
  ///
  /// In en, this message translates to:
  /// **'No code sent yet — tap resend first'**
  String get signInErrNoPendingCode;

  /// No description provided for @signInErrRateLimited.
  ///
  /// In en, this message translates to:
  /// **'Too many requests — try again later'**
  String get signInErrRateLimited;

  /// No description provided for @settingsModeSection.
  ///
  /// In en, this message translates to:
  /// **'Mode'**
  String get settingsModeSection;

  /// No description provided for @settingsModeSectionSubtitle.
  ///
  /// In en, this message translates to:
  /// **'How AI calls are routed.'**
  String get settingsModeSectionSubtitle;

  /// No description provided for @settingsHubSection.
  ///
  /// In en, this message translates to:
  /// **'model-relay connection'**
  String get settingsHubSection;

  /// No description provided for @settingsHubSectionSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Used in cloud / byo_endpoint modes. Bearer token is auto-filled '**
  String get settingsHubSectionSubtitle;

  /// No description provided for @settingsBYOKSection.
  ///
  /// In en, this message translates to:
  /// **'BYOK provider keys'**
  String get settingsBYOKSection;

  /// No description provided for @settingsBYOKSectionSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Stored encrypted in OS keychain. Used in direct mode and as '**
  String get settingsBYOKSectionSubtitle;

  /// No description provided for @settingsAccountSection.
  ///
  /// In en, this message translates to:
  /// **'Account'**
  String get settingsAccountSection;

  /// No description provided for @settingsAccountSectionSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Sign in to sync your chats across devices.'**
  String get settingsAccountSectionSubtitle;

  /// No description provided for @settingsServiceUrl.
  ///
  /// In en, this message translates to:
  /// **'Service URL'**
  String get settingsServiceUrl;

  /// No description provided for @settingsProvidersSection.
  ///
  /// In en, this message translates to:
  /// **'Model providers'**
  String get settingsProvidersSection;

  /// No description provided for @settingsProvidersSectionSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Configure which model providers you can chat with. Server stores keys encrypted.'**
  String get settingsProvidersSectionSubtitle;

  /// No description provided for @settingsChatDefaultsSection.
  ///
  /// In en, this message translates to:
  /// **'Chat defaults'**
  String get settingsChatDefaultsSection;

  /// No description provided for @settingsChatDefaultsSectionSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Default model for new conversations.'**
  String get settingsChatDefaultsSectionSubtitle;

  /// No description provided for @settingsDefaultModel.
  ///
  /// In en, this message translates to:
  /// **'Default model'**
  String get settingsDefaultModel;

  /// No description provided for @settingsAddProvider.
  ///
  /// In en, this message translates to:
  /// **'Add provider'**
  String get settingsAddProvider;

  /// No description provided for @settingsAddCustomProvider.
  ///
  /// In en, this message translates to:
  /// **'Add custom provider'**
  String get settingsAddCustomProvider;

  /// No description provided for @settingsCustomProviderId.
  ///
  /// In en, this message translates to:
  /// **'Provider ID (slug)'**
  String get settingsCustomProviderId;

  /// No description provided for @settingsCustomProviderName.
  ///
  /// In en, this message translates to:
  /// **'Display name'**
  String get settingsCustomProviderName;

  /// No description provided for @settingsConfigure.
  ///
  /// In en, this message translates to:
  /// **'Configure'**
  String get settingsConfigure;

  /// No description provided for @settingsRemove.
  ///
  /// In en, this message translates to:
  /// **'Remove'**
  String get settingsRemove;

  /// No description provided for @settingsApiKey.
  ///
  /// In en, this message translates to:
  /// **'API key'**
  String get settingsApiKey;

  /// No description provided for @settingsBaseUrlOptional.
  ///
  /// In en, this message translates to:
  /// **'Base URL (optional)'**
  String get settingsBaseUrlOptional;

  /// No description provided for @settingsFetchModeLabel.
  ///
  /// In en, this message translates to:
  /// **'Call routing'**
  String get settingsFetchModeLabel;

  /// No description provided for @settingsFetchModeServer.
  ///
  /// In en, this message translates to:
  /// **'Server-side relay'**
  String get settingsFetchModeServer;

  /// No description provided for @settingsFetchModeServerDesc.
  ///
  /// In en, this message translates to:
  /// **'Brain proxies the call. Lowest client work, easier debugging.'**
  String get settingsFetchModeServerDesc;

  /// No description provided for @settingsFetchModeClient.
  ///
  /// In en, this message translates to:
  /// **'Client-side direct'**
  String get settingsFetchModeClient;

  /// No description provided for @settingsFetchModeClientDesc.
  ///
  /// In en, this message translates to:
  /// **'Your device opens the LLM stream itself. Key only leaves the server when this device asks.'**
  String get settingsFetchModeClientDesc;

  /// No description provided for @settingsProviderConfigured.
  ///
  /// In en, this message translates to:
  /// **'Configured'**
  String get settingsProviderConfigured;

  /// No description provided for @settingsProviderNotConfigured.
  ///
  /// In en, this message translates to:
  /// **'Not configured'**
  String get settingsProviderNotConfigured;

  /// No description provided for @settingsGroupGeneral.
  ///
  /// In en, this message translates to:
  /// **'GENERAL'**
  String get settingsGroupGeneral;

  /// No description provided for @settingsGroupAgent.
  ///
  /// In en, this message translates to:
  /// **'AGENT'**
  String get settingsGroupAgent;

  /// No description provided for @settingsGroupSystem.
  ///
  /// In en, this message translates to:
  /// **'SYSTEM'**
  String get settingsGroupSystem;

  /// No description provided for @settingsNavStatistics.
  ///
  /// In en, this message translates to:
  /// **'Statistics'**
  String get settingsNavStatistics;

  /// No description provided for @settingsNavAppearance.
  ///
  /// In en, this message translates to:
  /// **'Appearance'**
  String get settingsNavAppearance;

  /// No description provided for @settingsNavShortcuts.
  ///
  /// In en, this message translates to:
  /// **'Shortcuts'**
  String get settingsNavShortcuts;

  /// No description provided for @settingsNavProviders.
  ///
  /// In en, this message translates to:
  /// **'AI Providers'**
  String get settingsNavProviders;

  /// No description provided for @settingsNavDefaultModel.
  ///
  /// In en, this message translates to:
  /// **'Default Model'**
  String get settingsNavDefaultModel;

  /// No description provided for @settingsNavSkills.
  ///
  /// In en, this message translates to:
  /// **'Skills'**
  String get settingsNavSkills;

  /// No description provided for @settingsNavMemory.
  ///
  /// In en, this message translates to:
  /// **'Memory'**
  String get settingsNavMemory;

  /// No description provided for @settingsNavCredentials.
  ///
  /// In en, this message translates to:
  /// **'Credentials'**
  String get settingsNavCredentials;

  /// No description provided for @settingsNavCodingWorkbench.
  ///
  /// In en, this message translates to:
  /// **'Coding Workbench'**
  String get settingsNavCodingWorkbench;

  /// No description provided for @settingsNavChat.
  ///
  /// In en, this message translates to:
  /// **'Chat'**
  String get settingsNavChat;

  /// No description provided for @settingsNavProxy.
  ///
  /// In en, this message translates to:
  /// **'Proxy'**
  String get settingsNavProxy;

  /// No description provided for @settingsNavStorage.
  ///
  /// In en, this message translates to:
  /// **'Storage'**
  String get settingsNavStorage;

  /// No description provided for @settingsNavApiKey.
  ///
  /// In en, this message translates to:
  /// **'API Key'**
  String get settingsNavApiKey;

  /// No description provided for @settingsNavAdvanced.
  ///
  /// In en, this message translates to:
  /// **'Advanced'**
  String get settingsNavAdvanced;

  /// No description provided for @settingsNavAbout.
  ///
  /// In en, this message translates to:
  /// **'About'**
  String get settingsNavAbout;

  /// No description provided for @settingsProviderSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search providers…'**
  String get settingsProviderSearchHint;

  /// No description provided for @settingsProvidersAll.
  ///
  /// In en, this message translates to:
  /// **'All'**
  String get settingsProvidersAll;

  /// No description provided for @settingsProvidersEnabled.
  ///
  /// In en, this message translates to:
  /// **'Enabled'**
  String get settingsProvidersEnabled;

  /// No description provided for @settingsProvidersDisabled.
  ///
  /// In en, this message translates to:
  /// **'Disabled'**
  String get settingsProvidersDisabled;

  /// No description provided for @settingsConnectivityCheck.
  ///
  /// In en, this message translates to:
  /// **'Connectivity check'**
  String get settingsConnectivityCheck;

  /// No description provided for @settingsCheckButton.
  ///
  /// In en, this message translates to:
  /// **'Check'**
  String get settingsCheckButton;

  /// No description provided for @settingsCheckOk.
  ///
  /// In en, this message translates to:
  /// **'Connection OK'**
  String get settingsCheckOk;

  /// No description provided for @settingsCheckSaveFirst.
  ///
  /// In en, this message translates to:
  /// **'Save the API key first, then check.'**
  String get settingsCheckSaveFirst;

  /// No description provided for @settingsConfigureFirstHint.
  ///
  /// In en, this message translates to:
  /// **'This provider isn\'t configured yet. Add an API key below and click Save.'**
  String get settingsConfigureFirstHint;

  /// No description provided for @settingsEncryptionNote.
  ///
  /// In en, this message translates to:
  /// **'Your API key + base URL are encrypted at rest with AES-256-GCM.'**
  String get settingsEncryptionNote;

  /// No description provided for @settingsModelList.
  ///
  /// In en, this message translates to:
  /// **'Models'**
  String get settingsModelList;

  /// No description provided for @settingsModelRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get settingsModelRefresh;

  /// No description provided for @settingsModelEmpty.
  ///
  /// In en, this message translates to:
  /// **'No models. Configure a key first, then refresh.'**
  String get settingsModelEmpty;

  /// No description provided for @settingsModelTabAll.
  ///
  /// In en, this message translates to:
  /// **'All'**
  String get settingsModelTabAll;

  /// No description provided for @settingsModelTabChat.
  ///
  /// In en, this message translates to:
  /// **'Chat'**
  String get settingsModelTabChat;

  /// No description provided for @settingsModelTabImage.
  ///
  /// In en, this message translates to:
  /// **'Image'**
  String get settingsModelTabImage;

  /// No description provided for @settingsModelTabVideo.
  ///
  /// In en, this message translates to:
  /// **'Video'**
  String get settingsModelTabVideo;

  /// No description provided for @settingsOfficialBadge.
  ///
  /// In en, this message translates to:
  /// **'BiuMind Official'**
  String get settingsOfficialBadge;

  /// No description provided for @settingsOfficialDescription.
  ///
  /// In en, this message translates to:
  /// **'Use BiuMind\'s curated platform models. No API key needed — sign in and chat. Calls route through our pool with usage-based or subscription billing.'**
  String get settingsOfficialDescription;

  /// No description provided for @settingsOfficialBilling.
  ///
  /// In en, this message translates to:
  /// **'Billing: pay-as-you-go (per-token) or monthly subscription. Manage in account.'**
  String get settingsOfficialBilling;

  /// No description provided for @themeSystem.
  ///
  /// In en, this message translates to:
  /// **'System'**
  String get themeSystem;

  /// No description provided for @themeLight.
  ///
  /// In en, this message translates to:
  /// **'Light'**
  String get themeLight;

  /// No description provided for @themeDark.
  ///
  /// In en, this message translates to:
  /// **'Dark'**
  String get themeDark;

  /// No description provided for @settingsAppearanceColorTheme.
  ///
  /// In en, this message translates to:
  /// **'Color theme'**
  String get settingsAppearanceColorTheme;

  /// No description provided for @settingsAppearanceColorThemeRecommended.
  ///
  /// In en, this message translates to:
  /// **'Recommended'**
  String get settingsAppearanceColorThemeRecommended;

  /// No description provided for @settingsAppearanceFontSize.
  ///
  /// In en, this message translates to:
  /// **'Font size'**
  String get settingsAppearanceFontSize;

  /// No description provided for @settingsAppearanceFontSizeSmall.
  ///
  /// In en, this message translates to:
  /// **'Small'**
  String get settingsAppearanceFontSizeSmall;

  /// No description provided for @settingsAppearanceFontSizeMedium.
  ///
  /// In en, this message translates to:
  /// **'Medium'**
  String get settingsAppearanceFontSizeMedium;

  /// No description provided for @settingsAppearanceFontSizeLarge.
  ///
  /// In en, this message translates to:
  /// **'Large'**
  String get settingsAppearanceFontSizeLarge;

  /// No description provided for @settingsAppearanceFontSizeHint.
  ///
  /// In en, this message translates to:
  /// **'Font size also drives spacing and list density.'**
  String get settingsAppearanceFontSizeHint;

  /// No description provided for @settingsAppearancePreviewTitle.
  ///
  /// In en, this message translates to:
  /// **'Hello, BiuMind'**
  String get settingsAppearancePreviewTitle;

  /// No description provided for @settingsAppearancePreviewBody.
  ///
  /// In en, this message translates to:
  /// **'Live preview — pick the size that feels right.'**
  String get settingsAppearancePreviewBody;

  /// No description provided for @settingsAppearanceMode.
  ///
  /// In en, this message translates to:
  /// **'Mode'**
  String get settingsAppearanceMode;

  /// No description provided for @paletteNamePurpleOrange.
  ///
  /// In en, this message translates to:
  /// **'Purple + Orange'**
  String get paletteNamePurpleOrange;

  /// No description provided for @paletteDescPurpleOrange.
  ///
  /// In en, this message translates to:
  /// **'Default — wisdom purple + action orange'**
  String get paletteDescPurpleOrange;

  /// No description provided for @paletteNamePurple.
  ///
  /// In en, this message translates to:
  /// **'Purple'**
  String get paletteNamePurple;

  /// No description provided for @paletteDescPurple.
  ///
  /// In en, this message translates to:
  /// **'Original brand purple'**
  String get paletteDescPurple;

  /// No description provided for @paletteNamePurpleBlue.
  ///
  /// In en, this message translates to:
  /// **'Purple + Blue'**
  String get paletteNamePurpleBlue;

  /// No description provided for @paletteDescPurpleBlue.
  ///
  /// In en, this message translates to:
  /// **'Rational, technical'**
  String get paletteDescPurpleBlue;

  /// No description provided for @paletteNamePurplePink.
  ///
  /// In en, this message translates to:
  /// **'Purple + Pink'**
  String get paletteNamePurplePink;

  /// No description provided for @paletteDescPurplePink.
  ///
  /// In en, this message translates to:
  /// **'Warm, expressive'**
  String get paletteDescPurplePink;

  /// No description provided for @paletteNamePurpleEmerald.
  ///
  /// In en, this message translates to:
  /// **'Purple + Emerald'**
  String get paletteNamePurpleEmerald;

  /// No description provided for @paletteDescPurpleEmerald.
  ///
  /// In en, this message translates to:
  /// **'Balanced, rare combo'**
  String get paletteDescPurpleEmerald;

  /// No description provided for @paletteNameAurora.
  ///
  /// In en, this message translates to:
  /// **'Aurora'**
  String get paletteNameAurora;

  /// No description provided for @paletteDescAurora.
  ///
  /// In en, this message translates to:
  /// **'Cyan-purple-pink mesh, max memorability'**
  String get paletteDescAurora;

  /// No description provided for @paletteNameSunset.
  ///
  /// In en, this message translates to:
  /// **'Sunset'**
  String get paletteNameSunset;

  /// No description provided for @paletteDescSunset.
  ///
  /// In en, this message translates to:
  /// **'Orange-pink-purple, warm energy'**
  String get paletteDescSunset;

  /// No description provided for @paletteNameCyber.
  ///
  /// In en, this message translates to:
  /// **'Cyber'**
  String get paletteNameCyber;

  /// No description provided for @paletteDescCyber.
  ///
  /// In en, this message translates to:
  /// **'Cyan-purple-magenta, futuristic'**
  String get paletteDescCyber;

  /// No description provided for @paletteNameOcean.
  ///
  /// In en, this message translates to:
  /// **'Ocean'**
  String get paletteNameOcean;

  /// No description provided for @paletteDescOcean.
  ///
  /// In en, this message translates to:
  /// **'Blue-cyan, calm B2B'**
  String get paletteDescOcean;

  /// No description provided for @paletteNameEmeraldGold.
  ///
  /// In en, this message translates to:
  /// **'Emerald Gold'**
  String get paletteNameEmeraldGold;

  /// No description provided for @paletteDescEmeraldGold.
  ///
  /// In en, this message translates to:
  /// **'Premium, financial'**
  String get paletteDescEmeraldGold;

  /// No description provided for @paletteNameRose.
  ///
  /// In en, this message translates to:
  /// **'Rose'**
  String get paletteNameRose;

  /// No description provided for @paletteDescRose.
  ///
  /// In en, this message translates to:
  /// **'Emotive, vibrant'**
  String get paletteDescRose;

  /// No description provided for @paletteNameOnyx.
  ///
  /// In en, this message translates to:
  /// **'Onyx'**
  String get paletteNameOnyx;

  /// No description provided for @paletteDescOnyx.
  ///
  /// In en, this message translates to:
  /// **'Minimalist, professional'**
  String get paletteDescOnyx;

  /// No description provided for @paletteNameInkblueOrange.
  ///
  /// In en, this message translates to:
  /// **'Inkblue + Signal Orange'**
  String get paletteNameInkblueOrange;

  /// No description provided for @paletteDescInkblueOrange.
  ///
  /// In en, this message translates to:
  /// **'Vercel-style minimalism'**
  String get paletteDescInkblueOrange;

  /// No description provided for @paletteNameQuantumTitanium.
  ///
  /// In en, this message translates to:
  /// **'Quantum + Titanium'**
  String get paletteNameQuantumTitanium;

  /// No description provided for @paletteDescQuantumTitanium.
  ///
  /// In en, this message translates to:
  /// **'Vision Pro feel, premium'**
  String get paletteDescQuantumTitanium;

  /// No description provided for @paletteNameClaudeWarm.
  ///
  /// In en, this message translates to:
  /// **'Claude Warm'**
  String get paletteNameClaudeWarm;

  /// No description provided for @paletteDescClaudeWarm.
  ///
  /// In en, this message translates to:
  /// **'Counter-trend warmth'**
  String get paletteDescClaudeWarm;

  /// No description provided for @paletteNameGraphiteCyan.
  ///
  /// In en, this message translates to:
  /// **'Graphite + Cyan'**
  String get paletteNameGraphiteCyan;

  /// No description provided for @paletteDescGraphiteCyan.
  ///
  /// In en, this message translates to:
  /// **'Cursor-style engineering'**
  String get paletteDescGraphiteCyan;

  /// No description provided for @paletteNameIndigoSand.
  ///
  /// In en, this message translates to:
  /// **'Indigo + Sand'**
  String get paletteNameIndigoSand;

  /// No description provided for @paletteDescIndigoSand.
  ///
  /// In en, this message translates to:
  /// **'2026 trend, balanced'**
  String get paletteDescIndigoSand;

  /// No description provided for @paletteNameWikiGreen.
  ///
  /// In en, this message translates to:
  /// **'Wiki Emerald'**
  String get paletteNameWikiGreen;

  /// No description provided for @paletteDescWikiGreen.
  ///
  /// In en, this message translates to:
  /// **'Calm green for note-taking'**
  String get paletteDescWikiGreen;

  /// No description provided for @aboutSubtitle.
  ///
  /// In en, this message translates to:
  /// **'About BiuMind'**
  String get aboutSubtitle;

  /// No description provided for @aboutBuild.
  ///
  /// In en, this message translates to:
  /// **'Build'**
  String get aboutBuild;

  /// No description provided for @aboutTagline.
  ///
  /// In en, this message translates to:
  /// **'Your AI workspace — chats, knowledge, and memory in one place.'**
  String get aboutTagline;

  /// No description provided for @settingsCheckUpdate.
  ///
  /// In en, this message translates to:
  /// **'Check for updates'**
  String get settingsCheckUpdate;

  /// No description provided for @settingsCheckUpdateLatest.
  ///
  /// In en, this message translates to:
  /// **'Up to date'**
  String get settingsCheckUpdateLatest;

  /// No description provided for @settingsCheckUpdateChecking.
  ///
  /// In en, this message translates to:
  /// **'Checking…'**
  String get settingsCheckUpdateChecking;

  /// No description provided for @settingsCheckUpdateAvailable.
  ///
  /// In en, this message translates to:
  /// **'Update available'**
  String get settingsCheckUpdateAvailable;

  /// No description provided for @settingsFetchNightly.
  ///
  /// In en, this message translates to:
  /// **'Get nightly builds'**
  String get settingsFetchNightly;

  /// No description provided for @settingsFetchNightlySubtitle.
  ///
  /// In en, this message translates to:
  /// **'Unsigned · may be unstable · back up your data first'**
  String get settingsFetchNightlySubtitle;

  /// No description provided for @settingsAppearanceSection.
  ///
  /// In en, this message translates to:
  /// **'Appearance'**
  String get settingsAppearanceSection;

  /// No description provided for @settingsAppearanceSectionSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Theme is currently locked to system; theme switcher in P3.6.'**
  String get settingsAppearanceSectionSubtitle;

  /// No description provided for @settingsBearerToken.
  ///
  /// In en, this message translates to:
  /// **'Bearer token'**
  String get settingsBearerToken;

  /// No description provided for @settingsBearerTokenHint.
  ///
  /// In en, this message translates to:
  /// **'JWT or virtual key (bk-live-…)'**
  String get settingsBearerTokenHint;

  /// No description provided for @settingsAnthropicKey.
  ///
  /// In en, this message translates to:
  /// **'Anthropic API key'**
  String get settingsAnthropicKey;

  /// No description provided for @settingsOpenAIKey.
  ///
  /// In en, this message translates to:
  /// **'OpenAI API key'**
  String get settingsOpenAIKey;

  /// No description provided for @settingsTheme.
  ///
  /// In en, this message translates to:
  /// **'Theme'**
  String get settingsTheme;

  /// No description provided for @settingsModeCloudTitle.
  ///
  /// In en, this message translates to:
  /// **'Cloud (default)'**
  String get settingsModeCloudTitle;

  /// No description provided for @settingsModeCloudDesc.
  ///
  /// In en, this message translates to:
  /// **'All AI calls go through your BiuMind model-relay. Platform billing + audit.'**
  String get settingsModeCloudDesc;

  /// No description provided for @settingsModeBYOEndpointTitle.
  ///
  /// In en, this message translates to:
  /// **'BYO Endpoint'**
  String get settingsModeBYOEndpointTitle;

  /// No description provided for @settingsModeBYOEndpointDesc.
  ///
  /// In en, this message translates to:
  /// **'Through model-relay, but model-relay forwards to your private endpoint '**
  String get settingsModeBYOEndpointDesc;

  /// No description provided for @settingsModeDirectTitle.
  ///
  /// In en, this message translates to:
  /// **'Direct (standalone)'**
  String get settingsModeDirectTitle;

  /// No description provided for @settingsModeDirectDesc.
  ///
  /// In en, this message translates to:
  /// **'Bypass model-relay. AI calls go directly from your device to the LLM '**
  String get settingsModeDirectDesc;

  /// No description provided for @chatTitle.
  ///
  /// In en, this message translates to:
  /// **'Chat'**
  String get chatTitle;

  /// No description provided for @chatHint.
  ///
  /// In en, this message translates to:
  /// **'Type a message…'**
  String get chatHint;

  /// No description provided for @chatSend.
  ///
  /// In en, this message translates to:
  /// **'Send'**
  String get chatSend;

  /// No description provided for @chatNewThread.
  ///
  /// In en, this message translates to:
  /// **'New chat'**
  String get chatNewThread;

  /// No description provided for @chatUntitled.
  ///
  /// In en, this message translates to:
  /// **'(untitled)'**
  String get chatUntitled;

  /// No description provided for @chatErrNotSignedIn.
  ///
  /// In en, this message translates to:
  /// **'Not signed in. Open Settings to connect.'**
  String get chatErrNotSignedIn;

  /// No description provided for @chatErrSettingsLoading.
  ///
  /// In en, this message translates to:
  /// **'Settings still loading — try again in a moment.'**
  String get chatErrSettingsLoading;

  /// No description provided for @chatErrDirectUnsupported.
  ///
  /// In en, this message translates to:
  /// **'Direct mode is not supported yet.'**
  String get chatErrDirectUnsupported;

  /// No description provided for @chatErrDirectNoKey.
  ///
  /// In en, this message translates to:
  /// **'Anthropic API key not configured. Set it in Settings.'**
  String get chatErrDirectNoKey;

  /// No description provided for @chatErrNetwork.
  ///
  /// In en, this message translates to:
  /// **'Network error. Check your connection.'**
  String get chatErrNetwork;

  /// No description provided for @chatErrAuth.
  ///
  /// In en, this message translates to:
  /// **'Session expired. Sign in again.'**
  String get chatErrAuth;

  /// No description provided for @chatV2SettingsTitle.
  ///
  /// In en, this message translates to:
  /// **'Chat preferences'**
  String get chatV2SettingsTitle;

  /// No description provided for @chatV2SettingsResetAll.
  ///
  /// In en, this message translates to:
  /// **'Reset'**
  String get chatV2SettingsResetAll;

  /// No description provided for @chatV2SettingsResetConfirmTitle.
  ///
  /// In en, this message translates to:
  /// **'Reset preferences'**
  String get chatV2SettingsResetConfirmTitle;

  /// No description provided for @chatV2SettingsResetConfirmBody.
  ///
  /// In en, this message translates to:
  /// **'Font size / default mode / default model / language will all be reset to factory defaults. This is irreversible.'**
  String get chatV2SettingsResetConfirmBody;

  /// No description provided for @chatV2SettingsResetButton.
  ///
  /// In en, this message translates to:
  /// **'Reset'**
  String get chatV2SettingsResetButton;

  /// No description provided for @chatV2SettingsCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get chatV2SettingsCancel;

  /// No description provided for @chatV2SettingsClose.
  ///
  /// In en, this message translates to:
  /// **'Close'**
  String get chatV2SettingsClose;

  /// No description provided for @chatV2SettingsFontScale.
  ///
  /// In en, this message translates to:
  /// **'Font size'**
  String get chatV2SettingsFontScale;

  /// No description provided for @chatV2SettingsFontScaleHint.
  ///
  /// In en, this message translates to:
  /// **'Scales text inside message bubbles'**
  String get chatV2SettingsFontScaleHint;

  /// No description provided for @chatV2SettingsDefaultMode.
  ///
  /// In en, this message translates to:
  /// **'Default conversation mode'**
  String get chatV2SettingsDefaultMode;

  /// No description provided for @chatV2SettingsAutoRenameTitle.
  ///
  /// In en, this message translates to:
  /// **'Auto-title from first prompt'**
  String get chatV2SettingsAutoRenameTitle;

  /// No description provided for @chatV2SettingsAutoRenameSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Off keeps \"New chat\" as placeholder until you rename manually'**
  String get chatV2SettingsAutoRenameSubtitle;

  /// No description provided for @chatV2SettingsDefaultModel.
  ///
  /// In en, this message translates to:
  /// **'Default model (chat mode)'**
  String get chatV2SettingsDefaultModel;

  /// No description provided for @chatV2SettingsDefaultModelDefault.
  ///
  /// In en, this message translates to:
  /// **'BiuMind default (unspecified)'**
  String get chatV2SettingsDefaultModelDefault;

  /// No description provided for @chatV2SettingsTtsTitle.
  ///
  /// In en, this message translates to:
  /// **'Read aloud (text-to-speech)'**
  String get chatV2SettingsTtsTitle;

  /// No description provided for @chatV2SettingsTtsHint.
  ///
  /// In en, this message translates to:
  /// **'Pick a cloud voice model for high-quality narration via model-relay. '**
  String get chatV2SettingsTtsHint;

  /// No description provided for @chatV2SettingsTtsModel.
  ///
  /// In en, this message translates to:
  /// **'Voice model'**
  String get chatV2SettingsTtsModel;

  /// No description provided for @chatV2SettingsTtsModelLocal.
  ///
  /// In en, this message translates to:
  /// **'Device voice (offline, free)'**
  String get chatV2SettingsTtsModelLocal;

  /// No description provided for @chatV2SettingsTtsVoice.
  ///
  /// In en, this message translates to:
  /// **'Voice ID'**
  String get chatV2SettingsTtsVoice;

  /// No description provided for @chatV2SettingsTtsVoiceHint.
  ///
  /// In en, this message translates to:
  /// **'e.g. longanyang (cosyvoice system voice)'**
  String get chatV2SettingsTtsVoiceHint;

  /// No description provided for @chatV2SettingsTtsNoModels.
  ///
  /// In en, this message translates to:
  /// **'No voice models configured. Add an audio_speech model in a channel first.'**
  String get chatV2SettingsTtsNoModels;

  /// No description provided for @chatV2SettingsLanguage.
  ///
  /// In en, this message translates to:
  /// **'Language'**
  String get chatV2SettingsLanguage;

  /// No description provided for @chatV2SettingsLanguageSystem.
  ///
  /// In en, this message translates to:
  /// **'System'**
  String get chatV2SettingsLanguageSystem;

  /// No description provided for @chatV2SettingsLanguageZh.
  ///
  /// In en, this message translates to:
  /// **'中文'**
  String get chatV2SettingsLanguageZh;

  /// No description provided for @chatV2SettingsLanguageEn.
  ///
  /// In en, this message translates to:
  /// **'English'**
  String get chatV2SettingsLanguageEn;

  /// No description provided for @chatV2AppBarPinTooltip.
  ///
  /// In en, this message translates to:
  /// **'Pin'**
  String get chatV2AppBarPinTooltip;

  /// No description provided for @chatV2AppBarUnpinTooltip.
  ///
  /// In en, this message translates to:
  /// **'Unpin'**
  String get chatV2AppBarUnpinTooltip;

  /// No description provided for @chatV2AppBarStopTooltip.
  ///
  /// In en, this message translates to:
  /// **'Stop generating'**
  String get chatV2AppBarStopTooltip;

  /// No description provided for @chatV2AppBarStoppingTooltip.
  ///
  /// In en, this message translates to:
  /// **'Stopping…'**
  String get chatV2AppBarStoppingTooltip;

  /// No description provided for @chatV2AppBarSearchTooltip.
  ///
  /// In en, this message translates to:
  /// **'Search in conversation (Cmd/Ctrl+F)'**
  String get chatV2AppBarSearchTooltip;

  /// No description provided for @chatV2AppBarMultiSelectTooltip.
  ///
  /// In en, this message translates to:
  /// **'Multi-select messages'**
  String get chatV2AppBarMultiSelectTooltip;

  /// No description provided for @chatV2AppBarShortcutsTooltip.
  ///
  /// In en, this message translates to:
  /// **'Keyboard shortcuts (?)'**
  String get chatV2AppBarShortcutsTooltip;

  /// No description provided for @chatV2AppBarSettingsTooltip.
  ///
  /// In en, this message translates to:
  /// **'Conversation settings (system prompt, etc)'**
  String get chatV2AppBarSettingsTooltip;

  /// No description provided for @chatV2AppBarMore.
  ///
  /// In en, this message translates to:
  /// **'More'**
  String get chatV2AppBarMore;

  /// No description provided for @chatV2AppBarStreaming.
  ///
  /// In en, this message translates to:
  /// **'Generating'**
  String get chatV2AppBarStreaming;

  /// No description provided for @chatV2AppBarStopping.
  ///
  /// In en, this message translates to:
  /// **'Stopping…'**
  String get chatV2AppBarStopping;

  /// No description provided for @chatV2HeroSubtitle.
  ///
  /// In en, this message translates to:
  /// **'What do you want to do today? Pick a starter, or '**
  String get chatV2HeroSubtitle;

  /// No description provided for @chatV2HeroNewBlank.
  ///
  /// In en, this message translates to:
  /// **'new blank conversation'**
  String get chatV2HeroNewBlank;

  /// No description provided for @chatV2HeroSkillsLabel.
  ///
  /// In en, this message translates to:
  /// **'My skills'**
  String get chatV2HeroSkillsLabel;

  /// No description provided for @chatV2HeroRecentLabel.
  ///
  /// In en, this message translates to:
  /// **'Recent conversations'**
  String get chatV2HeroRecentLabel;

  /// No description provided for @chatV2HeroRecentModelsLabel.
  ///
  /// In en, this message translates to:
  /// **'Recent models'**
  String get chatV2HeroRecentModelsLabel;

  /// No description provided for @chatV2HeroKvLabel.
  ///
  /// In en, this message translates to:
  /// **'This month'**
  String get chatV2HeroKvLabel;

  /// No description provided for @chatV2HeroKvMonthMessages.
  ///
  /// In en, this message translates to:
  /// **'Conversations'**
  String get chatV2HeroKvMonthMessages;

  /// No description provided for @chatV2HeroKvCredits.
  ///
  /// In en, this message translates to:
  /// **'Credits left'**
  String get chatV2HeroKvCredits;

  /// No description provided for @chatV2HeroKvStreak.
  ///
  /// In en, this message translates to:
  /// **'Streak'**
  String get chatV2HeroKvStreak;

  /// No description provided for @chatV2HeroSetDefaultModel.
  ///
  /// In en, this message translates to:
  /// **'Default model set: {model}'**
  String chatV2HeroSetDefaultModel(Object model);

  /// No description provided for @chatV2HeroStatsThreads.
  ///
  /// In en, this message translates to:
  /// **'{messages} messages · {threads} conversations'**
  String chatV2HeroStatsThreads(Object messages, Object threads);

  /// No description provided for @chatV2HeroStatsThisWeek.
  ///
  /// In en, this message translates to:
  /// **'This week {messages} · {active} active'**
  String chatV2HeroStatsThisWeek(Object active, Object messages);

  /// No description provided for @chatV2HeroStatsRecentDays.
  ///
  /// In en, this message translates to:
  /// **'Last {days}d {messages} · {active} active'**
  String chatV2HeroStatsRecentDays(Object active, Object days, Object messages);

  /// No description provided for @chatV2HeroStreakChip.
  ///
  /// In en, this message translates to:
  /// **'{n}-day streak'**
  String chatV2HeroStreakChip(Object n);

  /// No description provided for @chatV2HeroStreakTooltip.
  ///
  /// In en, this message translates to:
  /// **'{n} consecutive days of conversations'**
  String chatV2HeroStreakTooltip(Object n);

  /// No description provided for @chatV2HeroStatsSwitchTooltip.
  ///
  /// In en, this message translates to:
  /// **'Last {days} days: {messages} messages across {active} conversations\nTap to switch 7 / 30 day view'**
  String chatV2HeroStatsSwitchTooltip(
    Object active,
    Object days,
    Object messages,
  );

  /// No description provided for @chatV2ComposerHint.
  ///
  /// In en, this message translates to:
  /// **'What can I help with? @ to mention a skill, / for commands'**
  String get chatV2ComposerHint;

  /// No description provided for @chatV2ComposerDisclaimer.
  ///
  /// In en, this message translates to:
  /// **'AI-generated content — please verify important information'**
  String get chatV2ComposerDisclaimer;

  /// No description provided for @chatV2ComposerHintStreaming.
  ///
  /// In en, this message translates to:
  /// **'Generating…'**
  String get chatV2ComposerHintStreaming;

  /// No description provided for @chatV2ComposerAttachTooltip.
  ///
  /// In en, this message translates to:
  /// **'Attach image (drag-drop or paste also works)'**
  String get chatV2ComposerAttachTooltip;

  /// No description provided for @chatV2ComposerAttachNeedThread.
  ///
  /// In en, this message translates to:
  /// **'Pick a conversation first'**
  String get chatV2ComposerAttachNeedThread;

  /// No description provided for @chatV2ComposerAttachCamera.
  ///
  /// In en, this message translates to:
  /// **'Take photo'**
  String get chatV2ComposerAttachCamera;

  /// No description provided for @chatV2ComposerAttachGallery.
  ///
  /// In en, this message translates to:
  /// **'Choose from gallery'**
  String get chatV2ComposerAttachGallery;

  /// No description provided for @chatV2ComposerAttachFile.
  ///
  /// In en, this message translates to:
  /// **'Choose file'**
  String get chatV2ComposerAttachFile;

  /// No description provided for @chatV2ComposerWebOn.
  ///
  /// In en, this message translates to:
  /// **'Web search: ON (single-shot)'**
  String get chatV2ComposerWebOn;

  /// No description provided for @chatV2ComposerWebOff.
  ///
  /// In en, this message translates to:
  /// **'Web search: off — click to enable'**
  String get chatV2ComposerWebOff;

  /// No description provided for @chatV2ComposerWebSnack.
  ///
  /// In en, this message translates to:
  /// **'Web search: auto-disables after this send'**
  String get chatV2ComposerWebSnack;

  /// No description provided for @chatV2ComposerSendTooltip.
  ///
  /// In en, this message translates to:
  /// **'Send'**
  String get chatV2ComposerSendTooltip;

  /// No description provided for @chatV2ComposerCancelTooltip.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get chatV2ComposerCancelTooltip;

  /// No description provided for @chatV2ComposerCharTokens.
  ///
  /// In en, this message translates to:
  /// **'{chars} chars  ·  ~{tokens} tokens'**
  String chatV2ComposerCharTokens(Object chars, Object tokens);

  /// No description provided for @chatV2ComposerStopping.
  ///
  /// In en, this message translates to:
  /// **'Stopping…'**
  String get chatV2ComposerStopping;

  /// No description provided for @chatV2ComposerStoppingTooltip.
  ///
  /// In en, this message translates to:
  /// **'Stopping…'**
  String get chatV2ComposerStoppingTooltip;

  /// No description provided for @chatV2ComposerErrAttachOnlyImage.
  ///
  /// In en, this message translates to:
  /// **'Only image attachments supported, skipped {name}'**
  String chatV2ComposerErrAttachOnlyImage(Object name);

  /// No description provided for @chatV2ComposerErrAttachTooLarge.
  ///
  /// In en, this message translates to:
  /// **'Image must be under 10MB'**
  String get chatV2ComposerErrAttachTooLarge;

  /// No description provided for @chatV2ComposerErrAttach.
  ///
  /// In en, this message translates to:
  /// **'Attachment error: {err}'**
  String chatV2ComposerErrAttach(Object err);

  /// No description provided for @chatV2ComposerSlashDialogTitle.
  ///
  /// In en, this message translates to:
  /// **'Slash command'**
  String get chatV2ComposerSlashDialogTitle;

  /// No description provided for @chatV2DialogOk.
  ///
  /// In en, this message translates to:
  /// **'OK'**
  String get chatV2DialogOk;

  /// No description provided for @chatV2ComposerModelSwitchTooltip.
  ///
  /// In en, this message translates to:
  /// **'Switch model'**
  String get chatV2ComposerModelSwitchTooltip;

  /// No description provided for @chatV2ComposerModelDefault.
  ///
  /// In en, this message translates to:
  /// **'BiuMind default'**
  String get chatV2ComposerModelDefault;

  /// No description provided for @chatV2ComposerModelRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh model list'**
  String get chatV2ComposerModelRefresh;

  /// No description provided for @chatV2NewThreadFallback.
  ///
  /// In en, this message translates to:
  /// **'New conversation'**
  String get chatV2NewThreadFallback;

  /// No description provided for @chatV2SidebarTitle.
  ///
  /// In en, this message translates to:
  /// **'Conversations'**
  String get chatV2SidebarTitle;

  /// No description provided for @chatV2SidebarFilterHint.
  ///
  /// In en, this message translates to:
  /// **'Filter conversations…'**
  String get chatV2SidebarFilterHint;

  /// No description provided for @chatV2SidebarPaletteTooltip.
  ///
  /// In en, this message translates to:
  /// **'Command palette (Cmd/Ctrl+K)'**
  String get chatV2SidebarPaletteTooltip;

  /// No description provided for @chatV2SidebarStarredTooltip.
  ///
  /// In en, this message translates to:
  /// **'Starred messages'**
  String get chatV2SidebarStarredTooltip;

  /// No description provided for @chatV2SidebarCrossSearchTooltip.
  ///
  /// In en, this message translates to:
  /// **'Search all (Cmd/Ctrl+Shift+F)'**
  String get chatV2SidebarCrossSearchTooltip;

  /// No description provided for @chatV2SidebarImportTooltip.
  ///
  /// In en, this message translates to:
  /// **'Import JSON'**
  String get chatV2SidebarImportTooltip;

  /// No description provided for @chatV2SidebarNewTooltip.
  ///
  /// In en, this message translates to:
  /// **'New conversation'**
  String get chatV2SidebarNewTooltip;

  /// No description provided for @chatV2SidebarSectionPinned.
  ///
  /// In en, this message translates to:
  /// **'Pinned'**
  String get chatV2SidebarSectionPinned;

  /// No description provided for @chatV2SidebarSectionOthers.
  ///
  /// In en, this message translates to:
  /// **'Others'**
  String get chatV2SidebarSectionOthers;

  /// No description provided for @chatV2SidebarEmptyNew.
  ///
  /// In en, this message translates to:
  /// **'No conversations yet\nClick + above to start'**
  String get chatV2SidebarEmptyNew;

  /// No description provided for @chatV2SidebarEmptyFiltered.
  ///
  /// In en, this message translates to:
  /// **'No matching conversations'**
  String get chatV2SidebarEmptyFiltered;

  /// No description provided for @chatV2SidebarArchivedFooter.
  ///
  /// In en, this message translates to:
  /// **'Archived {count}'**
  String chatV2SidebarArchivedFooter(Object count);

  /// No description provided for @chatV2SidebarBatchTooltip.
  ///
  /// In en, this message translates to:
  /// **'Batch manage'**
  String get chatV2SidebarBatchTooltip;

  /// No description provided for @chatV2BatchSelectedCount.
  ///
  /// In en, this message translates to:
  /// **'{count} selected'**
  String chatV2BatchSelectedCount(Object count);

  /// No description provided for @chatV2BatchSelectAll.
  ///
  /// In en, this message translates to:
  /// **'Select all'**
  String get chatV2BatchSelectAll;

  /// No description provided for @chatV2BatchSelectNone.
  ///
  /// In en, this message translates to:
  /// **'Clear'**
  String get chatV2BatchSelectNone;

  /// No description provided for @chatV2BatchDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get chatV2BatchDelete;

  /// No description provided for @chatV2BatchExitTooltip.
  ///
  /// In en, this message translates to:
  /// **'Exit batch manage'**
  String get chatV2BatchExitTooltip;

  /// No description provided for @chatV2BatchDeleteTitle.
  ///
  /// In en, this message translates to:
  /// **'Delete conversations'**
  String get chatV2BatchDeleteTitle;

  /// No description provided for @chatV2BatchDeleteBody.
  ///
  /// In en, this message translates to:
  /// **'Delete the {count} selected conversations? This cannot be undone.'**
  String chatV2BatchDeleteBody(Object count);

  /// No description provided for @chatV2BatchDeletedCount.
  ///
  /// In en, this message translates to:
  /// **'Deleted {count} conversations'**
  String chatV2BatchDeletedCount(Object count);

  /// No description provided for @chatV2LoadError.
  ///
  /// In en, this message translates to:
  /// **'Load failed: {err}'**
  String chatV2LoadError(Object err);

  /// No description provided for @chatV2ExportSuccess.
  ///
  /// In en, this message translates to:
  /// **'Exported {name}'**
  String chatV2ExportSuccess(Object name);

  /// No description provided for @chatV2ExportAllSuccess.
  ///
  /// In en, this message translates to:
  /// **'Exported all conversations → {name}'**
  String chatV2ExportAllSuccess(Object name);

  /// No description provided for @chatV2ExportFailed.
  ///
  /// In en, this message translates to:
  /// **'Export failed: {err}'**
  String chatV2ExportFailed(Object err);

  /// No description provided for @chatV2ImportSuccessCount.
  ///
  /// In en, this message translates to:
  /// **'Imported {count} conversations'**
  String chatV2ImportSuccessCount(Object count);

  /// No description provided for @chatV2ImportSuccess.
  ///
  /// In en, this message translates to:
  /// **'Conversation imported'**
  String get chatV2ImportSuccess;

  /// No description provided for @chatV2ImportFailed.
  ///
  /// In en, this message translates to:
  /// **'Import failed: {err}'**
  String chatV2ImportFailed(Object err);

  /// No description provided for @chatV2ApplyTemplate.
  ///
  /// In en, this message translates to:
  /// **'Applied template \"{name}\"'**
  String chatV2ApplyTemplate(Object name);

  /// No description provided for @chatV2PaletteGroupOps.
  ///
  /// In en, this message translates to:
  /// **'Actions'**
  String get chatV2PaletteGroupOps;

  /// No description provided for @chatV2PaletteGroupCurrent.
  ///
  /// In en, this message translates to:
  /// **'Current conversation'**
  String get chatV2PaletteGroupCurrent;

  /// No description provided for @chatV2PaletteGroupSwitch.
  ///
  /// In en, this message translates to:
  /// **'Switch conversation'**
  String get chatV2PaletteGroupSwitch;

  /// No description provided for @chatV2PaletteNewThread.
  ///
  /// In en, this message translates to:
  /// **'New conversation'**
  String get chatV2PaletteNewThread;

  /// No description provided for @chatV2PaletteNewThreadHint.
  ///
  /// In en, this message translates to:
  /// **'Open new conversation wizard'**
  String get chatV2PaletteNewThreadHint;

  /// No description provided for @chatV2PaletteCrossSearch.
  ///
  /// In en, this message translates to:
  /// **'Search all conversations'**
  String get chatV2PaletteCrossSearch;

  /// No description provided for @chatV2PaletteStarred.
  ///
  /// In en, this message translates to:
  /// **'View starred messages'**
  String get chatV2PaletteStarred;

  /// No description provided for @chatV2PaletteStarredHint.
  ///
  /// In en, this message translates to:
  /// **'All ⭐ messages across threads'**
  String get chatV2PaletteStarredHint;

  /// No description provided for @chatV2PaletteDrafts.
  ///
  /// In en, this message translates to:
  /// **'View drafts'**
  String get chatV2PaletteDrafts;

  /// No description provided for @chatV2PaletteDraftsHint.
  ///
  /// In en, this message translates to:
  /// **'Unsent input across all threads'**
  String get chatV2PaletteDraftsHint;

  /// No description provided for @chatV2PaletteArchived.
  ///
  /// In en, this message translates to:
  /// **'View archived'**
  String get chatV2PaletteArchived;

  /// No description provided for @chatV2PaletteArchivedHint.
  ///
  /// In en, this message translates to:
  /// **'Unarchive / permanently delete'**
  String get chatV2PaletteArchivedHint;

  /// No description provided for @chatV2PaletteExportAll.
  ///
  /// In en, this message translates to:
  /// **'Export all conversations'**
  String get chatV2PaletteExportAll;

  /// No description provided for @chatV2PaletteExportAllHint.
  ///
  /// In en, this message translates to:
  /// **'One-shot JSON backup (incl. archived)'**
  String get chatV2PaletteExportAllHint;

  /// No description provided for @chatV2PaletteShortcuts.
  ///
  /// In en, this message translates to:
  /// **'View keyboard shortcuts'**
  String get chatV2PaletteShortcuts;

  /// No description provided for @chatV2PaletteShortcutsHint.
  ///
  /// In en, this message translates to:
  /// **'Or Shift+?'**
  String get chatV2PaletteShortcutsHint;

  /// No description provided for @chatV2PaletteSettings.
  ///
  /// In en, this message translates to:
  /// **'Open chat preferences'**
  String get chatV2PaletteSettings;

  /// No description provided for @chatV2PaletteSettingsHint.
  ///
  /// In en, this message translates to:
  /// **'Font / default mode / default model'**
  String get chatV2PaletteSettingsHint;

  /// No description provided for @chatV2PaletteMultiSelect.
  ///
  /// In en, this message translates to:
  /// **'Multi-select messages here'**
  String get chatV2PaletteMultiSelect;

  /// No description provided for @chatV2PaletteApplyTemplate.
  ///
  /// In en, this message translates to:
  /// **'Apply system prompt template'**
  String get chatV2PaletteApplyTemplate;

  /// No description provided for @chatV2PaletteApplyTemplateHint.
  ///
  /// In en, this message translates to:
  /// **'Pick a saved one for current conversation'**
  String get chatV2PaletteApplyTemplateHint;

  /// No description provided for @chatV2PaletteManageTemplates.
  ///
  /// In en, this message translates to:
  /// **'Manage system prompt templates'**
  String get chatV2PaletteManageTemplates;

  /// No description provided for @chatV2PaletteManageTemplatesHint.
  ///
  /// In en, this message translates to:
  /// **'Add / edit / delete saved prompts'**
  String get chatV2PaletteManageTemplatesHint;

  /// No description provided for @chatV2PaletteSwitchHint.
  ///
  /// In en, this message translates to:
  /// **'Switch to this conversation'**
  String get chatV2PaletteSwitchHint;

  /// No description provided for @chatV2ThreadStatusGenerating.
  ///
  /// In en, this message translates to:
  /// **'Generating'**
  String get chatV2ThreadStatusGenerating;

  /// No description provided for @chatV2ThreadStatusStopping.
  ///
  /// In en, this message translates to:
  /// **'Stopping…'**
  String get chatV2ThreadStatusStopping;

  /// No description provided for @chatV2OverflowShareCopied.
  ///
  /// In en, this message translates to:
  /// **'Share link copied'**
  String get chatV2OverflowShareCopied;

  /// No description provided for @chatV2OverflowShareCopiedUrl.
  ///
  /// In en, this message translates to:
  /// **'Copied {url}'**
  String chatV2OverflowShareCopiedUrl(Object url);

  /// No description provided for @chatV2OverflowIdCopied.
  ///
  /// In en, this message translates to:
  /// **'Thread ID copied'**
  String get chatV2OverflowIdCopied;

  /// No description provided for @chatV2NewDialogTitle.
  ///
  /// In en, this message translates to:
  /// **'New conversation'**
  String get chatV2NewDialogTitle;

  /// No description provided for @chatV2NewDialogTitleField.
  ///
  /// In en, this message translates to:
  /// **'Title (optional)'**
  String get chatV2NewDialogTitleField;

  /// No description provided for @chatV2NewDialogTitleSuggested.
  ///
  /// In en, this message translates to:
  /// **'Suggested: {suggested}'**
  String chatV2NewDialogTitleSuggested(Object suggested);

  /// No description provided for @chatV2NewDialogModelLabel.
  ///
  /// In en, this message translates to:
  /// **'Model'**
  String get chatV2NewDialogModelLabel;

  /// No description provided for @chatV2NewDialogRefreshTooltip.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get chatV2NewDialogRefreshTooltip;

  /// No description provided for @chatV2NewDialogModelOfficial.
  ///
  /// In en, this message translates to:
  /// **'BiuMind (official default)'**
  String get chatV2NewDialogModelOfficial;

  /// No description provided for @chatV2NewDialogModelEmpty.
  ///
  /// In en, this message translates to:
  /// **'(No models available — contact admin)'**
  String get chatV2NewDialogModelEmpty;

  /// No description provided for @chatV2NewDialogModelLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load models: {err}'**
  String chatV2NewDialogModelLoadFailed(Object err);

  /// No description provided for @chatV2NewDialogSystemPromptLabel.
  ///
  /// In en, this message translates to:
  /// **'System prompt (optional)'**
  String get chatV2NewDialogSystemPromptLabel;

  /// No description provided for @chatV2NewDialogSystemPromptHint.
  ///
  /// In en, this message translates to:
  /// **'\"You are a…\" — leave blank for default'**
  String get chatV2NewDialogSystemPromptHint;

  /// No description provided for @chatV2NewDialogPickWorker.
  ///
  /// In en, this message translates to:
  /// **'Pick worker'**
  String get chatV2NewDialogPickWorker;

  /// No description provided for @chatV2NewDialogNoOnlineDaemon.
  ///
  /// In en, this message translates to:
  /// **'No online daemon'**
  String get chatV2NewDialogNoOnlineDaemon;

  /// No description provided for @chatV2NewDialogEmptyEnvAuto.
  ///
  /// In en, this message translates to:
  /// **'BiuMind desktop auto-launches local biu serve — please wait or check that biu CLI is installed; or run `biu serve` manually.'**
  String get chatV2NewDialogEmptyEnvAuto;

  /// No description provided for @chatV2NewDialogEmptyEnvHistory.
  ///
  /// In en, this message translates to:
  /// **'Agent mode requires a worker_kind=biu_daemon local process. {count} historical worker(s) are not daemons or are offline.'**
  String chatV2NewDialogEmptyEnvHistory(Object count);

  /// No description provided for @chatV2NewDialogEnvLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Environment list failed: {err}'**
  String chatV2NewDialogEnvLoadFailed(Object err);

  /// No description provided for @chatV2NewDialogPoolTagLabel.
  ///
  /// In en, this message translates to:
  /// **'runtime pool tag (optional)'**
  String get chatV2NewDialogPoolTagLabel;

  /// No description provided for @chatV2NewDialogPoolTagHint.
  ///
  /// In en, this message translates to:
  /// **'Empty = default pool; or e.g. \"gpu\" / \"high-mem\"'**
  String get chatV2NewDialogPoolTagHint;

  /// No description provided for @chatV2NewDialogTaskModeHint.
  ///
  /// In en, this message translates to:
  /// **'Task mode lets brain dispatch the task to a runtime worker matching pool_tag. Tasks queue when no worker matches.'**
  String get chatV2NewDialogTaskModeHint;

  /// No description provided for @chatV2NewDialogCreate.
  ///
  /// In en, this message translates to:
  /// **'Create'**
  String get chatV2NewDialogCreate;

  /// No description provided for @chatV2NewDialogModeChat.
  ///
  /// In en, this message translates to:
  /// **'Chat'**
  String get chatV2NewDialogModeChat;

  /// No description provided for @chatV2NewDialogModeChatHint.
  ///
  /// In en, this message translates to:
  /// **'Talk to the model directly'**
  String get chatV2NewDialogModeChatHint;

  /// No description provided for @chatV2NewDialogModeAgent.
  ///
  /// In en, this message translates to:
  /// **'Agent'**
  String get chatV2NewDialogModeAgent;

  /// No description provided for @chatV2NewDialogModeAgentHint.
  ///
  /// In en, this message translates to:
  /// **'Run tools on a specific worker'**
  String get chatV2NewDialogModeAgentHint;

  /// No description provided for @chatV2NewDialogModeTask.
  ///
  /// In en, this message translates to:
  /// **'Task'**
  String get chatV2NewDialogModeTask;

  /// No description provided for @chatV2NewDialogModeTaskHint.
  ///
  /// In en, this message translates to:
  /// **'Background task execution'**
  String get chatV2NewDialogModeTaskHint;

  /// No description provided for @chatV2ShortcutsTitle.
  ///
  /// In en, this message translates to:
  /// **'Keyboard shortcuts'**
  String get chatV2ShortcutsTitle;

  /// No description provided for @chatV2ShortcutsSectionInput.
  ///
  /// In en, this message translates to:
  /// **'Input box'**
  String get chatV2ShortcutsSectionInput;

  /// No description provided for @chatV2ShortcutsSectionMessages.
  ///
  /// In en, this message translates to:
  /// **'Messages'**
  String get chatV2ShortcutsSectionMessages;

  /// No description provided for @chatV2ShortcutsSectionGlobal.
  ///
  /// In en, this message translates to:
  /// **'Global'**
  String get chatV2ShortcutsSectionGlobal;

  /// No description provided for @chatV2ShortcutsSend.
  ///
  /// In en, this message translates to:
  /// **'Send message'**
  String get chatV2ShortcutsSend;

  /// No description provided for @chatV2ShortcutsNewline.
  ///
  /// In en, this message translates to:
  /// **'Newline (no send)'**
  String get chatV2ShortcutsNewline;

  /// No description provided for @chatV2ShortcutsHistoryUp.
  ///
  /// In en, this message translates to:
  /// **'Previous draft (when input empty)'**
  String get chatV2ShortcutsHistoryUp;

  /// No description provided for @chatV2ShortcutsHistoryDown.
  ///
  /// In en, this message translates to:
  /// **'Next draft / leave history'**
  String get chatV2ShortcutsHistoryDown;

  /// No description provided for @chatV2ShortcutsSlash.
  ///
  /// In en, this message translates to:
  /// **'Open slash command palette'**
  String get chatV2ShortcutsSlash;

  /// No description provided for @chatV2ShortcutsEsc.
  ///
  /// In en, this message translates to:
  /// **'Close palette / search bar'**
  String get chatV2ShortcutsEsc;

  /// No description provided for @chatV2ShortcutsInThreadSearch.
  ///
  /// In en, this message translates to:
  /// **'Search in current conversation'**
  String get chatV2ShortcutsInThreadSearch;

  /// No description provided for @chatV2ShortcutsSearchNext.
  ///
  /// In en, this message translates to:
  /// **'Search bar: next match'**
  String get chatV2ShortcutsSearchNext;

  /// No description provided for @chatV2ShortcutsSearchPrev.
  ///
  /// In en, this message translates to:
  /// **'Search bar: previous match'**
  String get chatV2ShortcutsSearchPrev;

  /// No description provided for @chatV2ShortcutsPalette.
  ///
  /// In en, this message translates to:
  /// **'Open command palette'**
  String get chatV2ShortcutsPalette;

  /// No description provided for @chatV2ShortcutsNewThread.
  ///
  /// In en, this message translates to:
  /// **'New conversation'**
  String get chatV2ShortcutsNewThread;

  /// No description provided for @chatV2ShortcutsCrossSearch.
  ///
  /// In en, this message translates to:
  /// **'Search all conversations'**
  String get chatV2ShortcutsCrossSearch;

  /// No description provided for @chatV2ShortcutsModelPicker.
  ///
  /// In en, this message translates to:
  /// **'Switch model for current conversation'**
  String get chatV2ShortcutsModelPicker;

  /// No description provided for @chatV2ShortcutsHelp.
  ///
  /// In en, this message translates to:
  /// **'Open this help panel'**
  String get chatV2ShortcutsHelp;

  /// No description provided for @chatV2ArchivedTitle.
  ///
  /// In en, this message translates to:
  /// **'Archived conversations'**
  String get chatV2ArchivedTitle;

  /// No description provided for @chatV2ArchivedClose.
  ///
  /// In en, this message translates to:
  /// **'Close'**
  String get chatV2ArchivedClose;

  /// No description provided for @chatV2ArchivedEmpty.
  ///
  /// In en, this message translates to:
  /// **'No archived conversations'**
  String get chatV2ArchivedEmpty;

  /// No description provided for @chatV2ArchivedUnarchive.
  ///
  /// In en, this message translates to:
  /// **'Unarchive'**
  String get chatV2ArchivedUnarchive;

  /// No description provided for @chatV2ArchivedHardDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete forever'**
  String get chatV2ArchivedHardDelete;

  /// No description provided for @chatV2ArchivedHardDeleteTitle.
  ///
  /// In en, this message translates to:
  /// **'Delete forever'**
  String get chatV2ArchivedHardDeleteTitle;

  /// No description provided for @chatV2ArchivedHardDeleteBody.
  ///
  /// In en, this message translates to:
  /// **'Permanently delete \"{title}\"? This cannot be undone.'**
  String chatV2ArchivedHardDeleteBody(Object title);

  /// No description provided for @chatV2DraftsTitle.
  ///
  /// In en, this message translates to:
  /// **'Drafts'**
  String get chatV2DraftsTitle;

  /// No description provided for @chatV2DraftsEmpty.
  ///
  /// In en, this message translates to:
  /// **'No drafts. Anything you type in any conversation is auto-saved.'**
  String get chatV2DraftsEmpty;

  /// No description provided for @chatV2DraftsUnnamed.
  ///
  /// In en, this message translates to:
  /// **'(Unnamed / deleted)'**
  String get chatV2DraftsUnnamed;

  /// No description provided for @chatV2DraftsCharCount.
  ///
  /// In en, this message translates to:
  /// **'{count} chars'**
  String chatV2DraftsCharCount(Object count);

  /// No description provided for @chatV2DraftsDiscard.
  ///
  /// In en, this message translates to:
  /// **'Discard this draft'**
  String get chatV2DraftsDiscard;

  /// No description provided for @chatV2StarredTitle.
  ///
  /// In en, this message translates to:
  /// **'Starred messages'**
  String get chatV2StarredTitle;

  /// No description provided for @chatV2StarredEmpty.
  ///
  /// In en, this message translates to:
  /// **'No starred messages yet. Tap the ⭐ at the bottom of an assistant message to save it.'**
  String get chatV2StarredEmpty;

  /// No description provided for @chatV2StarredNoText.
  ///
  /// In en, this message translates to:
  /// **'(No text content)'**
  String get chatV2StarredNoText;

  /// No description provided for @chatV2CrossSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search all conversations…'**
  String get chatV2CrossSearchHint;

  /// No description provided for @chatV2CrossSearchHitCount.
  ///
  /// In en, this message translates to:
  /// **'{count} hits'**
  String chatV2CrossSearchHitCount(Object count);

  /// No description provided for @chatV2CrossSearchCloseTooltip.
  ///
  /// In en, this message translates to:
  /// **'Close (Esc)'**
  String get chatV2CrossSearchCloseTooltip;

  /// No description provided for @chatV2CrossSearchEmptyHint.
  ///
  /// In en, this message translates to:
  /// **'Type a keyword to search all history\n(press Esc to close)'**
  String get chatV2CrossSearchEmptyHint;

  /// No description provided for @chatV2CrossSearchNoMatch.
  ///
  /// In en, this message translates to:
  /// **'No matches'**
  String get chatV2CrossSearchNoMatch;

  /// No description provided for @chatV2PaletteSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search commands…'**
  String get chatV2PaletteSearchHint;

  /// No description provided for @chatV2PaletteNoMatch.
  ///
  /// In en, this message translates to:
  /// **'No matching commands'**
  String get chatV2PaletteNoMatch;

  /// No description provided for @chatV2InThreadSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search in current conversation…'**
  String get chatV2InThreadSearchHint;

  /// No description provided for @chatV2InThreadSearchPrev.
  ///
  /// In en, this message translates to:
  /// **'Previous (Shift+Enter)'**
  String get chatV2InThreadSearchPrev;

  /// No description provided for @chatV2InThreadSearchNext.
  ///
  /// In en, this message translates to:
  /// **'Next (Enter)'**
  String get chatV2InThreadSearchNext;

  /// No description provided for @chatV2HintIntro.
  ///
  /// In en, this message translates to:
  /// **'Tip: press '**
  String get chatV2HintIntro;

  /// No description provided for @chatV2HintBeforeCrossSearch.
  ///
  /// In en, this message translates to:
  /// **' to summon command palette; press '**
  String get chatV2HintBeforeCrossSearch;

  /// No description provided for @chatV2HintAfterCrossSearch.
  ///
  /// In en, this message translates to:
  /// **' for cross-thread search; '**
  String get chatV2HintAfterCrossSearch;

  /// No description provided for @chatV2HintAfterHelp.
  ///
  /// In en, this message translates to:
  /// **' for all shortcuts.'**
  String get chatV2HintAfterHelp;

  /// No description provided for @chatV2ChangelogSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Command palette · Cross-thread search · Drafts · Favorites · Model tips'**
  String get chatV2ChangelogSubtitle;

  /// No description provided for @chatV2ChangelogBullet1.
  ///
  /// In en, this message translates to:
  /// **'🔍 Cross-thread search (Cmd/Ctrl+Shift+F) + command palette (Cmd/Ctrl+K)'**
  String get chatV2ChangelogBullet1;

  /// No description provided for @chatV2ChangelogBullet2.
  ///
  /// In en, this message translates to:
  /// **'⭐ Starred-message sidebar + draft index + prompt templates'**
  String get chatV2ChangelogBullet2;

  /// No description provided for @chatV2ChangelogBullet3.
  ///
  /// In en, this message translates to:
  /// **'📤 One-click import/export (incl. bulk backup)'**
  String get chatV2ChangelogBullet3;

  /// No description provided for @chatV2ChangelogBullet4.
  ///
  /// In en, this message translates to:
  /// **'🎨 Code blocks: line numbers / wrap / language switch / save to file'**
  String get chatV2ChangelogBullet4;

  /// No description provided for @chatV2ChangelogBullet5.
  ///
  /// In en, this message translates to:
  /// **'⚡ Multimodal attachments / slash skill invocation / streaming token/s'**
  String get chatV2ChangelogBullet5;

  /// No description provided for @chatV2ChangelogDetails.
  ///
  /// In en, this message translates to:
  /// **'Details'**
  String get chatV2ChangelogDetails;

  /// No description provided for @chatV2SelectionSelectedCount.
  ///
  /// In en, this message translates to:
  /// **'{count} selected'**
  String chatV2SelectionSelectedCount(Object count);

  /// No description provided for @chatV2SelectionSelectAll.
  ///
  /// In en, this message translates to:
  /// **'Select all'**
  String get chatV2SelectionSelectAll;

  /// No description provided for @chatV2SelectionCopy.
  ///
  /// In en, this message translates to:
  /// **'Copy'**
  String get chatV2SelectionCopy;

  /// No description provided for @chatV2SelectionTranslate.
  ///
  /// In en, this message translates to:
  /// **'Translate'**
  String get chatV2SelectionTranslate;

  /// No description provided for @chatV2SelectionExportMd.
  ///
  /// In en, this message translates to:
  /// **'Export MD'**
  String get chatV2SelectionExportMd;

  /// No description provided for @chatV2SelectionDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get chatV2SelectionDelete;

  /// No description provided for @chatV2SelectionCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get chatV2SelectionCancel;

  /// No description provided for @chatV2SelectionCopiedCount.
  ///
  /// In en, this message translates to:
  /// **'Copied {count} messages'**
  String chatV2SelectionCopiedCount(Object count);

  /// No description provided for @chatV2SelectionTruncated.
  ///
  /// In en, this message translates to:
  /// **'Text too long, truncated to 4500 chars'**
  String get chatV2SelectionTruncated;

  /// No description provided for @chatV2SelectionTranslateFailed.
  ///
  /// In en, this message translates to:
  /// **'Translate open failed: {err}'**
  String chatV2SelectionTranslateFailed(Object err);

  /// No description provided for @chatV2SelectionExportedCount.
  ///
  /// In en, this message translates to:
  /// **'Exported {count} messages'**
  String chatV2SelectionExportedCount(Object count);

  /// No description provided for @chatV2SelectionDeleteTitle.
  ///
  /// In en, this message translates to:
  /// **'Delete messages'**
  String get chatV2SelectionDeleteTitle;

  /// No description provided for @chatV2SelectionDeleteBody.
  ///
  /// In en, this message translates to:
  /// **'Delete the {count} selected messages? This cannot be undone.'**
  String chatV2SelectionDeleteBody(Object count);

  /// No description provided for @chatV2SelectionMdUnnamed.
  ///
  /// In en, this message translates to:
  /// **'(Untitled)'**
  String get chatV2SelectionMdUnnamed;

  /// No description provided for @chatV2SelectionMdModelUnset.
  ///
  /// In en, this message translates to:
  /// **'(unset)'**
  String get chatV2SelectionMdModelUnset;

  /// No description provided for @chatV2TemplatesTitle.
  ///
  /// In en, this message translates to:
  /// **'System prompt templates'**
  String get chatV2TemplatesTitle;

  /// No description provided for @chatV2TemplatesNew.
  ///
  /// In en, this message translates to:
  /// **'New'**
  String get chatV2TemplatesNew;

  /// No description provided for @chatV2TemplatesEmpty.
  ///
  /// In en, this message translates to:
  /// **'No templates yet. Tap \"New\" in the top-right to save a frequently-used system prompt.'**
  String get chatV2TemplatesEmpty;

  /// No description provided for @chatV2TemplatesApply.
  ///
  /// In en, this message translates to:
  /// **'Apply'**
  String get chatV2TemplatesApply;

  /// No description provided for @chatV2TemplatesEdit.
  ///
  /// In en, this message translates to:
  /// **'Edit'**
  String get chatV2TemplatesEdit;

  /// No description provided for @chatV2TemplatesDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get chatV2TemplatesDelete;

  /// No description provided for @chatV2TemplatesDeleteTitle.
  ///
  /// In en, this message translates to:
  /// **'Delete template'**
  String get chatV2TemplatesDeleteTitle;

  /// No description provided for @chatV2TemplatesDeleteBody.
  ///
  /// In en, this message translates to:
  /// **'Delete \"{name}\"? This cannot be undone.'**
  String chatV2TemplatesDeleteBody(Object name);

  /// No description provided for @chatV2TemplatesEditDialogNew.
  ///
  /// In en, this message translates to:
  /// **'New template'**
  String get chatV2TemplatesEditDialogNew;

  /// No description provided for @chatV2TemplatesEditDialogEdit.
  ///
  /// In en, this message translates to:
  /// **'Edit template'**
  String get chatV2TemplatesEditDialogEdit;

  /// No description provided for @chatV2TemplatesNameLabel.
  ///
  /// In en, this message translates to:
  /// **'Name'**
  String get chatV2TemplatesNameLabel;

  /// No description provided for @chatV2TemplatesNameHint.
  ///
  /// In en, this message translates to:
  /// **'e.g. Flutter architect'**
  String get chatV2TemplatesNameHint;

  /// No description provided for @chatV2TemplatesContentLabel.
  ///
  /// In en, this message translates to:
  /// **'System prompt'**
  String get chatV2TemplatesContentLabel;

  /// No description provided for @chatV2TemplatesContentHint.
  ///
  /// In en, this message translates to:
  /// **'Enter the full system prompt…'**
  String get chatV2TemplatesContentHint;

  /// No description provided for @chatV2SettingsSheetTitle.
  ///
  /// In en, this message translates to:
  /// **'Conversation settings'**
  String get chatV2SettingsSheetTitle;

  /// No description provided for @chatV2SettingsSheetSaveFailed.
  ///
  /// In en, this message translates to:
  /// **'Save failed: {err}'**
  String chatV2SettingsSheetSaveFailed(Object err);

  /// No description provided for @chatV2SettingsSheetNotFound.
  ///
  /// In en, this message translates to:
  /// **'Conversation not found'**
  String get chatV2SettingsSheetNotFound;

  /// No description provided for @chatV2SettingsSheetMode.
  ///
  /// In en, this message translates to:
  /// **'Mode'**
  String get chatV2SettingsSheetMode;

  /// No description provided for @chatV2SettingsSheetModel.
  ///
  /// In en, this message translates to:
  /// **'Model'**
  String get chatV2SettingsSheetModel;

  /// No description provided for @chatV2SettingsSheetModelDefault.
  ///
  /// In en, this message translates to:
  /// **'Default'**
  String get chatV2SettingsSheetModelDefault;

  /// No description provided for @chatV2SettingsSheetCreated.
  ///
  /// In en, this message translates to:
  /// **'Created'**
  String get chatV2SettingsSheetCreated;

  /// No description provided for @chatV2SettingsSheetUpdated.
  ///
  /// In en, this message translates to:
  /// **'Updated'**
  String get chatV2SettingsSheetUpdated;

  /// No description provided for @chatV2SettingsSheetFromTemplate.
  ///
  /// In en, this message translates to:
  /// **'From template'**
  String get chatV2SettingsSheetFromTemplate;

  /// No description provided for @chatV2SettingsSheetClear.
  ///
  /// In en, this message translates to:
  /// **'Clear'**
  String get chatV2SettingsSheetClear;

  /// No description provided for @chatV2SettingsSheetHint.
  ///
  /// In en, this message translates to:
  /// **'Attached as a system message to every request; tap \"Save\" top-right.'**
  String get chatV2SettingsSheetHint;

  /// No description provided for @chatV2SettingsSheetPromptHint.
  ///
  /// In en, this message translates to:
  /// **'e.g. You are a senior Flutter architect; always cite file + line number.'**
  String get chatV2SettingsSheetPromptHint;

  /// No description provided for @chatV2AttachRemove.
  ///
  /// In en, this message translates to:
  /// **'Remove'**
  String get chatV2AttachRemove;

  /// No description provided for @chatV2OverflowMore.
  ///
  /// In en, this message translates to:
  /// **'More'**
  String get chatV2OverflowMore;

  /// No description provided for @chatV2OverflowPin.
  ///
  /// In en, this message translates to:
  /// **'Pin'**
  String get chatV2OverflowPin;

  /// No description provided for @chatV2OverflowUnpin.
  ///
  /// In en, this message translates to:
  /// **'Unpin'**
  String get chatV2OverflowUnpin;

  /// No description provided for @chatV2OverflowRename.
  ///
  /// In en, this message translates to:
  /// **'Rename'**
  String get chatV2OverflowRename;

  /// No description provided for @chatV2OverflowArchive.
  ///
  /// In en, this message translates to:
  /// **'Archive'**
  String get chatV2OverflowArchive;

  /// No description provided for @chatV2OverflowExportJson.
  ///
  /// In en, this message translates to:
  /// **'Export JSON'**
  String get chatV2OverflowExportJson;

  /// No description provided for @chatV2OverflowShareLink.
  ///
  /// In en, this message translates to:
  /// **'Copy share link'**
  String get chatV2OverflowShareLink;

  /// No description provided for @chatV2OverflowCopyId.
  ///
  /// In en, this message translates to:
  /// **'Copy thread ID'**
  String get chatV2OverflowCopyId;

  /// No description provided for @chatV2OverflowDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get chatV2OverflowDelete;

  /// No description provided for @chatV2OverflowDeleteConfirmTitle.
  ///
  /// In en, this message translates to:
  /// **'Delete conversation'**
  String get chatV2OverflowDeleteConfirmTitle;

  /// No description provided for @chatV2OverflowDeleteConfirmBody.
  ///
  /// In en, this message translates to:
  /// **'Delete \"{title}\"? This cannot be undone.'**
  String chatV2OverflowDeleteConfirmBody(Object title);

  /// No description provided for @chatV2RenameDialogTitle.
  ///
  /// In en, this message translates to:
  /// **'Rename conversation'**
  String get chatV2RenameDialogTitle;

  /// No description provided for @chatV2RenameDialogHint.
  ///
  /// In en, this message translates to:
  /// **'Enter new name'**
  String get chatV2RenameDialogHint;

  /// No description provided for @chatV2DialogCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get chatV2DialogCancel;

  /// No description provided for @chatV2DialogSave.
  ///
  /// In en, this message translates to:
  /// **'Save'**
  String get chatV2DialogSave;

  /// No description provided for @chatV2DialogDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get chatV2DialogDelete;

  /// No description provided for @chatV2ApprovalTitle.
  ///
  /// In en, this message translates to:
  /// **'Allow {toolName}?'**
  String chatV2ApprovalTitle(Object toolName);

  /// No description provided for @chatV2ApprovalAllow.
  ///
  /// In en, this message translates to:
  /// **'Allow'**
  String get chatV2ApprovalAllow;

  /// No description provided for @chatV2ApprovalDeny.
  ///
  /// In en, this message translates to:
  /// **'Deny'**
  String get chatV2ApprovalDeny;

  /// No description provided for @chatV2ApprovalAlways.
  ///
  /// In en, this message translates to:
  /// **'Always allow'**
  String get chatV2ApprovalAlways;

  /// No description provided for @chatV2ApprovalShowMore.
  ///
  /// In en, this message translates to:
  /// **'Show full input ▾'**
  String get chatV2ApprovalShowMore;

  /// No description provided for @chatV2ComposerModeChat.
  ///
  /// In en, this message translates to:
  /// **'Chat'**
  String get chatV2ComposerModeChat;

  /// No description provided for @chatV2ComposerModeAgent.
  ///
  /// In en, this message translates to:
  /// **'Agent'**
  String get chatV2ComposerModeAgent;

  /// No description provided for @chatV2ComposerModeChatHint.
  ///
  /// In en, this message translates to:
  /// **'Pure model — no tools'**
  String get chatV2ComposerModeChatHint;

  /// No description provided for @chatV2ComposerModeAgentHint.
  ///
  /// In en, this message translates to:
  /// **'With tools via daemon'**
  String get chatV2ComposerModeAgentHint;

  /// No description provided for @chatV2ComposerModeNoDaemon.
  ///
  /// In en, this message translates to:
  /// **'No daemon online'**
  String get chatV2ComposerModeNoDaemon;

  /// No description provided for @chatV2ComposerWorkdirSet.
  ///
  /// In en, this message translates to:
  /// **'Set working directory'**
  String get chatV2ComposerWorkdirSet;

  /// No description provided for @chatV2ComposerWorkdirNone.
  ///
  /// In en, this message translates to:
  /// **'No workdir'**
  String get chatV2ComposerWorkdirNone;

  /// No description provided for @chatV2ComposerWorkdirClear.
  ///
  /// In en, this message translates to:
  /// **'Clear workdir'**
  String get chatV2ComposerWorkdirClear;

  /// No description provided for @chatV2ComposerAutoApproveAuto.
  ///
  /// In en, this message translates to:
  /// **'Auto approve'**
  String get chatV2ComposerAutoApproveAuto;

  /// No description provided for @chatV2ComposerAutoApproveWhitelist.
  ///
  /// In en, this message translates to:
  /// **'Whitelist'**
  String get chatV2ComposerAutoApproveWhitelist;

  /// No description provided for @chatV2ComposerAutoApproveManual.
  ///
  /// In en, this message translates to:
  /// **'Manual'**
  String get chatV2ComposerAutoApproveManual;

  /// No description provided for @chatV2ComposerAutoApproveTooltip.
  ///
  /// In en, this message translates to:
  /// **'Tool-call approval mode'**
  String get chatV2ComposerAutoApproveTooltip;

  /// No description provided for @chatV2ModelPickerSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search models…'**
  String get chatV2ModelPickerSearchHint;

  /// No description provided for @chatV2ModelPickerSettings.
  ///
  /// In en, this message translates to:
  /// **'Settings'**
  String get chatV2ModelPickerSettings;

  /// No description provided for @chatV2ModelPickerEmpty.
  ///
  /// In en, this message translates to:
  /// **'No models available'**
  String get chatV2ModelPickerEmpty;

  /// No description provided for @chatV2ModelPickerEmptyAction.
  ///
  /// In en, this message translates to:
  /// **'Open AI providers'**
  String get chatV2ModelPickerEmptyAction;

  /// No description provided for @chatV2ModelPickerNoMatch.
  ///
  /// In en, this message translates to:
  /// **'No models match'**
  String get chatV2ModelPickerNoMatch;

  /// No description provided for @chatV2ReasoningStreaming.
  ///
  /// In en, this message translates to:
  /// **'Thinking…'**
  String get chatV2ReasoningStreaming;

  /// No description provided for @chatV2ReasoningClosed.
  ///
  /// In en, this message translates to:
  /// **'Reasoning'**
  String get chatV2ReasoningClosed;

  /// No description provided for @chatV2ReasoningExpand.
  ///
  /// In en, this message translates to:
  /// **'Expand'**
  String get chatV2ReasoningExpand;

  /// No description provided for @chatV2ReasoningCollapse.
  ///
  /// In en, this message translates to:
  /// **'Collapse'**
  String get chatV2ReasoningCollapse;

  /// No description provided for @chatV2ComposerAttachNoVision.
  ///
  /// In en, this message translates to:
  /// **'Current model does not support image input — switch to a vision-capable model'**
  String get chatV2ComposerAttachNoVision;

  /// No description provided for @chatV2CtxBarTooltip.
  ///
  /// In en, this message translates to:
  /// **'Context: {used} / {total} tokens · {pct}%'**
  String chatV2CtxBarTooltip(Object pct, Object total, Object used);

  /// No description provided for @wikiTitle.
  ///
  /// In en, this message translates to:
  /// **'Wiki'**
  String get wikiTitle;

  /// No description provided for @wikiNoCreds.
  ///
  /// In en, this message translates to:
  /// **'No model-relay credentials configured.'**
  String get wikiNoCreds;

  /// No description provided for @wikiOpenSettings.
  ///
  /// In en, this message translates to:
  /// **'Open Settings'**
  String get wikiOpenSettings;

  /// No description provided for @wikiNoProjects.
  ///
  /// In en, this message translates to:
  /// **'No projects yet — create one to start.'**
  String get wikiNoProjects;

  /// No description provided for @wikiCreateProject.
  ///
  /// In en, this message translates to:
  /// **'Create project'**
  String get wikiCreateProject;

  /// No description provided for @wikiNewPageButton.
  ///
  /// In en, this message translates to:
  /// **'New page'**
  String get wikiNewPageButton;

  /// No description provided for @wikiNewPageDialogTitle.
  ///
  /// In en, this message translates to:
  /// **'New page'**
  String get wikiNewPageDialogTitle;

  /// No description provided for @wikiSelectPageHint.
  ///
  /// In en, this message translates to:
  /// **'Select a page or create one to start.'**
  String get wikiSelectPageHint;

  /// No description provided for @graphTitle.
  ///
  /// In en, this message translates to:
  /// **'Graph'**
  String get graphTitle;

  /// No description provided for @graphErrorPrefix.
  ///
  /// In en, this message translates to:
  /// **'Graph error: {message}'**
  String graphErrorPrefix(Object message);

  /// No description provided for @graphAliasesLabel.
  ///
  /// In en, this message translates to:
  /// **'Aliases'**
  String get graphAliasesLabel;

  /// No description provided for @graphSummaryLabel.
  ///
  /// In en, this message translates to:
  /// **'Summary'**
  String get graphSummaryLabel;

  /// No description provided for @graphPathLabel.
  ///
  /// In en, this message translates to:
  /// **'Path'**
  String get graphPathLabel;

  /// No description provided for @commonError.
  ///
  /// In en, this message translates to:
  /// **'Error: {message}'**
  String commonError(Object message);

  /// No description provided for @commonNotFound.
  ///
  /// In en, this message translates to:
  /// **'Not found.'**
  String get commonNotFound;

  /// No description provided for @commonOk.
  ///
  /// In en, this message translates to:
  /// **'OK'**
  String get commonOk;

  /// No description provided for @commonCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get commonCancel;

  /// No description provided for @commonDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get commonDelete;

  /// No description provided for @commonCreate.
  ///
  /// In en, this message translates to:
  /// **'Create'**
  String get commonCreate;

  /// No description provided for @commonRetry.
  ///
  /// In en, this message translates to:
  /// **'Retry'**
  String get commonRetry;

  /// No description provided for @navAdmin.
  ///
  /// In en, this message translates to:
  /// **'Admin'**
  String get navAdmin;

  /// No description provided for @adminTitle.
  ///
  /// In en, this message translates to:
  /// **'Admin'**
  String get adminTitle;

  /// No description provided for @adminTabUsers.
  ///
  /// In en, this message translates to:
  /// **'Users'**
  String get adminTabUsers;

  /// No description provided for @adminTabAudit.
  ///
  /// In en, this message translates to:
  /// **'Audit log'**
  String get adminTabAudit;

  /// No description provided for @adminSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search by email or id…'**
  String get adminSearchHint;

  /// No description provided for @adminEmptyUsers.
  ///
  /// In en, this message translates to:
  /// **'No users match this filter.'**
  String get adminEmptyUsers;

  /// No description provided for @adminEmptyAudit.
  ///
  /// In en, this message translates to:
  /// **'No audit events yet.'**
  String get adminEmptyAudit;

  /// No description provided for @adminColEmail.
  ///
  /// In en, this message translates to:
  /// **'Email'**
  String get adminColEmail;

  /// No description provided for @adminColPlan.
  ///
  /// In en, this message translates to:
  /// **'Plan'**
  String get adminColPlan;

  /// No description provided for @adminColCreated.
  ///
  /// In en, this message translates to:
  /// **'Joined'**
  String get adminColCreated;

  /// No description provided for @adminUserDetails.
  ///
  /// In en, this message translates to:
  /// **'User details'**
  String get adminUserDetails;

  /// No description provided for @adminLimitsTitle.
  ///
  /// In en, this message translates to:
  /// **'Plan limits'**
  String get adminLimitsTitle;

  /// No description provided for @adminFieldRPM.
  ///
  /// In en, this message translates to:
  /// **'model-relay RPM'**
  String get adminFieldRPM;

  /// No description provided for @adminFieldTPM.
  ///
  /// In en, this message translates to:
  /// **'model-relay TPM'**
  String get adminFieldTPM;

  /// No description provided for @adminFieldSandboxDaily.
  ///
  /// In en, this message translates to:
  /// **'Sandbox / day'**
  String get adminFieldSandboxDaily;

  /// No description provided for @adminFieldSandboxConcurrent.
  ///
  /// In en, this message translates to:
  /// **'Sandbox concurrent'**
  String get adminFieldSandboxConcurrent;

  /// No description provided for @adminFieldMemoryQuota.
  ///
  /// In en, this message translates to:
  /// **'Memories / project'**
  String get adminFieldMemoryQuota;

  /// No description provided for @adminFieldBrainProjects.
  ///
  /// In en, this message translates to:
  /// **'Projects / user'**
  String get adminFieldBrainProjects;

  /// No description provided for @adminChangePlan.
  ///
  /// In en, this message translates to:
  /// **'Change plan'**
  String get adminChangePlan;

  /// No description provided for @adminPlanReason.
  ///
  /// In en, this message translates to:
  /// **'Reason (audit log)'**
  String get adminPlanReason;

  /// No description provided for @adminPlanApply.
  ///
  /// In en, this message translates to:
  /// **'Apply'**
  String get adminPlanApply;

  /// No description provided for @adminPlanApplied.
  ///
  /// In en, this message translates to:
  /// **'Plan updated'**
  String get adminPlanApplied;

  /// No description provided for @adminAuditAt.
  ///
  /// In en, this message translates to:
  /// **'When'**
  String get adminAuditAt;

  /// No description provided for @adminAuditActor.
  ///
  /// In en, this message translates to:
  /// **'Actor'**
  String get adminAuditActor;

  /// No description provided for @adminAuditAction.
  ///
  /// In en, this message translates to:
  /// **'Action'**
  String get adminAuditAction;

  /// No description provided for @adminAuditTarget.
  ///
  /// In en, this message translates to:
  /// **'Target'**
  String get adminAuditTarget;

  /// No description provided for @adminAuditDetail.
  ///
  /// In en, this message translates to:
  /// **'Detail'**
  String get adminAuditDetail;

  /// No description provided for @appsTitle.
  ///
  /// In en, this message translates to:
  /// **'App Center'**
  String get appsTitle;

  /// No description provided for @appsManage.
  ///
  /// In en, this message translates to:
  /// **'Manage'**
  String get appsManage;

  /// No description provided for @appsManageTitle.
  ///
  /// In en, this message translates to:
  /// **'App management'**
  String get appsManageTitle;

  /// No description provided for @appsRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get appsRefresh;

  /// No description provided for @appsSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search apps'**
  String get appsSearchHint;

  /// No description provided for @appsEmpty.
  ///
  /// In en, this message translates to:
  /// **'No apps match your filters.'**
  String get appsEmpty;

  /// No description provided for @appsNoInstalls.
  ///
  /// In en, this message translates to:
  /// **'No apps installed yet — pick one from the App Center.'**
  String get appsNoInstalls;

  /// No description provided for @appsInstall.
  ///
  /// In en, this message translates to:
  /// **'Install'**
  String get appsInstall;

  /// No description provided for @appsUninstall.
  ///
  /// In en, this message translates to:
  /// **'Uninstall'**
  String get appsUninstall;

  /// No description provided for @appsOpen.
  ///
  /// In en, this message translates to:
  /// **'Open'**
  String get appsOpen;

  /// No description provided for @appsCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get appsCancel;

  /// No description provided for @appsInstalled.
  ///
  /// In en, this message translates to:
  /// **'Installed'**
  String get appsInstalled;

  /// No description provided for @appsCategoryAll.
  ///
  /// In en, this message translates to:
  /// **'All'**
  String get appsCategoryAll;

  /// No description provided for @appsCategoryInstalled.
  ///
  /// In en, this message translates to:
  /// **'Installed ({count})'**
  String appsCategoryInstalled(Object count);

  /// No description provided for @appsCategoryProductivity.
  ///
  /// In en, this message translates to:
  /// **'Productivity'**
  String get appsCategoryProductivity;

  /// No description provided for @appsCategoryContent.
  ///
  /// In en, this message translates to:
  /// **'Content'**
  String get appsCategoryContent;

  /// No description provided for @appsCategoryData.
  ///
  /// In en, this message translates to:
  /// **'Data'**
  String get appsCategoryData;

  /// No description provided for @appsCategoryComm.
  ///
  /// In en, this message translates to:
  /// **'Comm'**
  String get appsCategoryComm;

  /// No description provided for @appsCategoryDev.
  ///
  /// In en, this message translates to:
  /// **'Dev'**
  String get appsCategoryDev;

  /// No description provided for @appsCategoryUtility.
  ///
  /// In en, this message translates to:
  /// **'Utility'**
  String get appsCategoryUtility;

  /// No description provided for @appsConfigureFirst.
  ///
  /// In en, this message translates to:
  /// **'Configure BiuMind model-relay credentials in Settings first to unlock the App Center.'**
  String get appsConfigureFirst;

  /// No description provided for @appsInstallTitle.
  ///
  /// In en, this message translates to:
  /// **'Install {name} v{version}'**
  String appsInstallTitle(Object name, Object version);

  /// No description provided for @appsNoPermissionRequested.
  ///
  /// In en, this message translates to:
  /// **'This app requests no permissions.'**
  String get appsNoPermissionRequested;

  /// No description provided for @appsInstalledToast.
  ///
  /// In en, this message translates to:
  /// **'{name} installed.'**
  String appsInstalledToast(Object name);

  /// No description provided for @appsUninstalledToast.
  ///
  /// In en, this message translates to:
  /// **'{name} uninstalled.'**
  String appsUninstalledToast(Object name);

  /// No description provided for @appsUninstallTitle.
  ///
  /// In en, this message translates to:
  /// **'Uninstall app?'**
  String get appsUninstallTitle;

  /// No description provided for @appsUninstallConfirm.
  ///
  /// In en, this message translates to:
  /// **'Uninstall {identifier}? Data created by the app is retained; clean it up separately from the management page.'**
  String appsUninstallConfirm(Object identifier);

  /// No description provided for @appsSectionPermissions.
  ///
  /// In en, this message translates to:
  /// **'Permissions'**
  String get appsSectionPermissions;

  /// No description provided for @appsSectionViews.
  ///
  /// In en, this message translates to:
  /// **'Views'**
  String get appsSectionViews;

  /// No description provided for @appsSectionTriggers.
  ///
  /// In en, this message translates to:
  /// **'Triggers'**
  String get appsSectionTriggers;

  /// No description provided for @appsSectionSkills.
  ///
  /// In en, this message translates to:
  /// **'Bundled skills'**
  String get appsSectionSkills;

  /// No description provided for @appsErrNetwork.
  ///
  /// In en, this message translates to:
  /// **'Network error. Check your connection and try again.'**
  String get appsErrNetwork;

  /// No description provided for @appsErrUnauthorized.
  ///
  /// In en, this message translates to:
  /// **'Session expired. Please sign in again.'**
  String get appsErrUnauthorized;

  /// No description provided for @appsErrNotInstalled.
  ///
  /// In en, this message translates to:
  /// **'Install the app first to invoke its actions.'**
  String get appsErrNotInstalled;

  /// No description provided for @appsErrInstallDisabled.
  ///
  /// In en, this message translates to:
  /// **'This app is currently disabled — enable it from app settings.'**
  String get appsErrInstallDisabled;

  /// No description provided for @appsErrNotFound.
  ///
  /// In en, this message translates to:
  /// **'The target no longer exists.'**
  String get appsErrNotFound;

  /// No description provided for @appsErrConflict.
  ///
  /// In en, this message translates to:
  /// **'Conflict with the latest state — refresh and retry.'**
  String get appsErrConflict;

  /// No description provided for @appsErrValidation.
  ///
  /// In en, this message translates to:
  /// **'Invalid request: {detail}'**
  String appsErrValidation(Object detail);

  /// No description provided for @appsErrRateLimit.
  ///
  /// In en, this message translates to:
  /// **'Too many requests. Please try again shortly.'**
  String get appsErrRateLimit;

  /// No description provided for @appsErrServer.
  ///
  /// In en, this message translates to:
  /// **'Service temporarily unavailable ({status}). Please retry.'**
  String appsErrServer(Object status);

  /// No description provided for @appsErrUnknown.
  ///
  /// In en, this message translates to:
  /// **'Operation failed: {detail}'**
  String appsErrUnknown(Object detail);

  /// No description provided for @permNetOutbound.
  ///
  /// In en, this message translates to:
  /// **'Outbound network access. Limited to domains listed in the manifest.'**
  String get permNetOutbound;

  /// No description provided for @permHubInvoke.
  ///
  /// In en, this message translates to:
  /// **'Call language models (counts against your model-relay quota).'**
  String get permHubInvoke;

  /// No description provided for @permGraphRead.
  ///
  /// In en, this message translates to:
  /// **'Read knowledge-graph nodes / edges.'**
  String get permGraphRead;

  /// No description provided for @permGraphWrite.
  ///
  /// In en, this message translates to:
  /// **'Write knowledge-graph nodes / edges.'**
  String get permGraphWrite;

  /// No description provided for @permMemoryRead.
  ///
  /// In en, this message translates to:
  /// **'Read your multi-tier memory.'**
  String get permMemoryRead;

  /// No description provided for @permMemoryWrite.
  ///
  /// In en, this message translates to:
  /// **'Write to your multi-tier memory.'**
  String get permMemoryWrite;

  /// No description provided for @permFilesRead.
  ///
  /// In en, this message translates to:
  /// **'Read Files content scoped to this app.'**
  String get permFilesRead;

  /// No description provided for @permFilesWrite.
  ///
  /// In en, this message translates to:
  /// **'Write Files content scoped to this app.'**
  String get permFilesWrite;

  /// No description provided for @permCronRegister.
  ///
  /// In en, this message translates to:
  /// **'Register scheduled jobs (cron).'**
  String get permCronRegister;

  /// No description provided for @permWebhookRegister.
  ///
  /// In en, this message translates to:
  /// **'Register webhook receiver paths.'**
  String get permWebhookRegister;

  /// No description provided for @permNotifySend.
  ///
  /// In en, this message translates to:
  /// **'Send notifications to you.'**
  String get permNotifySend;

  /// No description provided for @permSandboxExec.
  ///
  /// In en, this message translates to:
  /// **'Execute commands in an isolated sandbox (high risk).'**
  String get permSandboxExec;

  /// No description provided for @permOauth.
  ///
  /// In en, this message translates to:
  /// **'Sign in to third-party accounts via OAuth.'**
  String get permOauth;

  /// No description provided for @permSecretsRead.
  ///
  /// In en, this message translates to:
  /// **'Read vault credentials (high risk, enterprise only).'**
  String get permSecretsRead;

  /// No description provided for @sidebarCustomizeTitle.
  ///
  /// In en, this message translates to:
  /// **'Customize sidebar'**
  String get sidebarCustomizeTitle;

  /// No description provided for @sidebarCollapse.
  ///
  /// In en, this message translates to:
  /// **'Collapse sidebar'**
  String get sidebarCollapse;

  /// No description provided for @sidebarExpand.
  ///
  /// In en, this message translates to:
  /// **'Expand sidebar'**
  String get sidebarExpand;

  /// No description provided for @sidebarModeHidden.
  ///
  /// In en, this message translates to:
  /// **'Hide sidebar'**
  String get sidebarModeHidden;

  /// No description provided for @sidebarModeIconsOnly.
  ///
  /// In en, this message translates to:
  /// **'Icons only'**
  String get sidebarModeIconsOnly;

  /// No description provided for @sidebarModeExpanded.
  ///
  /// In en, this message translates to:
  /// **'Icons and text'**
  String get sidebarModeExpanded;

  /// No description provided for @sidebarRestoreDefaults.
  ///
  /// In en, this message translates to:
  /// **'Restore defaults'**
  String get sidebarRestoreDefaults;

  /// No description provided for @sidebarSave.
  ///
  /// In en, this message translates to:
  /// **'Save'**
  String get sidebarSave;

  /// No description provided for @sidebarSaving.
  ///
  /// In en, this message translates to:
  /// **'Saving…'**
  String get sidebarSaving;

  /// No description provided for @sidebarSaved.
  ///
  /// In en, this message translates to:
  /// **'Sidebar saved.'**
  String get sidebarSaved;

  /// No description provided for @sidebarConflict.
  ///
  /// In en, this message translates to:
  /// **'Another device updated this sidebar — reloaded latest.'**
  String get sidebarConflict;

  /// No description provided for @sidebarSectionSystem.
  ///
  /// In en, this message translates to:
  /// **'System (toggle visibility)'**
  String get sidebarSectionSystem;

  /// No description provided for @sidebarSectionPinned.
  ///
  /// In en, this message translates to:
  /// **'Pinned apps'**
  String get sidebarSectionPinned;

  /// No description provided for @sidebarSectionAvailable.
  ///
  /// In en, this message translates to:
  /// **'Installed apps you can pin'**
  String get sidebarSectionAvailable;

  /// No description provided for @sidebarHidden.
  ///
  /// In en, this message translates to:
  /// **'Hidden from sidebar'**
  String get sidebarHidden;

  /// No description provided for @sidebarPin.
  ///
  /// In en, this message translates to:
  /// **'Pin'**
  String get sidebarPin;

  /// No description provided for @sidebarPinnedEmpty.
  ///
  /// In en, this message translates to:
  /// **'No apps pinned yet — pin one from the list below.'**
  String get sidebarPinnedEmpty;

  /// No description provided for @sidebarPinnedOrphan.
  ///
  /// In en, this message translates to:
  /// **'App no longer installed — remove this entry.'**
  String get sidebarPinnedOrphan;

  /// No description provided for @sidebarMoveUp.
  ///
  /// In en, this message translates to:
  /// **'Move up'**
  String get sidebarMoveUp;

  /// No description provided for @sidebarMoveDown.
  ///
  /// In en, this message translates to:
  /// **'Move down'**
  String get sidebarMoveDown;

  /// No description provided for @sidebarRemove.
  ///
  /// In en, this message translates to:
  /// **'Remove'**
  String get sidebarRemove;

  /// No description provided for @sidebarPinAction.
  ///
  /// In en, this message translates to:
  /// **'Pin to sidebar'**
  String get sidebarPinAction;

  /// No description provided for @sidebarUnpinAction.
  ///
  /// In en, this message translates to:
  /// **'Unpin from sidebar'**
  String get sidebarUnpinAction;

  /// No description provided for @sidebarCustomizeAction.
  ///
  /// In en, this message translates to:
  /// **'Customize sidebar…'**
  String get sidebarCustomizeAction;

  /// No description provided for @sidebarPinnedToast.
  ///
  /// In en, this message translates to:
  /// **'Pinned to sidebar.'**
  String get sidebarPinnedToast;

  /// No description provided for @sidebarUnpinnedToast.
  ///
  /// In en, this message translates to:
  /// **'Removed from sidebar.'**
  String get sidebarUnpinnedToast;

  /// No description provided for @sidebarPinNeedsInstall.
  ///
  /// In en, this message translates to:
  /// **'Install the app first to pin it.'**
  String get sidebarPinNeedsInstall;

  /// No description provided for @sidebarPinSuggestionAction.
  ///
  /// In en, this message translates to:
  /// **'Add to sidebar'**
  String get sidebarPinSuggestionAction;

  /// No description provided for @sidebarPinSuggestionDismiss.
  ///
  /// In en, this message translates to:
  /// **'Not now'**
  String get sidebarPinSuggestionDismiss;

  /// No description provided for @sidebarQueuedOffline.
  ///
  /// In en, this message translates to:
  /// **'Network unreachable — edit queued, will sync on reconnect.'**
  String get sidebarQueuedOffline;

  /// No description provided for @sidebarOutboxBanner.
  ///
  /// In en, this message translates to:
  /// **'Sidebar edit pending sync (offline).'**
  String get sidebarOutboxBanner;

  /// No description provided for @upgradeTitle.
  ///
  /// In en, this message translates to:
  /// **'Upgrade {name}: v{from} → v{to}'**
  String upgradeTitle(Object from, Object name, Object to);

  /// No description provided for @upgradeNoNewPerms.
  ///
  /// In en, this message translates to:
  /// **'No new permissions required — safe to upgrade.'**
  String get upgradeNoNewPerms;

  /// No description provided for @upgradeNeedsApproval.
  ///
  /// In en, this message translates to:
  /// **'This upgrade requests new permissions. Review and check each one before applying.'**
  String get upgradeNeedsApproval;

  /// No description provided for @upgradeSectionAdded.
  ///
  /// In en, this message translates to:
  /// **'New permissions'**
  String get upgradeSectionAdded;

  /// No description provided for @upgradeSectionRemoved.
  ///
  /// In en, this message translates to:
  /// **'No longer requested'**
  String get upgradeSectionRemoved;

  /// No description provided for @upgradeSectionUnchanged.
  ///
  /// In en, this message translates to:
  /// **'Already granted'**
  String get upgradeSectionUnchanged;

  /// No description provided for @upgradeCancel.
  ///
  /// In en, this message translates to:
  /// **'Not now'**
  String get upgradeCancel;

  /// No description provided for @upgradeApply.
  ///
  /// In en, this message translates to:
  /// **'Upgrade'**
  String get upgradeApply;

  /// No description provided for @upgradeBannerTitle.
  ///
  /// In en, this message translates to:
  /// **'Upgrades available'**
  String get upgradeBannerTitle;

  /// No description provided for @upgradeBannerSubtitle.
  ///
  /// In en, this message translates to:
  /// **'{count} app(s) have a newer version waiting'**
  String upgradeBannerSubtitle(Object count);

  /// No description provided for @upgradeRowVersion.
  ///
  /// In en, this message translates to:
  /// **'v{from} → v{to}'**
  String upgradeRowVersion(Object from, Object to);

  /// No description provided for @upgradeAvailable.
  ///
  /// In en, this message translates to:
  /// **'Upgrade available'**
  String get upgradeAvailable;

  /// No description provided for @upgradeAppliedToast.
  ///
  /// In en, this message translates to:
  /// **'Upgraded.'**
  String get upgradeAppliedToast;

  /// No description provided for @repoUpgradeConfirmTitle.
  ///
  /// In en, this message translates to:
  /// **'Update GitHub app'**
  String get repoUpgradeConfirmTitle;

  /// No description provided for @repoUpgradeConfirmBody.
  ///
  /// In en, this message translates to:
  /// **'Will update to {version}. The app will be briefly unavailable while updating.'**
  String repoUpgradeConfirmBody(Object version);

  /// No description provided for @repoUpgradeLatestVersion.
  ///
  /// In en, this message translates to:
  /// **'the latest version'**
  String get repoUpgradeLatestVersion;

  /// No description provided for @repoUpgradeUnsupportedPlatform.
  ///
  /// In en, this message translates to:
  /// **'Updating GitHub apps is not supported on this platform (available in the macOS / Linux client).'**
  String get repoUpgradeUnsupportedPlatform;

  /// No description provided for @repoUpgradeBadRepoUrl.
  ///
  /// In en, this message translates to:
  /// **'Cannot derive the app name from the repo URL: {url}'**
  String repoUpgradeBadRepoUrl(Object url);

  /// No description provided for @heroGreetingMorning.
  ///
  /// In en, this message translates to:
  /// **'Good morning'**
  String get heroGreetingMorning;

  /// No description provided for @heroGreetingNoon.
  ///
  /// In en, this message translates to:
  /// **'Good day'**
  String get heroGreetingNoon;

  /// No description provided for @heroGreetingAfternoon.
  ///
  /// In en, this message translates to:
  /// **'Good afternoon'**
  String get heroGreetingAfternoon;

  /// No description provided for @heroGreetingEvening.
  ///
  /// In en, this message translates to:
  /// **'Good evening'**
  String get heroGreetingEvening;

  /// No description provided for @heroGreetingNight.
  ///
  /// In en, this message translates to:
  /// **'Still up?'**
  String get heroGreetingNight;

  /// No description provided for @heroSubtitleNoThread.
  ///
  /// In en, this message translates to:
  /// **'What would you like to chat about today?'**
  String get heroSubtitleNoThread;

  /// No description provided for @heroSubtitleEmptyThread.
  ///
  /// In en, this message translates to:
  /// **'Start your conversation'**
  String get heroSubtitleEmptyThread;

  /// No description provided for @heroRecentSection.
  ///
  /// In en, this message translates to:
  /// **'Recent conversations'**
  String get heroRecentSection;

  /// No description provided for @heroRecentEmpty.
  ///
  /// In en, this message translates to:
  /// **'No conversations yet'**
  String get heroRecentEmpty;

  /// No description provided for @heroRelativeMinutes.
  ///
  /// In en, this message translates to:
  /// **'{n}m ago'**
  String heroRelativeMinutes(Object n);

  /// No description provided for @heroRelativeHours.
  ///
  /// In en, this message translates to:
  /// **'{n}h ago'**
  String heroRelativeHours(Object n);

  /// No description provided for @heroRelativeDays.
  ///
  /// In en, this message translates to:
  /// **'{n}d ago'**
  String heroRelativeDays(Object n);

  /// No description provided for @heroRelativeJustNow.
  ///
  /// In en, this message translates to:
  /// **'Just now'**
  String get heroRelativeJustNow;

  /// No description provided for @heroCurrentModel.
  ///
  /// In en, this message translates to:
  /// **'Current model: {model}'**
  String heroCurrentModel(Object model);

  /// No description provided for @heroSignInBanner.
  ///
  /// In en, this message translates to:
  /// **'Not signed in — tap to go to login'**
  String get heroSignInBanner;

  /// No description provided for @starterPromptWritingTitle.
  ///
  /// In en, this message translates to:
  /// **'Writing assistant'**
  String get starterPromptWritingTitle;

  /// No description provided for @starterPromptWritingHint.
  ///
  /// In en, this message translates to:
  /// **'Polish a piece of text'**
  String get starterPromptWritingHint;

  /// No description provided for @starterPromptWritingPrompt.
  ///
  /// In en, this message translates to:
  /// **'Please polish the following text to make it more professional:\n\n'**
  String get starterPromptWritingPrompt;

  /// No description provided for @starterPromptCodeTitle.
  ///
  /// In en, this message translates to:
  /// **'Code review'**
  String get starterPromptCodeTitle;

  /// No description provided for @starterPromptCodeHint.
  ///
  /// In en, this message translates to:
  /// **'Review the code I paste'**
  String get starterPromptCodeHint;

  /// No description provided for @starterPromptCodePrompt.
  ///
  /// In en, this message translates to:
  /// **'Please review the following code and point out improvements:\n\n```\n\n```'**
  String get starterPromptCodePrompt;

  /// No description provided for @starterPromptResearchTitle.
  ///
  /// In en, this message translates to:
  /// **'Deep research'**
  String get starterPromptResearchTitle;

  /// No description provided for @starterPromptResearchHint.
  ///
  /// In en, this message translates to:
  /// **'Expand on a topic'**
  String get starterPromptResearchHint;

  /// No description provided for @starterPromptResearchPrompt.
  ///
  /// In en, this message translates to:
  /// **'Please analyze the following topic in depth, offering multiple perspectives:\n\n'**
  String get starterPromptResearchPrompt;

  /// No description provided for @starterPromptTranslateTitle.
  ///
  /// In en, this message translates to:
  /// **'Translate'**
  String get starterPromptTranslateTitle;

  /// No description provided for @starterPromptTranslateHint.
  ///
  /// In en, this message translates to:
  /// **'EN ⇄ ZH'**
  String get starterPromptTranslateHint;

  /// No description provided for @starterPromptTranslatePrompt.
  ///
  /// In en, this message translates to:
  /// **'Please translate the following content while preserving meaning and tone:\n\n'**
  String get starterPromptTranslatePrompt;

  /// No description provided for @starterPromptDataTitle.
  ///
  /// In en, this message translates to:
  /// **'Data analysis'**
  String get starterPromptDataTitle;

  /// No description provided for @starterPromptDataHint.
  ///
  /// In en, this message translates to:
  /// **'Analyze data'**
  String get starterPromptDataHint;

  /// No description provided for @starterPromptDataPrompt.
  ///
  /// In en, this message translates to:
  /// **'Please analyze the following data and surface key insights:\n\n'**
  String get starterPromptDataPrompt;

  /// No description provided for @starterPromptIdeasTitle.
  ///
  /// In en, this message translates to:
  /// **'Brainstorm'**
  String get starterPromptIdeasTitle;

  /// No description provided for @starterPromptIdeasHint.
  ///
  /// In en, this message translates to:
  /// **'Generate ideas'**
  String get starterPromptIdeasHint;

  /// No description provided for @starterPromptIdeasPrompt.
  ///
  /// In en, this message translates to:
  /// **'Please give me 10 creative ideas around the following topic:\n\n'**
  String get starterPromptIdeasPrompt;

  /// No description provided for @navCreation.
  ///
  /// In en, this message translates to:
  /// **'Create'**
  String get navCreation;

  /// No description provided for @navProfile.
  ///
  /// In en, this message translates to:
  /// **'Me'**
  String get navProfile;

  /// No description provided for @creationInspiration.
  ///
  /// In en, this message translates to:
  /// **'Inspiration'**
  String get creationInspiration;

  /// No description provided for @creationStudio.
  ///
  /// In en, this message translates to:
  /// **'Studio'**
  String get creationStudio;

  /// No description provided for @creationWorks.
  ///
  /// In en, this message translates to:
  /// **'My Works'**
  String get creationWorks;

  /// No description provided for @creationGallery.
  ///
  /// In en, this message translates to:
  /// **'Gallery'**
  String get creationGallery;

  /// No description provided for @creationRecharge.
  ///
  /// In en, this message translates to:
  /// **'Credits'**
  String get creationRecharge;

  /// No description provided for @creationHeroTitle.
  ///
  /// In en, this message translates to:
  /// **'Create'**
  String get creationHeroTitle;

  /// No description provided for @creationHeroSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Multimodal AIGC engine — make ideas tangible'**
  String get creationHeroSubtitle;

  /// No description provided for @creationTabImage.
  ///
  /// In en, this message translates to:
  /// **'Image'**
  String get creationTabImage;

  /// No description provided for @creationTabVideo.
  ///
  /// In en, this message translates to:
  /// **'Video'**
  String get creationTabVideo;

  /// No description provided for @creationTabDigitalHuman.
  ///
  /// In en, this message translates to:
  /// **'Digital Human'**
  String get creationTabDigitalHuman;

  /// No description provided for @creationTabHotparse.
  ///
  /// In en, this message translates to:
  /// **'Hot Parse'**
  String get creationTabHotparse;

  /// No description provided for @creationPromptHint.
  ///
  /// In en, this message translates to:
  /// **'Describe what you want to generate...'**
  String get creationPromptHint;

  /// No description provided for @creationFirstFrame.
  ///
  /// In en, this message translates to:
  /// **'First Frame'**
  String get creationFirstFrame;

  /// No description provided for @creationLastFrame.
  ///
  /// In en, this message translates to:
  /// **'Last Frame'**
  String get creationLastFrame;

  /// No description provided for @creationReferenceImage.
  ///
  /// In en, this message translates to:
  /// **'Reference'**
  String get creationReferenceImage;

  /// No description provided for @creationAiOptimize.
  ///
  /// In en, this message translates to:
  /// **'AI Optimize'**
  String get creationAiOptimize;

  /// No description provided for @creationSharePublic.
  ///
  /// In en, this message translates to:
  /// **'Share Publicly'**
  String get creationSharePublic;

  /// No description provided for @creationSubmit.
  ///
  /// In en, this message translates to:
  /// **'Generate'**
  String get creationSubmit;

  /// No description provided for @creationCardPending.
  ///
  /// In en, this message translates to:
  /// **'Pending'**
  String get creationCardPending;

  /// No description provided for @creationCardQueued.
  ///
  /// In en, this message translates to:
  /// **'Queued'**
  String get creationCardQueued;

  /// No description provided for @creationCardRunning.
  ///
  /// In en, this message translates to:
  /// **'Generating'**
  String get creationCardRunning;

  /// No description provided for @creationCardCompleted.
  ///
  /// In en, this message translates to:
  /// **'Completed'**
  String get creationCardCompleted;

  /// No description provided for @creationCardFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed'**
  String get creationCardFailed;

  /// No description provided for @creationCardBlocked.
  ///
  /// In en, this message translates to:
  /// **'Blocked by moderation'**
  String get creationCardBlocked;

  /// No description provided for @creationCardCancelled.
  ///
  /// In en, this message translates to:
  /// **'Cancelled'**
  String get creationCardCancelled;

  /// No description provided for @creationActionRetry.
  ///
  /// In en, this message translates to:
  /// **'Retry'**
  String get creationActionRetry;

  /// No description provided for @creationActionRedo.
  ///
  /// In en, this message translates to:
  /// **'Regenerate'**
  String get creationActionRedo;

  /// No description provided for @creationActionEdit.
  ///
  /// In en, this message translates to:
  /// **'Edit'**
  String get creationActionEdit;

  /// No description provided for @creationActionDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get creationActionDelete;

  /// No description provided for @creationActionDownload.
  ///
  /// In en, this message translates to:
  /// **'Download'**
  String get creationActionDownload;

  /// No description provided for @creationActionMakeSimilar.
  ///
  /// In en, this message translates to:
  /// **'Make Similar'**
  String get creationActionMakeSimilar;

  /// No description provided for @creationActionShare.
  ///
  /// In en, this message translates to:
  /// **'Share'**
  String get creationActionShare;

  /// No description provided for @creationActionPublic.
  ///
  /// In en, this message translates to:
  /// **'Make Public'**
  String get creationActionPublic;

  /// No description provided for @creationActionPrivate.
  ///
  /// In en, this message translates to:
  /// **'Make Private'**
  String get creationActionPrivate;

  /// No description provided for @creationActionCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get creationActionCancel;

  /// No description provided for @creationCreditCost.
  ///
  /// In en, this message translates to:
  /// **'{n} credits'**
  String creationCreditCost(Object n);

  /// No description provided for @creationCreditRefunded.
  ///
  /// In en, this message translates to:
  /// **'Refunded {n} credits'**
  String creationCreditRefunded(Object n);

  /// No description provided for @creationCreditInsufficient.
  ///
  /// In en, this message translates to:
  /// **'Not enough credits — top up to continue'**
  String get creationCreditInsufficient;

  /// No description provided for @creationErrorEmptyPrompt.
  ///
  /// In en, this message translates to:
  /// **'Please describe what you want to generate'**
  String get creationErrorEmptyPrompt;

  /// No description provided for @creationErrorModelNotFound.
  ///
  /// In en, this message translates to:
  /// **'Model not available'**
  String get creationErrorModelNotFound;

  /// No description provided for @creationOfflineBanner.
  ///
  /// In en, this message translates to:
  /// **'Offline — sync paused'**
  String get creationOfflineBanner;

  /// No description provided for @membershipCenterTitle.
  ///
  /// In en, this message translates to:
  /// **'Membership'**
  String get membershipCenterTitle;

  /// No description provided for @membershipCurrentPlan.
  ///
  /// In en, this message translates to:
  /// **'Current plan'**
  String get membershipCurrentPlan;

  /// No description provided for @membershipChoosePlan.
  ///
  /// In en, this message translates to:
  /// **'Choose a plan'**
  String get membershipChoosePlan;

  /// No description provided for @membershipPlanCompareTitle.
  ///
  /// In en, this message translates to:
  /// **'Compare plans'**
  String get membershipPlanCompareTitle;

  /// No description provided for @membershipOrdersTitle.
  ///
  /// In en, this message translates to:
  /// **'Order history'**
  String get membershipOrdersTitle;

  /// No description provided for @membershipOrdersEmpty.
  ///
  /// In en, this message translates to:
  /// **'No orders yet'**
  String get membershipOrdersEmpty;

  /// No description provided for @membershipCheckoutTitle.
  ///
  /// In en, this message translates to:
  /// **'Checkout'**
  String get membershipCheckoutTitle;

  /// No description provided for @membershipNotLoggedIn.
  ///
  /// In en, this message translates to:
  /// **'Sign in to view membership status'**
  String get membershipNotLoggedIn;

  /// No description provided for @membershipBadgeCurrent.
  ///
  /// In en, this message translates to:
  /// **'Current'**
  String get membershipBadgeCurrent;

  /// No description provided for @membershipCtaSelect.
  ///
  /// In en, this message translates to:
  /// **'Select'**
  String get membershipCtaSelect;

  /// No description provided for @membershipCtaCurrent.
  ///
  /// In en, this message translates to:
  /// **'Current plan'**
  String get membershipCtaCurrent;

  /// No description provided for @membershipCtaUpgrade.
  ///
  /// In en, this message translates to:
  /// **'Upgrade'**
  String get membershipCtaUpgrade;

  /// No description provided for @membershipCtaDowngrade.
  ///
  /// In en, this message translates to:
  /// **'Downgrade'**
  String get membershipCtaDowngrade;

  /// No description provided for @membershipPriceFree.
  ///
  /// In en, this message translates to:
  /// **'Free'**
  String get membershipPriceFree;

  /// No description provided for @membershipPricePerMonth.
  ///
  /// In en, this message translates to:
  /// **'/ mo'**
  String get membershipPricePerMonth;

  /// No description provided for @membershipPricePerYear.
  ///
  /// In en, this message translates to:
  /// **'/ yr'**
  String get membershipPricePerYear;

  /// No description provided for @membershipQuotaChat.
  ///
  /// In en, this message translates to:
  /// **'Chat monthly quota'**
  String get membershipQuotaChat;

  /// No description provided for @membershipQuotaAIGC.
  ///
  /// In en, this message translates to:
  /// **'AIGC monthly quota'**
  String get membershipQuotaAIGC;

  /// No description provided for @membershipActionCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel subscription'**
  String get membershipActionCancel;

  /// No description provided for @membershipActionResume.
  ///
  /// In en, this message translates to:
  /// **'Resume subscription'**
  String get membershipActionResume;

  /// No description provided for @membershipResumed.
  ///
  /// In en, this message translates to:
  /// **'Subscription resumed'**
  String get membershipResumed;

  /// No description provided for @membershipCancelTitle.
  ///
  /// In en, this message translates to:
  /// **'Cancel subscription'**
  String get membershipCancelTitle;

  /// No description provided for @membershipCancelOptionPeriodEnd.
  ///
  /// In en, this message translates to:
  /// **'Cancel at period end'**
  String get membershipCancelOptionPeriodEnd;

  /// No description provided for @membershipCancelOptionImmediate.
  ///
  /// In en, this message translates to:
  /// **'Cancel now + prorated refund'**
  String get membershipCancelOptionImmediate;

  /// No description provided for @membershipCancelHint.
  ///
  /// In en, this message translates to:
  /// **'Tap \"Resume\" before period_end to undo cancellation'**
  String get membershipCancelHint;

  /// No description provided for @membershipCancelDeny.
  ///
  /// In en, this message translates to:
  /// **'Not now'**
  String get membershipCancelDeny;

  /// No description provided for @membershipCancelConfirm.
  ///
  /// In en, this message translates to:
  /// **'Confirm cancel'**
  String get membershipCancelConfirm;

  /// No description provided for @membershipCanceledImmediate.
  ///
  /// In en, this message translates to:
  /// **'Subscription canceled immediately'**
  String get membershipCanceledImmediate;

  /// No description provided for @membershipCanceledPeriodEnd.
  ///
  /// In en, this message translates to:
  /// **'Subscription will cancel at period end'**
  String get membershipCanceledPeriodEnd;

  /// No description provided for @membershipUpgradeImmediate.
  ///
  /// In en, this message translates to:
  /// **'Effective immediately, prorated'**
  String get membershipUpgradeImmediate;

  /// No description provided for @membershipUpgradeRefund.
  ///
  /// In en, this message translates to:
  /// **'Old plan unused credit'**
  String get membershipUpgradeRefund;

  /// No description provided for @membershipUpgradeNewCharge.
  ///
  /// In en, this message translates to:
  /// **'New plan prorated charge'**
  String get membershipUpgradeNewCharge;

  /// No description provided for @membershipUpgradeNetCharge.
  ///
  /// In en, this message translates to:
  /// **'Total due now'**
  String get membershipUpgradeNetCharge;

  /// No description provided for @membershipDowngradeAt.
  ///
  /// In en, this message translates to:
  /// **'Downgrade effective at period end'**
  String get membershipDowngradeAt;

  /// No description provided for @membershipUpgradeContinue.
  ///
  /// In en, this message translates to:
  /// **'Continue to pay'**
  String get membershipUpgradeContinue;

  /// No description provided for @membershipDowngradeConfirm.
  ///
  /// In en, this message translates to:
  /// **'Confirm downgrade'**
  String get membershipDowngradeConfirm;

  /// No description provided for @membershipPaymentMethodTitle.
  ///
  /// In en, this message translates to:
  /// **'Payment method'**
  String get membershipPaymentMethodTitle;

  /// No description provided for @membershipPaymentWechatNative.
  ///
  /// In en, this message translates to:
  /// **'WeChat Pay (QR)'**
  String get membershipPaymentWechatNative;

  /// No description provided for @membershipPaymentWechatH5.
  ///
  /// In en, this message translates to:
  /// **'WeChat Pay (H5)'**
  String get membershipPaymentWechatH5;

  /// No description provided for @membershipPaymentAlipayPC.
  ///
  /// In en, this message translates to:
  /// **'Alipay (Web)'**
  String get membershipPaymentAlipayPC;

  /// No description provided for @membershipPaymentAlipayWap.
  ///
  /// In en, this message translates to:
  /// **'Alipay (Mobile)'**
  String get membershipPaymentAlipayWap;

  /// No description provided for @membershipPaymentStripe.
  ///
  /// In en, this message translates to:
  /// **'Credit card'**
  String get membershipPaymentStripe;

  /// No description provided for @membershipCheckoutOrderTitle.
  ///
  /// In en, this message translates to:
  /// **'Order details'**
  String get membershipCheckoutOrderTitle;

  /// No description provided for @membershipCheckoutPay.
  ///
  /// In en, this message translates to:
  /// **'Pay now'**
  String get membershipCheckoutPay;

  /// No description provided for @membershipCheckoutWechatScan.
  ///
  /// In en, this message translates to:
  /// **'Scan with WeChat to pay'**
  String get membershipCheckoutWechatScan;

  /// No description provided for @membershipCheckoutH5Opened.
  ///
  /// In en, this message translates to:
  /// **'WeChat H5 payment opened'**
  String get membershipCheckoutH5Opened;

  /// No description provided for @membershipCheckoutRedirected.
  ///
  /// In en, this message translates to:
  /// **'Redirected to payment page, return after completion'**
  String get membershipCheckoutRedirected;

  /// No description provided for @membershipOrderProviderWechat.
  ///
  /// In en, this message translates to:
  /// **'WeChat Pay'**
  String get membershipOrderProviderWechat;

  /// No description provided for @membershipOrderProviderAlipay.
  ///
  /// In en, this message translates to:
  /// **'Alipay'**
  String get membershipOrderProviderAlipay;

  /// No description provided for @membershipOrderProviderStripe.
  ///
  /// In en, this message translates to:
  /// **'Stripe'**
  String get membershipOrderProviderStripe;

  /// No description provided for @membershipOrderStatusPaid.
  ///
  /// In en, this message translates to:
  /// **'Paid'**
  String get membershipOrderStatusPaid;

  /// No description provided for @membershipOrderStatusPending.
  ///
  /// In en, this message translates to:
  /// **'Pending'**
  String get membershipOrderStatusPending;

  /// No description provided for @membershipOrderStatusRefunded.
  ///
  /// In en, this message translates to:
  /// **'Refunded'**
  String get membershipOrderStatusRefunded;

  /// No description provided for @membershipOrderStatusFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed'**
  String get membershipOrderStatusFailed;

  /// No description provided for @membershipOrderStatusCanceled.
  ///
  /// In en, this message translates to:
  /// **'Canceled'**
  String get membershipOrderStatusCanceled;

  /// No description provided for @membershipCouponTitle.
  ///
  /// In en, this message translates to:
  /// **'Redeem code'**
  String get membershipCouponTitle;

  /// No description provided for @membershipCouponHint.
  ///
  /// In en, this message translates to:
  /// **'Enter your code to redeem instantly'**
  String get membershipCouponHint;

  /// No description provided for @membershipCouponSubmit.
  ///
  /// In en, this message translates to:
  /// **'Redeem'**
  String get membershipCouponSubmit;

  /// No description provided for @membershipCouponSuccess.
  ///
  /// In en, this message translates to:
  /// **'Redeemed'**
  String get membershipCouponSuccess;

  /// No description provided for @membershipCouponNotFound.
  ///
  /// In en, this message translates to:
  /// **'Invalid code'**
  String get membershipCouponNotFound;

  /// No description provided for @membershipCouponExpired.
  ///
  /// In en, this message translates to:
  /// **'Code expired'**
  String get membershipCouponExpired;

  /// No description provided for @membershipCouponInactive.
  ///
  /// In en, this message translates to:
  /// **'Code disabled'**
  String get membershipCouponInactive;

  /// No description provided for @membershipCouponAlreadyUsed.
  ///
  /// In en, this message translates to:
  /// **'You have already used this code'**
  String get membershipCouponAlreadyUsed;

  /// No description provided for @membershipReferralTitle.
  ///
  /// In en, this message translates to:
  /// **'Invite friends'**
  String get membershipReferralTitle;

  /// No description provided for @membershipReferralYourCode.
  ///
  /// In en, this message translates to:
  /// **'Your invite code'**
  String get membershipReferralYourCode;

  /// No description provided for @membershipReferralStats.
  ///
  /// In en, this message translates to:
  /// **'Stats'**
  String get membershipReferralStats;

  /// No description provided for @membershipReferralStatTotal.
  ///
  /// In en, this message translates to:
  /// **'Total'**
  String get membershipReferralStatTotal;

  /// No description provided for @membershipReferralStatRewarded.
  ///
  /// In en, this message translates to:
  /// **'Rewarded'**
  String get membershipReferralStatRewarded;

  /// No description provided for @membershipReferralStatPending.
  ///
  /// In en, this message translates to:
  /// **'Pending'**
  String get membershipReferralStatPending;

  /// No description provided for @membershipReferralStatReverted.
  ///
  /// In en, this message translates to:
  /// **'Reverted'**
  String get membershipReferralStatReverted;

  /// No description provided for @membershipReferralShare.
  ///
  /// In en, this message translates to:
  /// **'Share'**
  String get membershipReferralShare;

  /// No description provided for @membershipReferralCopyCode.
  ///
  /// In en, this message translates to:
  /// **'Code copied'**
  String get membershipReferralCopyCode;

  /// No description provided for @membershipReferralCopyLink.
  ///
  /// In en, this message translates to:
  /// **'Link copied'**
  String get membershipReferralCopyLink;

  /// No description provided for @membershipReferralRulesTitle.
  ///
  /// In en, this message translates to:
  /// **'How rewards work'**
  String get membershipReferralRulesTitle;

  /// No description provided for @chatV2HintDismiss.
  ///
  /// In en, this message translates to:
  /// **'Don\'t show again'**
  String get chatV2HintDismiss;

  /// No description provided for @chatV2ChangelogHeadline.
  ///
  /// In en, this message translates to:
  /// **'BiuMind Chat picked up 5 new tools'**
  String get chatV2ChangelogHeadline;

  /// No description provided for @commonForbidden.
  ///
  /// In en, this message translates to:
  /// **'Forbidden — you don\'t own this resource.'**
  String get commonForbidden;

  /// No description provided for @appsPermissionRequestIntro.
  ///
  /// In en, this message translates to:
  /// **'This app requests the following permissions. Uncheck any you don\'t want to grant; the server enforces only the granted subset.'**
  String get appsPermissionRequestIntro;

  /// No description provided for @appsErrForbidden.
  ///
  /// In en, this message translates to:
  /// **'You don\'t have permission to perform this action.'**
  String get appsErrForbidden;

  /// No description provided for @permWikiRead.
  ///
  /// In en, this message translates to:
  /// **'Read your Wiki content (limited to this app\'s namespace).'**
  String get permWikiRead;

  /// No description provided for @permWikiWrite.
  ///
  /// In en, this message translates to:
  /// **'Write to your Wiki (limited to this app\'s namespace).'**
  String get permWikiWrite;
}

class _AppLocalizationsDelegate
    extends LocalizationsDelegate<AppLocalizations> {
  const _AppLocalizationsDelegate();

  @override
  Future<AppLocalizations> load(Locale locale) {
    return SynchronousFuture<AppLocalizations>(lookupAppLocalizations(locale));
  }

  @override
  bool isSupported(Locale locale) =>
      <String>['en', 'zh'].contains(locale.languageCode);

  @override
  bool shouldReload(_AppLocalizationsDelegate old) => false;
}

AppLocalizations lookupAppLocalizations(Locale locale) {
  // Lookup logic when only language code is specified.
  switch (locale.languageCode) {
    case 'en':
      return AppLocalizationsEn();
    case 'zh':
      return AppLocalizationsZh();
  }

  throw FlutterError(
    'AppLocalizations.delegate failed to load unsupported locale "$locale". This is likely '
    'an issue with the localizations generation tool. Please file an issue '
    'on GitHub with a reproducible sample app and the gen-l10n configuration '
    'that was used.',
  );
}
