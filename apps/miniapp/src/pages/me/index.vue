<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { get, patch } from '@/data/api/client';
import { isLoggedIn, clearTokens } from '@/core/token_manager';
import {
  loadPreferredModel,
  savePreferredModel,
} from '@/lib/preferred_model';
import { modelDisplayName } from '@/lib/provider_catalog';
import { loadModelEntries, type ModelEntry } from '@/data/api/models';
import {
  applyFontScale,
  useFontScale,
  useFontScaleStyle,
  labelFor as fontScaleLabel,
  FONT_SCALES,
} from '@/lib/font_scale';
import { clearLocalCache, formatBytes } from '@/lib/cache_clear';

interface MeUser {
  id: string;
  email: string;
  display_name: string;
}

interface ProviderRow {
  id: string;
  provider: string;
  provider_user_id: string;
  nickname?: string;
  avatar_url?: string;
  bound_at: string;
  last_login_at?: string;
}

const me = ref<MeUser | null>(null);
const providers = ref<ProviderRow[]>([]);
const loading = ref(false);
const err = ref('');

// 头像 URL 客户端本地存 — 微信 chooseAvatar 返回设备临时 URL,
// 跨设备同步要先上 OSS (W4-W5 接). 当前仅本机展示.
const AVATAR_KEY = 'biumind.avatar_url';
const avatarUrl = ref<string>('');

// 编辑模式
const editing = ref(false);
const editName = ref('');
const editAvatar = ref('');
const saving = ref(false);

// ── 设置中心 v2 ───────────────────────────────────────────────────
const preferredModel = ref<string>(loadPreferredModel());
const fontScale = useFontScale();
const fontStyle = useFontScaleStyle();
const modelEntries = ref<ModelEntry[]>([]);

const modelDisplay = computed(() => {
  if (preferredModel.value === 'biumind-default') return 'BiuMind';
  return modelDisplayName(preferredModel.value);
});

async function reloadModelEntriesIfNeeded() {
  if (modelEntries.value.length > 0) return;
  try {
    modelEntries.value = await loadModelEntries();
  } catch {
    /* noop — picker 时如果空再 fallback */
  }
}

async function onChooseModel() {
  await reloadModelEntriesIfNeeded();
  if (modelEntries.value.length === 0) {
    uni.showToast({ title: '模型列表加载失败', icon: 'none' });
    return;
  }
  const items = modelEntries.value.map((e) =>
    e.isOfficial ? '✨ ' + e.label + ' (官方)' : e.label + ' · ' + e.providerName,
  );
  uni.showActionSheet({
    itemList: items,
    success: (res) => {
      const sel = modelEntries.value[res.tapIndex];
      if (!sel) return;
      preferredModel.value = sel.modelId;
      savePreferredModel(sel.modelId);
      uni.showToast({ title: '已设为默认', icon: 'success' });
    },
  });
}

function onChooseFontScale() {
  const items = FONT_SCALES.map((s) =>
    s.id === fontScale.value ? '✓ ' + s.label : s.label,
  );
  uni.showActionSheet({
    itemList: items,
    success: (res) => {
      const sel = FONT_SCALES[res.tapIndex];
      if (!sel) return;
      applyFontScale(sel.id); // 响应式 ref + storage 一起更新
      uni.showToast({ title: '已切换到 ' + sel.label, icon: 'none' });
    },
  });
}

function onClearCache() {
  uni.showModal({
    title: '清除本地缓存',
    content: '将清除草稿 / 本地置顶 / 隐私同意状态等. 不会影响云端数据.',
    confirmText: '清除',
    confirmColor: '#ef4444',
    cancelText: '取消',
    success: async (r) => {
      if (!r.confirm) return;
      uni.showLoading({ title: '清除中...', mask: true });
      try {
        const result = await clearLocalCache();
        uni.hideLoading();
        uni.showToast({
          title:
            result.cleared > 0
              ? '已清除 ' + result.cleared + ' 项 · ' + formatBytes(result.bytes || 0)
              : '没有需要清除的',
          icon: 'none',
        });
        // 同步状态 — preferred 直接读 storage; fontScale 走 applyFontScale
        // 让响应式 ref 更新, 否则 me 页字号会跟字号 ref 失去 sync
        preferredModel.value = loadPreferredModel();
        applyFontScale('normal');
      } catch {
        uni.hideLoading();
        uni.showToast({ title: '清除失败', icon: 'none' });
      }
    },
  });
}

function onAbout() {
  uni.navigateTo({ url: '/pages/me/about' });
}

function onQuickNote() {
  uni.navigateTo({ url: '/pages/notes/index' });
}

