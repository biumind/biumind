<script setup lang="ts">
// 隐私政策 — 微信小程序提审 + 国内合规硬要求.
//
// ⚠️ 内容仅为起步合规模板, 涵盖小程序提审需要的最小信息披露范围
// (个人信息收集类型 / 用途 / 第三方共享 / 用户权利 / 联系方式).
// 上线前必须由法务/合规审过, 本文件仅占位防止审核被驳回.
//
// 同步更新位置:
//   1. 公众平台 → 设置 → 隐私设置 → 用户隐私保护指引 (单独的微信审核流)
//   2. 本页 (用户在 App 内查看)
//   3. 网站 https://your-biumind.example.com/privacy (跨产品同步)

import { ref } from 'vue';

const lastUpdated = ref('2026-05-28');
const contactEmail = ref('privacy@biumind.cn'); // ⚠️ 替换为真实邮箱
</script>

<template>
  <view class="page">
    <view class="header">
      <text class="title">BiuMind 隐私政策</text>
      <text class="meta">最后更新:{{ lastUpdated }}</text>
    </view>

    <view class="section">
      <text class="h2">一、引言</text>
      <text class="p">
        BiuMind（以下简称"我们"）非常重视用户的个人信息保护。本隐私政策说明我们收集、使用、存储、共享您个人信息的方式与目的。请您在使用本服务前仔细阅读并理解本政策的全部内容。
      </text>
    </view>

    <view class="section">
      <text class="h2">二、我们收集的信息</text>
      <text class="p">为向您提供 BiuMind 服务,我们会收集以下信息:</text>
      <text class="p">
        <text class="b">1. 账号信息:</text>
        当您通过微信登录时,我们会向微信开放平台获取您的 OpenID、UnionID 用于唯一标识用户身份。在您主动授权后,我们可能获取您的微信昵称、头像。
      </text>
      <text class="p">
        <text class="b">2. 使用日志:</text>
        为保障服务安全与稳定,我们会记录您的登录时间、设备信息(机型、操作系统版本)、IP 地址、访问 URL 与请求时间。
      </text>
      <text class="p">
        <text class="b">3. 用户内容:</text>
        当您主动输入对话内容、上传知识库文档时,这些内容将被存储用于提供 AI 对话与知识检索服务。
      </text>
      <text class="p">
        <text class="b">4. 设备标识:</text>
        我们生成一个本地随机字符串 (installation_id) 用于设备级会话管理,该标识不与您的真实身份关联。
      </text>
    </view>

    <view class="section">
      <text class="h2">三、我们如何使用信息</text>
      <text class="p">
        1. 提供核心服务:基于您的对话内容调用 AI 模型生成回复;基于您上传的内容进行知识检索。
      </text>
      <text class="p">
        2. 账号识别与安全:验证您的身份,防止未授权访问。
      </text>
      <text class="p">
        3. 服务优化:基于聚合统计数据(不识别个人身份)分析功能使用情况,改进产品体验。
      </text>
      <text class="p">
        4. 法律合规:配合监管要求或法律程序时进行必要披露。
      </text>
    </view>

    <view class="section">
      <text class="h2">四、信息存储</text>
      <text class="p">
        您的个人信息存储于中国境内服务器,存储时长不超过为提供服务所必需的期间。账号注销后 30 天内删除您的可识别个人信息(法律要求保留的除外)。
      </text>
    </view>

    <view class="section">
      <text class="h2">五、信息共享</text>
      <text class="p">未经您同意,我们不会向任何第三方共享您的个人信息,以下情形除外:</text>
      <text class="p">
        <text class="b">1. AI 模型服务商:</text>
        您输入的对话内容会传输至第三方大语言模型服务商(如 Anthropic、OpenAI、深度求索等)以生成回复。我们已与这些服务商签署数据处理协议,要求其不留存或将您的内容用于模型训练。
      </text>
      <text class="p">
        <text class="b">2. 微信开放平台:</text>
        登录验证、订阅消息推送时,我们会与腾讯交互必要标识符。
      </text>
      <text class="p">
        <text class="b">3. 法律强制要求:</text>
        司法、行政机关基于法律程序的合法要求。
      </text>
    </view>

    <view class="section">
      <text class="h2">六、您的权利</text>
      <text class="p">您对自己的个人信息享有以下权利:</text>
      <text class="p">1. 查询/复制:可在"我的"页面查看绑定的账号信息。</text>
      <text class="p">2. 更正/删除:可在"我的"页面解绑账号或联系我们删除特定数据。</text>
      <text class="p">3. 撤回授权:可在微信"设置 → 隐私 → 授权管理"中撤回对 BiuMind 的授权。</text>
      <text class="p">4. 注销账号:可联系客服注销账号,我们将在 30 天内删除您的可识别个人信息。</text>
    </view>

    <view class="section">
      <text class="h2">七、第三方 SDK 与服务</text>
      <text class="p">本小程序集成以下第三方服务,请您同时遵守相应隐私政策:</text>
      <text class="p">· 微信开放平台 (腾讯) — 登录、订阅消息</text>
      <text class="p">· 大语言模型 API — 仅传输对话内容用于生成回复,不留存</text>
    </view>

    <view class="section">
      <text class="h2">八、未成年人保护</text>
      <text class="p">
        我们不主动面向 14 岁以下未成年人提供服务。若您是未成年人,请在监护人指导下使用本服务并阅读本政策。
      </text>
    </view>

    <view class="section">
      <text class="h2">九、政策变更</text>
      <text class="p">
        本政策更新时,我们会在小程序内显著位置提示。重大变更(如新增个人信息收集类型)需要您重新同意后方可继续使用。
      </text>
    </view>

    <view class="section">
      <text class="h2">十、联系我们</text>
      <text class="p">
        如您对本政策或个人信息处理有任何疑问、投诉,请联系:
      </text>
      <text class="p">邮箱: {{ contactEmail }}</text>
    </view>

    <view class="footer-note">
      <text class="dim">— 完 —</text>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.page {
  padding: 32rpx;
  background: #fff;
}
.header {
  margin-bottom: 48rpx;
}
.title {
  display: block;
  font-size: 40rpx;
  font-weight: 700;
  color: #1f2937;
}
.meta {
  display: block;
  margin-top: 12rpx;
  font-size: 24rpx;
  color: #9ca3af;
}
.section {
  margin-bottom: 48rpx;
}
.h2 {
  display: block;
  font-size: 30rpx;
  font-weight: 600;
  color: #1f2937;
  margin-bottom: 20rpx;
}
.p {
  display: block;
  font-size: 26rpx;
  line-height: 1.7;
  color: #374151;
  margin-bottom: 12rpx;
}
.b {
  font-weight: 600;
  color: #1f2937;
}
.footer-note {
  text-align: center;
  padding: 48rpx 0;
}
.dim {
  font-size: 24rpx;
  color: #9ca3af;
}
</style>
