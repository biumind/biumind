// ReferralsClient — W6-13 邀请奖励.
//   POST /v1/referrals/invite — 拿邀请码 + stats
//   POST /v1/referrals/claim  — 用别人的码建立邀请关系

import '../../../data/api/_http_helpers.dart';
import '../domain/referral.dart';

class ReferralsClient {
  final Uri identityBase;
  final String? Function() getToken;

  ReferralsClient({required this.identityBase, required this.getToken});

  Future<ReferralStats> invite() async {
    final j = await apiRequest(
      method: 'POST',
      url: identityBase.resolve('/v1/referrals/invite'),
      bearerToken: getToken(),
      body: {},
    );
    return ReferralStats.fromJson(j);
  }

  Future<ReferralClaimResult> claim({
    required String inviterUserID,
    required String inviteCode,
    String? deviceFP,
    String? clientIP,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: identityBase.resolve('/v1/referrals/claim'),
      bearerToken: getToken(),
      body: {
        'inviter_user_id': inviterUserID,
        'invite_code': inviteCode,
        if (deviceFP != null && deviceFP.isNotEmpty) 'device_fp': deviceFP,
        if (clientIP != null && clientIP.isNotEmpty) 'client_ip': clientIP,
      },
    );
    return ReferralClaimResult.fromJson(j);
  }
}