function onFeedback() {
  // 复制邮箱让用户写邮件 — 也可以未来接入 button open-type=feedback (微信内置)
  uni.setClipboardData({
    data: 'hi@your-biumind.example.com',
    success: () =>
      uni.showToast({
        title: '邮箱已复制, 欢迎来信',
        icon: 'none',
      }),
  });
}

// #ifdef MP-WEIXIN
function onContactCS() {
  // 微信原生客服会话, 由 button open-type=contact 触发, 这里仅占位
}
// #endif

async function loadAll() {
  if (!isLoggedIn()) {
    uni.redirectTo({ url: '/pages/me/login' });
    return;
  }
  loading.value = true;
  err.value = '';
  try {
    const [u, p] = await Promise.all([
      get<MeUser>('/v1/identity/me'),
      get<{ providers: ProviderRow[] }>('/v1/identity/me/providers'),
    ]);
    me.value = u;
    providers.value = p.providers || [];
  } catch (e: unknown) {
    err.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
  // 本地恢复 avatar
  try {
    const v = uni.getStorageSync(AVATAR_KEY);
    if (typeof v === 'string') avatarUrl.value = v;
  } catch {
    /* noop */
  }
}

function providerLabel(p: string): string {
  const m: Record<string, string> = {
    wechat_mp: '微信',
    wechat_oa: '微信公众号',
    wechat_open: '微信开放平台',
    alipay_mp: '支付宝',
    toutiao_mp: '抖音',
    baidu_mp: '百度',
    qq_mp: 'QQ',
    kuaishou_mp: '快手',
    jd_mp: '京东',
    lark_mp: '飞书',
  };
  return m[p] || p;
}

function onLogout() {
  uni.showModal({
    title: '退出登录',
    content: '确定要退出当前账号吗?',
    success: (r) => {
      if (r.confirm) {
        clearTokens();
        uni.redirectTo({ url: '/pages/me/login' });
      }
    },
  });
}

function startEdit() {
  editName.value = me.value?.display_name || '';
  editAvatar.value = avatarUrl.value;
  editing.value = true;
}

function cancelEdit() {
  editing.value = false;
}

// 微信 chooseAvatar 返回事件 (button open-type=chooseAvatar)
// 仅在 mp-weixin 触发, 其他平台不绑.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function onChooseAvatar(e: any) {
  const url = e?.detail?.avatarUrl as string | undefined;
  if (url) {
    editAvatar.value = url;
  }
}

// 微信 input type=nickname 在用户提交时触发 blur, value 同步到 v-model
// 这里没额外逻辑 — vue v-model="editName" 已 handle.

async function saveProfile() {
  const name = editName.value.trim();
  if (!name) {
    uni.showToast({ title: '请输入昵称', icon: 'none' });
    return;
  }
  saving.value = true;
  try {
    // 1) 后端: 同步 display_name (avatar 仍本地)
    if (name !== (me.value?.display_name || '')) {
      const r = await patch<{ display_name: string }, { user: MeUser }>(
        '/v1/identity/me/profile',
        { display_name: name },
      );
      me.value = r.user;
    }
    // 2) 本地: 头像 URL 落 storage
    if (editAvatar.value && editAvatar.value !== avatarUrl.value) {
      avatarUrl.value = editAvatar.value;
      try {
        uni.setStorageSync(AVATAR_KEY, editAvatar.value);
      } catch {
        /* storage full — 内存仍 OK 至 app 退出 */
      }
    }
    editing.value = false;
    uni.showToast({ title: '已保存', icon: 'success' });
  } catch (e: unknown) {
    uni.showToast({
      title: e instanceof Error ? e.message : '保存失败',
      icon: 'none',
    });
  } finally {
    saving.value = false;
  }
}

onMounted(loadAll);
</script>

