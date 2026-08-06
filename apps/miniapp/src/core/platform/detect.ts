// platform/detect.ts — 运行时识别当前小程序宿主.
//
// uni-app 用 vite define 在编译期注入下面的全局常量, 不同平台编译产物
// 只保留对应分支. 不要用 process.env.UNI_PLATFORM (那只在 node 编译期).

export type Platform =
  | 'mp-weixin'
  | 'mp-alipay'
  | 'mp-toutiao'
  | 'mp-baidu'
  | 'mp-qq'
  | 'mp-kuaishou'
  | 'mp-jd'
  | 'mp-lark'
  | 'h5'
  | 'app'
  | 'unknown';

export function detectPlatform(): Platform {
  // #ifdef MP-WEIXIN
  return 'mp-weixin';
  // #endif
  // #ifdef MP-ALIPAY
  return 'mp-alipay';
  // #endif
  // #ifdef MP-TOUTIAO
  return 'mp-toutiao';
  // #endif
  // #ifdef MP-BAIDU
  return 'mp-baidu';
  // #endif
  // #ifdef MP-QQ
  return 'mp-qq';
  // #endif
  // #ifdef MP-KUAISHOU
  return 'mp-kuaishou';
  // #endif
  // #ifdef MP-JD
  return 'mp-jd';
  // #endif
  // #ifdef MP-LARK
  return 'mp-lark';
  // #endif
  // #ifdef H5
  return 'h5';
  // #endif
  // #ifdef APP-PLUS
  return 'app';
  // #endif
  // eslint-disable-next-line no-unreachable
  return 'unknown';
}

// supportsChunkedSSE — 该平台 uni.request 是否支持 enableChunked + onChunkReceived.
// 不支持的退化到短轮询. 见 BiuMind-MiniApp-Design.md §4.
export function supportsChunkedSSE(p: Platform = detectPlatform()): boolean {
  switch (p) {
    case 'mp-weixin':
    case 'mp-qq':
      return true; // 微信基础库 ≥ 2.20.1; QQ 共用微信内核
    case 'h5':
      return true; // 用 EventSource 路径
    case 'mp-toutiao':
    case 'mp-baidu':
    case 'mp-lark':
      // 文档声称支持但实测不稳, 先短轮询; 后续按平台版本探测开放
      return false;
    case 'mp-alipay':
    case 'mp-kuaishou':
    case 'mp-jd':
      return false;
    default:
      return false;
  }
}
