// Referral — W6-13 邀请关系视图模型.
// 与 services/identity/internal/api/coupons_referrals.go inviteResp + ReferralStats 对齐.

class ReferralStats {
  final int total;
  final int rewarded;
  final int pending;
  final int reverted;
  final String inviteCode;

  const ReferralStats({
    required this.total,
    required this.rewarded,
    required this.pending,
    required this.reverted,
    required this.inviteCode,
  });

  static const empty = ReferralStats(
    total: 0, rewarded: 0, pending: 0, reverted: 0, inviteCode: '',
  );

  factory ReferralStats.fromJson(Map<String, dynamic> j) {
    final stats = j['stats'] is Map<String, dynamic>
        ? j['stats'] as Map<String, dynamic>
        : const <String, dynamic>{};
    return ReferralStats(
      total: ((stats['Total'] ?? stats['total'] ?? 0) as num).toInt(),
      rewarded: ((stats['Rewarded'] ?? stats['rewarded'] ?? 0) as num).toInt(),
      pending: ((stats['Pending'] ?? stats['pending'] ?? 0) as num).toInt(),
      reverted: ((stats['Reverted'] ?? stats['reverted'] ?? 0) as num).toInt(),
      inviteCode: (j['invite_code'] ?? stats['InviteCode'] ?? stats['invite_code'] ?? '') as String,
    );
  }
}

class ReferralClaimResult {
  final String referralID;
  final String status;

  const ReferralClaimResult({required this.referralID, required this.status});

  factory ReferralClaimResult.fromJson(Map<String, dynamic> j) =>
      ReferralClaimResult(
        referralID: (j['referral_id'] ?? '') as String,
        status: (j['status'] ?? 'pending') as String,
      );
}