<template>
  <view class="me" :style="fontStyle">
    <!-- 个人信息卡 -->
    <view class="profile">
      <image
        v-if="avatarUrl"
        class="avatar"
        :src="avatarUrl"
        mode="aspectFill"
      />
      <view v-else class="avatar avatar-placeholder">
        <text class="avatar-letter">
          {{ (me?.display_name || 'B').charAt(0).toUpperCase() }}
        </text>
      </view>
      <view class="profile-text">
        <text class="profile-name">{{ me?.display_name || '加载中...' }}</text>
        <text class="profile-email" v-if="me && !me.email.includes('@no-mail')">
          {{ me.email }}
        </text>
      </view>
      <text class="profile-edit" @tap="startEdit">编辑</text>
    </view>

    <!-- 功能入口 -->
    <view class="section">
      <text class="section-title">功能</text>
      <view class="row clickable" hover-class="row-hover" @tap="onQuickNote">
        <text class="row-label">笔记速记</text>
        <text class="row-arrow">›</text>
      </view>
    </view>

    <!-- 已绑定账号 -->
    <view class="section">
      <text class="section-title">已绑定账号</text>
      <view v-if="loading" class="hint">加载中...</view>
      <view v-else-if="err" class="error">{{ err }}</view>
      <view v-else>
        <view v-for="p in providers" :key="p.id" class="row">
          <text class="row-label">{{ providerLabel(p.provider) }}</text>
          <text class="row-value">
            {{ p.nickname || p.provider_user_id.slice(0, 8) + '***' }}
          </text>
        </view>
        <view v-if="providers.length === 0" class="hint">暂无绑定</view>
      </view>
    </view>

    <!-- ── 设置中心 v2 ── -->
    <view class="section">
      <text class="section-title">偏好设置</text>
      <view class="row clickable" hover-class="row-hover" @tap="onChooseModel">
        <text class="row-label">默认模型</text>
        <view class="row-right">
          <text class="row-value">{{ modelDisplay }}</text>
          <text class="row-arrow">›</text>
        </view>
      </view>
      <view class="row clickable" hover-class="row-hover" @tap="onChooseFontScale">
        <text class="row-label">字号</text>
        <view class="row-right">
          <text class="row-value">{{ fontScaleLabel(fontScale) }}</text>
          <text class="row-arrow">›</text>
        </view>
      </view>
    </view>

    <view class="section">
      <text class="section-title">数据 & 支持</text>
      <view class="row clickable" hover-class="row-hover" @tap="onClearCache">
        <text class="row-label">清除本地缓存</text>
        <text class="row-arrow">›</text>
      </view>
      <view class="row clickable" hover-class="row-hover" @tap="onFeedback">
        <text class="row-label">意见反馈</text>
        <text class="row-arrow">›</text>
      </view>
      <!-- #ifdef MP-WEIXIN -->
      <button class="row clickable row-button" hover-class="row-hover" open-type="contact" @tap="onContactCS">
        <text class="row-label">在线客服</text>
        <text class="row-arrow">›</text>
      </button>
      <!-- #endif -->
      <view class="row clickable" hover-class="row-hover" @tap="onAbout">
        <text class="row-label">关于 BiuMind</text>
        <text class="row-arrow">›</text>
      </view>
    </view>

    <button class="btn-logout" @tap="onLogout">退出登录</button>

    <!-- 编辑资料 modal-like 覆盖层 -->
    <view v-if="editing" class="overlay" @tap="cancelEdit">
      <view class="dialog" @tap.stop>
        <text class="dialog-title">完善资料</text>

        <!-- 微信原生 chooseAvatar — 仅 mp-weixin 真生效;
             其他平台 button 退化为普通按钮, 用户在 input 改昵称即可 -->
        <view class="avatar-edit">
          <!-- #ifdef MP-WEIXIN -->
          <button
            class="avatar-btn"
            open-type="chooseAvatar"
            @chooseavatar="onChooseAvatar"
          >
            <image
              v-if="editAvatar"
              class="avatar-edit-img"
              :src="editAvatar"
              mode="aspectFill"
            />
            <view v-else class="avatar-edit-placeholder">
              <text class="avatar-edit-hint">点击选择头像</text>
            </view>
          </button>
          <!-- #endif -->
          <!-- #ifndef MP-WEIXIN -->
          <view class="avatar-edit-placeholder">
            <text class="avatar-edit-hint">头像功能仅微信小程序支持</text>
          </view>
          <!-- #endif -->
        </view>

        <view class="form-row">
          <text class="form-label">昵称</text>
          <!-- #ifdef MP-WEIXIN -->
          <input
            v-model="editName"
            class="form-input"
            type="nickname"
            placeholder="请输入昵称"
            maxlength="32"
          />
          <!-- #endif -->
          <!-- #ifndef MP-WEIXIN -->
          <input
            v-model="editName"
            class="form-input"
            placeholder="请输入昵称"
            maxlength="32"
          />
          <!-- #endif -->
        </view>

        <view class="dialog-actions">
          <button class="btn-cancel" @tap="cancelEdit">取消</button>
          <button class="btn-save" :disabled="saving" @tap="saveProfile">
            {{ saving ? '保存中...' : '保存' }}
          </button>
        </view>
      </view>
    </view>
  </view>
</template>

