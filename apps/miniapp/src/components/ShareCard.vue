<script setup lang="ts">
// ShareCard.vue — 隐藏的 canvas 2d 节点 + 导出长图能力.
//
// 业务调用:
//   const ref = ref<InstanceType<typeof ShareCard>>();
//   const path = await ref.value.exportSingle({prompt, answer, scene: 'm='+id});
//   uni.previewImage({ urls: [path] });
//
// 跨端策略:
//   mp-weixin: canvas 2d + uni.canvasToTempFilePath, 主战场
//   h5      : <canvas> + toDataURL fallback (有限支持)
//   其它端  : 直接 reject, 业务层 toast "暂不支持"

import { getCurrentInstance } from 'vue';
import {
  renderSingleCard,
  renderLongShot,
  type CardMessage,
} from '@/lib/share_card';
import { getShareQRCode, type WxacodeParams } from '@/lib/wxacode';

const inst = getCurrentInstance();

interface NodeQueryRes {
  node: {
    width: number;
    height: number;
    getContext(t: '2d'): unknown;
  };
  width: number;
  height: number;
}

function getCanvasNode(): Promise<NodeQueryRes> {
  return new Promise((resolve, reject) => {
    // 给 layout 一帧时间, 避免组件刚 mount 就查不到节点
    setTimeout(() => {
      const query = uni.createSelectorQuery().in(inst);
      query
        .select('#biu-share-canvas')
        .fields({ node: true, size: true })
        .exec((res: unknown) => {
          const arr = res as NodeQueryRes[] | null;
          if (!arr || !arr[0] || !arr[0].node) {
            reject(new Error('canvas 节点未找到 - 当前端可能不支持 canvas 2d'));
            return;
          }
          resolve(arr[0]);
        });
    }, 30);
  });
}

interface CanvasToTempFilePathSuccess {
  tempFilePath: string;
}

function canvasToTempFile(
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  node: any,
  width: number,
  height: number,
): Promise<string> {
  return new Promise((resolve, reject) => {
    uni.canvasToTempFilePath(
      {
        canvas: node,
        x: 0,
        y: 0,
        width,
        height,
        destWidth: width,
        destHeight: height,
        fileType: 'png',
        success: (r: CanvasToTempFilePathSuccess) => resolve(r.tempFilePath),
        fail: (e: { errMsg?: string }) =>
          reject(new Error(e.errMsg || 'canvasToTempFilePath failed')),
      } as Parameters<typeof uni.canvasToTempFilePath>[0],
      // mp-weixin 的 canvasToTempFilePath 第二参数应为 component context
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (inst as any)?.proxy,
    );
  });
}

export interface ExportSingleArgs {
  prompt: string;
  answer: string;
  /** 用于二维码 scene, 通常为 'm=' + messageId */
  scene?: string;
}

export interface ExportLongArgs {
  messages: CardMessage[];
  threadTitle?: string;
  /** 用于二维码 scene, 通常为 't=' + threadId */
  scene?: string;
}

async function exportSingle(args: ExportSingleArgs): Promise<string> {
  const { node } = await getCanvasNode();
  const dpr = uni.getSystemInfoSync().pixelRatio || 2;
  const qr = await getShareQRCode(
    args.scene ? ({ scene: args.scene } as WxacodeParams) : undefined,
  );
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const ctx = node.getContext('2d') as any;
  const { width, height } = renderSingleCard(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    { canvas: node as any, ctx, dpr },
    { prompt: args.prompt, answer: args.answer, qr },
  );
  // 等一帧让 GPU 把绘制提交完成 (mp-weixin 上必要)
  await new Promise((r) => setTimeout(r, 60));
  return canvasToTempFile(node, width * dpr, height * dpr);
}

async function exportLong(args: ExportLongArgs): Promise<string> {
  const { node } = await getCanvasNode();
  const dpr = uni.getSystemInfoSync().pixelRatio || 2;
  const qr = await getShareQRCode(
    args.scene ? ({ scene: args.scene } as WxacodeParams) : undefined,
  );
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const ctx = node.getContext('2d') as any;
  const { width, height } = renderLongShot(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    { canvas: node as any, ctx, dpr },
    { messages: args.messages, threadTitle: args.threadTitle, qr },
  );
  await new Promise((r) => setTimeout(r, 80));
  return canvasToTempFile(node, width * dpr, height * dpr);
}

defineExpose({ exportSingle, exportLong });
</script>

<template>
  <!-- 屏外离屏 canvas — 不参与正常布局, 仅作为绘制目标 -->
  <view class="share-card-host">
    <canvas
      id="biu-share-canvas"
      type="2d"
      class="share-canvas"
    />
  </view>
</template>

<style scoped>
.share-card-host {
  position: fixed;
  top: -100000rpx;
  left: -100000rpx;
  width: 750rpx;
  height: 100rpx;
  overflow: hidden;
  pointer-events: none;
  z-index: -1;
}
.share-canvas {
  width: 750rpx;
  /* 高度运行时按内容动态计算 — 这里给个充足的 max 让 layout 不限制 */
  height: 4000rpx;
}
</style>
