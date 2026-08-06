<script setup lang="ts">
// 用户协议 (服务条款) — 微信小程序 + 国内合规起步模板.
//
// ⚠️ 内容仅为合规初稿, 上线前必须由法务核定. 重点条款 (免责限度 /
// 知识产权归属 / 争议管辖) 可能直接影响诉讼结果, 不要照抄使用.
//
// 同步位置: 微信公众平台审核 / 本页 / 网站 /terms.

import { ref } from 'vue';

const lastUpdated = ref('2026-05-28');
const contactEmail = ref('legal@biumind.cn'); // ⚠️ 替换为真实邮箱
</script>

<template>
  <view class="page">
    <view class="header">
      <text class="title">BiuMind 用户协议</text>
      <text class="meta">最后更新:{{ lastUpdated }}</text>
    </view>

    <view class="section">
      <text class="h2">一、协议接受</text>
      <text class="p">
        欢迎使用 BiuMind!本协议是您与 BiuMind 运营方之间就使用本服务达成的法律协议。点击"登录"或继续使用本服务,即表示您已阅读、理解并同意本协议全部条款。如不同意,请停止使用本服务。
      </text>
    </view>

    <view class="section">
      <text class="h2">二、服务内容</text>
      <text class="p">
        BiuMind 是一款 AI 工作平台,为您提供以下核心服务:
      </text>
      <text class="p">1. AI 对话:基于大语言模型的智能问答与协作</text>
      <text class="p">2. 知识库:文档存储、检索与图谱化管理</text>
      <text class="p">3. 多端协同:在小程序、网页、桌面端、移动端之间同步数据</text>
      <text class="p">
        我们保留随时调整、新增、暂停或终止部分服务的权利,但会在重大变更时提前通过 App 内通知告知您。
      </text>
    </view>

    <view class="section">
      <text class="h2">三、账号注册与使用</text>
      <text class="p">
        1. 您应通过微信授权或邮箱注册创建账号,所提供信息应真实准确。
      </text>
      <text class="p">
        2. 您应妥善保管账号凭证,因您未尽妥善保管义务导致的损失由您自行承担。
      </text>
      <text class="p">
        3. 不得将账号转让、出借给他人。我们检测到异常使用时,有权暂停账号。
      </text>
      <text class="p">
        4. 您可随时通过"我的"页面解绑第三方登录或申请注销账号。
      </text>
    </view>

    <view class="section">
      <text class="h2">四、用户行为规范</text>
      <text class="p">您承诺在使用本服务时不进行以下行为:</text>
      <text class="p">1. 发布违反国家法律法规、危害国家安全、损害社会公共利益的内容</text>
      <text class="p">2. 发布暴力、色情、赌博、毒品等违法或违背公序良俗的内容</text>
      <text class="p">3. 发布涉嫌侵犯他人知识产权、商业秘密、隐私的内容</text>
      <text class="p">4. 利用本服务进行欺诈、传销、网络攻击或其他违法活动</text>
      <text class="p">5. 未经允许爬取、复制、贩卖本平台数据</text>
      <text class="p">6. 干扰服务正常运行(如恶意请求、试图绕过访问限制)</text>
      <text class="p">
        如您违反上述规范,我们有权立即暂停或终止向您提供服务,并保留追究法律责任的权利。
      </text>
    </view>

    <view class="section">
      <text class="h2">五、知识产权</text>
      <text class="p">
        <text class="b">1. 平台权利:</text>
        BiuMind 服务及其相关软件、文档、商标、Logo 等知识产权归 BiuMind 运营方所有。未经书面授权,您不得复制、修改、反向工程、二次分发。
      </text>
      <text class="p">
        <text class="b">2. 用户内容:</text>
        您输入的对话、上传的文档等内容(以下统称"用户内容")的知识产权归您所有。
      </text>
      <text class="p">
        <text class="b">3. 使用授权:</text>
        为提供服务,您授予我们一项免费、非独占、可在全球范围内使用您的用户内容的许可,仅限于为您提供 AI 对话、检索、存储等服务的必要范围,不用于公开发布或模型训练。
      </text>
      <text class="p">
        <text class="b">4. AI 生成内容:</text>
        AI 生成的回复内容,版权归属遵循适用法律。我们建议您在公开发布前自行评估生成内容的合规性与准确性。
      </text>
    </view>

    <view class="section">
      <text class="h2">六、付费服务</text>
      <text class="p">
        部分高级功能为付费服务,具体定价以"会员"页面公示为准。订阅服务支持随时取消,已支付费用按未使用比例退还(法律允许范围内)。
      </text>
    </view>

    <view class="section">
      <text class="h2">七、免责声明</text>
      <text class="p">
        1. 本服务"按现状"提供。我们对服务的可用性、稳定性、特定用途的适用性不作任何明示或默示担保。
      </text>
      <text class="p">
        2. AI 生成内容可能存在事实错误、过时或不准确。重要决策(医疗、法律、财务等)请以专业意见为准,我们不对依据 AI 内容做出的决策承担责任。
      </text>
      <text class="p">
        3. 因不可抗力(战争、自然灾害)、第三方原因(网络故障、电力中断)、政府行为造成的服务中断,我们不承担违约责任。
      </text>
      <text class="p">
        4. 在适用法律允许的最大范围内,我们对您因使用本服务遭受的间接损失、附带损失、营业利润损失不承担赔偿责任。
      </text>
    </view>

    <view class="section">
      <text class="h2">八、协议变更</text>
      <text class="p">
        我们可能随时修订本协议。修订后的协议将在小程序内公示,自公示之日起 7 日后生效。如您不同意修订内容,可停止使用并申请注销账号。继续使用视为接受修订。
      </text>
    </view>

    <view class="section">
      <text class="h2">九、争议解决</text>
      <text class="p">
        1. 本协议适用中华人民共和国法律。
      </text>
      <text class="p">
        2. 因本协议或本服务产生的争议,双方应首先友好协商解决;协商不成的,任何一方均可向 BiuMind 运营方所在地有管辖权的人民法院提起诉讼。
      </text>
    </view>

    <view class="section">
      <text class="h2">十、联系我们</text>
      <text class="p">
        如您对本协议有任何疑问或建议,请联系:
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
