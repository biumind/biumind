// data/api/auth.ts — 9 端小程序登录 client.
//
// 每个平台:
//   1. 调本平台 SDK 拿 code (uni.login provider 各异; 部分平台用专用 API)
//   2. 把 code 发给后端 POST /v1/auth/<platform>/mp-login
//   3. setTokens 落本地
//
// 后端 alipay / jd 当前 503 — 客户端这一层先把 code 拿到, 端到端联通后
// 再切 RSA / SDK 实现, 不影响其他平台路径.

import { post } from './client';
import { setTokens, clearTokens } from '@/core/token_manager';

interface LoginRespUser {
  id: string;
  email: string;
  display_name: string;
  email_verified: boolean;
}

interface LoginResp {
  access_token: string;
  refresh_token: string;
  expires_in_seconds: number;
  user: LoginRespUser;
}

interface MpLoginReq {
  code: string;
  installation_id: string;
  device_name: string;
}

const INSTALL_KEY = 'biumind.installation_id';

function getOrCreateInstallationID(): string {
  try {
    const v = uni.getStorageSync(INSTALL_KEY);
    if (typeof v === 'string' && v.length > 0) return v;
  } catch {
    /* fall through */
  }
  const id = 'mp-' + Math.random().toString(36).slice(2) + '-' + Date.now().toString(36);
  try {
    uni.setStorageSync(INSTALL_KEY, id);
  } catch {
    /* noop */
  }
  return id;
}

// uniLoginCode — 各平台 uni.login provider 字符串都不一样; 抽到这里集中.
function uniLoginCode(provider: string): Promise<string> {
  return new Promise((resolve, reject) => {
    uni.login({
      provider,
      success: (res) => {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const r = res as any;
        const code = r.code || r.authCode || r.authorization_code;
        if (typeof code === 'string' && code.length > 0) resolve(code);
        else reject(new Error('uni.login(' + provider + '): empty code'));
      },
      fail: (e) => reject(new Error(e.errMsg || ('uni.login(' + provider + ') failed'))),
    });
  });
}

async function exchangeAndStore(
  endpoint: string, code: string, deviceName: string,
): Promise<LoginRespUser> {
  const req: MpLoginReq = {
    code,
    installation_id: getOrCreateInstallationID(),
    device_name: deviceName,
  };
  const resp = await post<MpLoginReq, LoginResp>(endpoint, req);
  setTokens({
    accessToken: resp.access_token,
    refreshToken: resp.refresh_token,
    expiresAt: Date.now() + resp.expires_in_seconds * 1000,
  });
  return resp.user;
}

// ── 9 端登录 ─────────────────────────────────────────────────

export async function loginWithWechatMP(): Promise<LoginRespUser> {
  // #ifndef MP-WEIXIN
  throw new Error('wechat login only available in mp-weixin build');
  // #endif
  // #ifdef MP-WEIXIN
  const code = await uniLoginCode('weixin');
  return exchangeAndStore('/v1/auth/wechat/mp-login', code, 'wechat-miniapp');
  // #endif
}

export async function loginWithAlipayMP(): Promise<LoginRespUser> {
  // #ifndef MP-ALIPAY
  throw new Error('alipay login only available in mp-alipay build');
  // #endif
  // #ifdef MP-ALIPAY
  const code = await uniLoginCode('alipay');
  return exchangeAndStore('/v1/auth/alipay/mp-login', code, 'alipay-miniapp');
  // #endif
}

export async function loginWithToutiaoMP(): Promise<LoginRespUser> {
  // #ifndef MP-TOUTIAO
  throw new Error('toutiao login only available in mp-toutiao build');
  // #endif
  // #ifdef MP-TOUTIAO
  const code = await uniLoginCode('toutiao');
  return exchangeAndStore('/v1/auth/toutiao/mp-login', code, 'toutiao-miniapp');
  // #endif
}

export async function loginWithBaiduMP(): Promise<LoginRespUser> {
  // #ifndef MP-BAIDU
  throw new Error('baidu login only available in mp-baidu build');
  // #endif
  // #ifdef MP-BAIDU
  const code = await uniLoginCode('baidu');
  return exchangeAndStore('/v1/auth/baidu/mp-login', code, 'baidu-miniapp');
  // #endif
}

export async function loginWithQQMP(): Promise<LoginRespUser> {
  // #ifndef MP-QQ
  throw new Error('qq login only available in mp-qq build');
  // #endif
  // #ifdef MP-QQ
  const code = await uniLoginCode('qq');
  return exchangeAndStore('/v1/auth/qq/mp-login', code, 'qq-miniapp');
  // #endif
}

export async function loginWithKuaishouMP(): Promise<LoginRespUser> {
  // #ifndef MP-KUAISHOU
  throw new Error('kuaishou login only available in mp-kuaishou build');
  // #endif
  // #ifdef MP-KUAISHOU
  const code = await uniLoginCode('kuaishou');
  return exchangeAndStore('/v1/auth/kuaishou/mp-login', code, 'kuaishou-miniapp');
  // #endif
}