<style lang="scss" scoped>
.me {
  padding: 32rpx;
}
.profile {
  display: flex;
  align-items: center;
  background: #fff;
  border-radius: 16rpx;
  padding: 32rpx;
  margin-bottom: 32rpx;
}
.avatar {
  width: 96rpx;
  height: 96rpx;
  border-radius: 50%;
  margin-right: 24rpx;
  flex-shrink: 0;
}
.avatar-placeholder {
  background: #3b82f6;
  display: flex;
  align-items: center;
  justify-content: center;
}
.avatar-letter {
  color: #fff;
  font-size: 48rpx;
  font-weight: 600;
}
.profile-text {
  flex: 1;
  display: flex;
  flex-direction: column;
}
.profile-name {
  font-size: 32rpx;
  color: #1f2937;
  font-weight: 600;
}
.profile-email {
  margin-top: 8rpx;
  font-size: 24rpx;
  color: #6b7280;
}
.profile-edit {
  font-size: 26rpx;
  color: #3b82f6;
  padding: 16rpx;
}

.section {
  background: #fff;
  border-radius: 16rpx;
  padding: 32rpx;
  margin-bottom: 32rpx;
}
.section-title {
  font-size: 28rpx;
  color: #6b7280;
  margin-bottom: 24rpx;
  display: block;
}
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24rpx 0;
  border-bottom: 1px solid #f3f4f6;
}
.row.clickable {
  cursor: pointer;
}
.row-hover {
  background: #f8fafc;
  margin: 0 -16rpx;
  padding: 24rpx 16rpx;
  border-radius: 8rpx;
}
.row-button {
  // 复用 row 视觉但 button 标签默认带样式, reset 一下
  background: transparent;
  border: none;
  width: 100%;
  text-align: left;
  padding-left: 0;
  padding-right: 0;
  line-height: normal;
}
.row-button::after {
  border: none;
}
.row:last-child {
  border-bottom: none;
}
.row-label {
  color: #1f2937;
  font-size: calc(30rpx * var(--font-scale, 1));
}
.row-right {
  display: flex;
  align-items: center;
  gap: 8rpx;
}
.row-value {
  color: #6b7280;
  font-size: calc(28rpx * var(--font-scale, 1));
}
.row-arrow {
  color: #cbd5e1;
  font-size: 28rpx;
  margin-left: 8rpx;
}
.hint {
  color: #9ca3af;
  font-size: 28rpx;
  text-align: center;
  padding: 32rpx;
}
.error {
  color: #ef4444;
  font-size: 26rpx;
}
.btn-logout {
  width: 100%;
  height: 88rpx;
  background-color: #fff;
  color: #ef4444;
  border-radius: 12rpx;
  font-size: 30rpx;
  margin-top: 32rpx;
}

/* 编辑资料 dialog */
.overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}
.dialog {
  width: 600rpx;
  background: #fff;
  border-radius: 16rpx;
  padding: 48rpx 32rpx;
  display: flex;
  flex-direction: column;
}
.dialog-title {
  font-size: 32rpx;
  font-weight: 600;
  color: #1f2937;
  text-align: center;
  margin-bottom: 32rpx;
}
.avatar-edit {
  display: flex;
  justify-content: center;
  margin-bottom: 32rpx;
}
.avatar-btn {
  width: 144rpx;
  height: 144rpx;
  padding: 0;
  background: transparent;
  border: none;
  border-radius: 50%;
  overflow: hidden;
  line-height: normal;
}
.avatar-btn::after {
  border: none;
}
.avatar-edit-img {
  width: 144rpx;
  height: 144rpx;
  border-radius: 50%;
}
.avatar-edit-placeholder {
  width: 144rpx;
  height: 144rpx;
  border-radius: 50%;
  background: #f3f4f6;
  display: flex;
  align-items: center;
  justify-content: center;
}
.avatar-edit-hint {
  font-size: 22rpx;
  color: #6b7280;
  text-align: center;
}
.form-row {
  display: flex;
  flex-direction: column;
  margin-bottom: 32rpx;
}
.form-label {
  font-size: 26rpx;
  color: #6b7280;
  margin-bottom: 12rpx;
}
.form-input {
  height: 80rpx;
  padding: 0 24rpx;
  border: 1px solid #d1d5db;
  border-radius: 12rpx;
  font-size: 30rpx;
}
.dialog-actions {
  display: flex;
  gap: 24rpx;
}
.btn-cancel,
.btn-save {
  flex: 1;
  height: 80rpx;
  line-height: 80rpx;
  border-radius: 12rpx;
  font-size: 28rpx;
}
.btn-cancel {
  background: #f3f4f6;
  color: #1f2937;
}
.btn-save {
  background: #3b82f6;
  color: #fff;
}
.btn-save[disabled] {
  opacity: 0.6;
}
</style>
