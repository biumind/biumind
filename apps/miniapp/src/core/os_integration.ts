// core/os_integration.ts — 平台原生能力统一层 (C10).
//
// 业务代码不直接调 wx.* / my.* / tt.* 等平台 API; 通过这里封装的
// 抽象函数. 各平台差异 + fallback 集中.
//
// 当前覆盖:
//   - requestSubscribeMessage: 订阅消息授权 → 上报后端
//   - shareTo: 主动分享 (调起分享菜单 / 复制链接 fallback)
//   - showToast: 跨端 toast
//   - copyToClipboard: 跨端剪贴板

import { detectPlatform } from './platform/detect';
import { post } from '@/data/api/client';

interface SubscribeMpReq {
  platform: string;
  openid: string;
  template_id: string;
  times: number;
}

/**
 * requestSubscribeMessage — 用户主动点"开启通知"时调.
 *
 * 流程:
 *   1. 平台 SDK 弹授权窗 (wx.requestSubscribeMessage / 同等 API)
 *   2. 用户点"允许"后, SDK 返 { tplId: 'accept' | 'reject' }
 *   3. 把 accept 的 template_id 上报后端 (POST /v1/notify/mp-subscribe)
 *
 * 不支持的平台 (H5 等) 抛 Error, 调用方按需 catch.
 */
export async function requestSubscribeMessage(templateIds: string[]): Promise<void> {
  if (templateIds.length === 0) return;
  const platform = detectPlatform();

  // openid 在 token_manager 里没存; 这里实际真实场景是: 后端拿到 user 后
  // 把 openid 也写进 user.providers; W5 此处简化为 from-provider-list:
  // 客户端先 GET /v1/identity/me/providers 找当前 platform 的 openid.
  // 为不阻塞编译, 当前从 storage 缓存里取 (登录时由 auth.ts 写入).
  const openid = readCachedOpenid(platform);
  if (!openid) {
    throw new Error('未登录或缺少当前平台 openid 缓存');
  }

  // 各平台调用差异
  const accepted: string[] = [];

  // #ifdef MP-WEIXIN
  await new Promise<void>((resolve, reject) => {
    uni.requestSubscribeMessage({
      tmplIds: templateIds,
      success: (res) => {
        for (const tpl of templateIds) {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          if ((res as any)[tpl] === 'accept') accepted.push(tpl);
        }
        resolve();
      },
      fail: (e) => reject(new Error(e.errMsg || 'requestSubscribeMessage failed')),
    });
  });
  // #endif

  // #ifdef MP-ALIPAY
  // 支付宝: my.requestSubscribeMessage(同 tmpl_ids 参数); 类型补丁缺失故用 any.
  await new Promise<void>((resolve, reject) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const my = (globalThis as any).my;
    if (!my || typeof my.requestSubscribeMessage !== 'function') {
      reject(new Error('alipay: requestSubscribeMessage 不可用'));
      return;
    }
    my.requestSubscribeMessage({
      entityIds: templateIds,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      success: (res: any) => {
        for (const tpl of templateIds) {
          if (res?.behavior === 'subscribe') accepted.push(tpl);
        }
        resolve();
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      fail: (e: any) => reject(new Error(e?.errorMessage || 'alipay subscribe failed')),
    });
  });
  // #endif

  // #ifdef MP-TOUTIAO || MP-BAIDU || MP-QQ || MP-KUAISHOU || MP-JD || MP-LARK
  // 抖音 / 百度 / QQ / 快手 / 京东 / 飞书 — 各家都提供类似 wx 的 API
  // 但参数键名差异较大. 占位实现: 直接全部 accept (实际接入时按平台拆分).
  for (const tpl of templateIds) accepted.push(tpl);
  // #endif

  if (accepted.length === 0) {
    throw new Error('用户未授权任何模板');
  }

  // 上报后端
  for (const tpl of accepted) {
    const req: SubscribeMpReq = {
      platform,
      openid,
      template_id: tpl,
      times: 1,
    };
    await post<SubscribeMpReq, { id: string }>('/v1/notify/mp-subscribe', req);
  }
}

/**
 * shareTo — 主动调起分享 (小程序的"分享给朋友" / "分享到朋友圈").
 *
 * 注意: 小程序里, 真正的分享内容由页面 `onShareAppMessage` / `onShareTimeline`
 * 钩子返回; 这里只是把 *调起菜单* 这步抽象. 业务页面要自己定义钩子.
 */
export function shareTo(): void {
  const platform = detectPlatform();
  switch (platform) {
    case 'mp-weixin':
    case 'mp-qq':
      uni.showShareMenu({ withShareTicket: true });
      return;
    case 'mp-toutiao':
    case 'mp-baidu':
    case 'mp-alipay':
    case 'mp-kuaishou':
    case 'mp-jd':
    case 'mp-lark':
      // 多数家走 page-level 分享按钮, 不暴露主动调起 API; toast 提示.
      uni.showToast({ title: '点击右上角"..."分享', icon: 'none' });
      return;
    case 'h5':
      // H5 复制链接到剪贴板
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      copyToClipboard((globalThis as any).location?.href || 'https://biumind.cn');
      return;
    default:
      uni.showToast({ title: '当前平台不支持分享', icon: 'none' });
  }
}

export function copyToClipboard(text: string): Promise<void> {
  return new Promise((resolve, reject) => {
    uni.setClipboardData({
      data: text,
      success: () => resolve(),
      fail: (e) => reject(new Error(e.errMsg || 'clipboard failed')),
    });
  });
}

// ── 内部: openid 缓存 ─────────────────────────────────────────
//
// 真正的 openid 存在 identity_providers 表, 客户端要拿就要调
// /v1/identity/me/providers 一次. W5 简化: 登录时把当前 platform openid
// 同步写到 storage, 订阅消息授权时直接读.

const OPENID_KEY_PREFIX = 'biumind.openid.';

export function cacheOpenid(platform: string, openid: string): void {
  try {
    uni.setStorageSync(OPENID_KEY_PREFIX + platform, openid);
  } catch {
    /* noop */
  }
}

function readCachedOpenid(platform: string): string {
  try {
    const v = uni.getStorageSync(OPENID_KEY_PREFIX + platform);
    return typeof v === 'string' ? v : '';
  } catch {
    return '';
  }
}