export async function loginWithJDMP(): Promise<LoginRespUser> {
  // #ifndef MP-JD
  throw new Error('jd login only available in mp-jd build');
  // #endif
  // #ifdef MP-JD
  const code = await uniLoginCode('jd');
  return exchangeAndStore('/v1/auth/jd/mp-login', code, 'jd-miniapp');
  // #endif
}

export async function loginWithLarkMP(): Promise<LoginRespUser> {
  // #ifndef MP-LARK
  throw new Error('lark login only available in mp-lark build');
  // #endif
  // #ifdef MP-LARK
  const code = await uniLoginCode('lark');
  return exchangeAndStore('/v1/auth/lark/mp-login', code, 'lark-miniapp');
  // #endif
}

// ── H5 双轨: 邮箱密码 + OAuth ───────────────────────────────────

// 邮箱密码登录 — 复用既有 /v1/auth/login (Flutter App 同款).
export async function loginWithEmailPassword(
  email: string, password: string,
): Promise<LoginRespUser> {
  interface PwdReq {
    email: string;
    password: string;
    device_name: string;
    installation_id: string;
  }
  const req: PwdReq = {
    email: email.trim().toLowerCase(),
    password,
    device_name: 'h5',
    installation_id: getOrCreateInstallationID(),
  };
  const resp = await post<PwdReq, LoginResp>('/v1/auth/login', req);
  setTokens({
    accessToken: resp.access_token,
    refreshToken: resp.refresh_token,
    expiresAt: Date.now() + resp.expires_in_seconds * 1000,
  });
  return resp.user;
}

// 注册 — 后端不下发 token, 只返 user_id + email_sent.
// 客户端拿到后跳"验证邮箱"页, 用户输入 code 调 verifyEmail.
export interface RegisterResp {
  user_id: string;
  email: string;
  verification_required: boolean;
  email_sent: boolean;
}
export async function register(
  email: string, password: string, displayName: string,
): Promise<RegisterResp> {
  return post<{
    email: string;
    password: string;
    display_name: string;
  }, RegisterResp>('/v1/auth/register', {
    email: email.trim().toLowerCase(),
    password,
    display_name: displayName,
  });
}

export async function verifyEmail(email: string, code: string): Promise<LoginRespUser> {
  const resp = await post<{ email: string; code: string }, LoginResp>(
    '/v1/auth/verify-email',
    { email: email.trim().toLowerCase(), code },
  );
  setTokens({
    accessToken: resp.access_token,
    refreshToken: resp.refresh_token,
    expiresAt: Date.now() + resp.expires_in_seconds * 1000,
  });
  return resp.user;
}

export async function forgotPassword(email: string): Promise<void> {
  await post<{ email: string }, unknown>('/v1/auth/forgot-password', {
    email: email.trim().toLowerCase(),
  });
}

export async function resetPassword(
  email: string, code: string, newPassword: string,
): Promise<void> {
  await post<
    { email: string; code: string; new_password: string },
    unknown
  >('/v1/auth/reset-password', {
    email: email.trim().toLowerCase(),
    code,
    new_password: newPassword,
  });
}

// H5 OAuth — 仅在 H5 build 中可用 (其他平台走 mp-login).
// 跳后端 /authorize → 微信 → 后端 /callback → fragment redirect 到
// /pages/me/oauth-return, 那个页面调 acceptOAuthFragment 完成 setTokens.
export function loginH5WithWechat(returnPath?: string): void {
  // #ifndef H5
  throw new Error('loginH5WithWechat: only available in H5 build');
  // #endif
  // #ifdef H5
  const base = (import.meta.env.VITE_BIU_API_BASE as string) || '';
  const ret = returnPath || '/';
  const url =
    base + '/v1/auth/wechat/h5-authorize?return=' + encodeURIComponent(ret);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (globalThis as any).location.href = url;
  // #endif
}

// acceptOAuthFragment — oauth-return 页调; 解 fragment, setTokens, 返 return path.
export interface OAuthFragmentResult {
  ok: boolean;
  error?: string;
  returnPath: string;
}
export function acceptOAuthFragment(rawHash: string): OAuthFragmentResult {
  const hash = rawHash.startsWith('#') ? rawHash.slice(1) : rawHash;
  const params = new URLSearchParams(hash);
  const ret = params.get('return') || '/';
  const err = params.get('error');
  if (err) return { ok: false, error: err, returnPath: ret };
  const access = params.get('access_token');
  const refresh = params.get('refresh_token');
  const expiresIn = parseInt(params.get('expires_in') || '0', 10);
  if (!access || !refresh || !expiresIn) {
    return { ok: false, error: 'missing_token', returnPath: ret };
  }
  setTokens({
    accessToken: access,
    refreshToken: refresh,
    expiresAt: Date.now() + expiresIn * 1000,
  });
  return { ok: true, returnPath: ret };
}

// 旧名 loginH5 保留兼容; 抛错引导用户用具体方法.
export async function loginH5(): Promise<LoginRespUser> {
  throw new Error('loginH5 is deprecated — use loginWithEmailPassword or loginH5WithWechat');
}

export function logout(): void {
  clearTokens();
}
