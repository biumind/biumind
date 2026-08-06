// lib/wxacode.ts — 小程序码 (wxacode) 接口预留.
//
// 微信侧调 wx.cloud / 后端 HTTP 走 wxacode.getUnlimited 拿到无限制
// 小程序码 (PNG buffer), 推荐参数:
//   page  = 'pages/chat/index'   // 扫码进入页
//   scene = 't=' + threadId       // ≤ 32 字符, 进入页用 onLoad(options) 解析
//   check_path = false            // 体验版/未发布也能扫
//   env_version = 'release'       // 走线上版
//
// 后端契约 (待 services/brain 实现):
//   GET /v1/share/wxacode?scene=...&page=...
//   200 OK { src: 'data:image/png;base64,...' }
//
// 当前阶段后端未 ready, 走 placeholder — share_card.ts 检测到
// isPlaceholder=true 时画一个简易"扫码占位"矩形, 不阻塞 UI 体验.

export interface WxacodeParams {
  /** ≤ 32 字符. 推荐 't=<threadId>' / 'm=<messageId>' */
  scene?: string;
  /** 默认 pages/chat/index */
  page?: string;
}

export interface WxacodeResult {
  /** 图片源 — base64 dataurl 或 https URL. placeholder 时为空字符串 */
  src: string;
  /** 当前是否走的是占位实现 (后端未 ready) */
  isPlaceholder: boolean;
  /** 提示文案 — 画在占位矩形下方 */
  hint?: string;
}

/**
 * 获取分享用小程序码.
 *
 * 当前实现: 直接返回占位. 等后端 /v1/share/wxacode ready 后改为真请求.
 * 调用方不需要变 — share_card.ts 已处理两条路径.
 */
export async function getShareQRCode(
  _params?: WxacodeParams,
): Promise<WxacodeResult> {
  // TODO(brain): 接 GET /v1/share/wxacode 后改为:
  //   const r = await get<{ src: string }>('/v1/share/wxacode?scene=' + ...);
  //   return { src: r.src, isPlaceholder: false };
  return {
    src: '',
    isPlaceholder: true,
    hint: '扫码体验 BiuMind',
  };
}
