<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { APIError, call, get, type Basket, type BasketPage, type User } from './api/client'

const user = ref<User | null>(null)
const username = ref('')
const password = ref('')
const baskets = ref<Basket[]>([])
const selected = ref('')
const busy = ref(false)
const error = ref('')
const imageError = ref(false)
const display = ref<HTMLElement>()
const current = computed(() => baskets.value.find(b => b.basketNo === selected.value))
const quality: Record<string, string> = {passed:'合格', pending:'待检', failed:'不合格'}

function expired() { user.value = null; selected.value = ''; baskets.value = [] }
function report(e: unknown) {
  if (e instanceof APIError && e.status === 401) expired()
  error.value = e instanceof Error ? e.message : '读取失败，请重试'
}
async function load() {
  busy.value = true; error.value = ''
  try {
    const items: Basket[] = []
    let page = 1
    while (true) {
      const result = await get<BasketPage>(`/baskets?page=${page++}&pageSize=100`)
      items.push(...result.items)
      if (!result.items.length || items.length >= result.pagination.total) break
    }
    baskets.value = items
    if (!items.some(b => b.basketNo === selected.value)) selected.value = ''
  } catch (e) { report(e) }
  finally { busy.value = false }
}
async function login() {
  busy.value = true; error.value = ''
  try {
    await call('POST', '/auth/login', {username:username.value, password:password.value})
    password.value = ''
    user.value = await get<User>('/me')
    await load()
  } catch (e) { report(e) }
  finally { busy.value = false }
}
async function fullscreen() {
  try { await display.value?.requestFullscreen() }
  catch { error.value = '浏览器未允许全屏，可以使用浏览器自身的全屏功能。' }
}
async function exitFullscreen() { await document.exitFullscreen().catch(() => {}) }
onMounted(async () => {
  window.addEventListener('auth-required', expired)
  busy.value = true
  try { user.value = await get<User>('/me'); await load() }
  catch (e) { if (!(e instanceof APIError && e.status === 401)) report(e) }
  finally { busy.value = false }
})
onUnmounted(() => window.removeEventListener('auth-required', expired))
</script>

<template>
  <main>
    <header>
      <div><p class="eyebrow">炉前 / 摄像头演示</p><h1>料筐二维码</h1></div>
      <span v-if="user" class="muted">{{ user.displayName }}</span>
    </header>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <form v-if="!user" class="login" @submit.prevent="login">
      <h2>使用工作台账号登录</h2>
      <label for="username">用户名</label>
      <input id="username" v-model="username" autocomplete="username" required>
      <label for="password">密码</label>
      <input id="password" v-model="password" type="password" autocomplete="current-password" required>
      <button class="primary" :disabled="busy">{{ busy ? '正在连接…' : '登录' }}</button>
    </form>
    <template v-else>
      <section class="controls" aria-label="二维码选择">
        <div class="picker">
          <label for="basket">选择料筐</label>
          <select id="basket" v-model="selected" :disabled="busy" @change="imageError = false">
            <option value="">不显示二维码</option>
            <option v-for="b in baskets" :key="b.basketNo" :value="b.basketNo">
              {{ b.basketNo }} · {{ b.batchNo }} · {{ b.materialName }}
            </option>
          </select>
        </div>
        <button :disabled="busy" @click="load">{{ busy ? '读取中…' : '刷新列表' }}</button>
        <button :disabled="!selected" @click="selected = ''">清空画面</button>
        <button class="primary" :disabled="!selected || imageError" @click="fullscreen">全屏展示</button>
      </section>
      <p v-if="!busy && !baskets.length" class="muted">暂无料筐，请先在工作台「料筐与批次」中添加，再刷新列表。</p>
      <p class="hint">选择后让摄像头对准下方二维码，识别后自动校验入炉；同一码持续停留只触发一次。</p>
      <section ref="display" class="display" aria-label="摄像头扫码区域">
        <button class="exit-fullscreen" @click="exitFullscreen">退出全屏</button>
        <template v-if="current">
          <img v-if="!imageError" :key="selected" :src="`/api/v1/baskets/${encodeURIComponent(selected)}/qrcode`" :alt="`料筐 ${selected} 的二维码`" @error="imageError = true">
          <p v-else role="alert">二维码加载失败，请刷新页面后重试。</p>
          <strong class="basket-number">{{ selected }}</strong>
          <p class="details">{{ current.batchNo }} · {{ current.materialName }} · {{ quality[current.qualityStatus] || current.qualityStatus }}<template v-if="current.occupied"> · 已占用</template><template v-if="!current.enabled || !current.batchEnabled"> · 已停用</template></p>
        </template>
        <p v-else class="empty">请选择一个料筐</p>
      </section>
    </template>
  </main>
</template>

<style>
:root{font-family:Inter,"PingFang SC","Microsoft YaHei",sans-serif;color:#223b40;background:#f4f7f7;font-synthesis:none;--accent:#137e73;--muted:#667c82;--border:#d6e1e3}
*{box-sizing:border-box}body{margin:0}main{max-width:1080px;margin:auto;padding:24px}header{display:flex;align-items:center;justify-content:space-between;margin-bottom:24px}.eyebrow{font-size:12px;color:var(--accent);margin:0 0 8px;letter-spacing:2px}h1{font-size:28px;margin:0}h2{font-size:20px;margin:0 0 16px}.muted,.hint{color:var(--muted);font-size:14px}.hint{line-height:1.7;margin:16px 0}.controls{display:flex;gap:12px;align-items:end;flex-wrap:wrap}.picker{flex:1;min-width:240px}label{display:block;font-size:14px;font-weight:600;margin-bottom:8px}input,select,button{font:inherit;border:1px solid var(--border);border-radius:6px;background:white;min-height:44px;padding:10px 16px;color:inherit}input,select{width:100%}button{cursor:pointer;font-size:14px;white-space:nowrap}.primary{background:var(--accent);border-color:var(--accent);color:white}button:disabled{opacity:.5;cursor:default}:focus-visible{outline:3px solid #61b4a9;outline-offset:3px}.display{background:white;min-height:420px;border:1px solid var(--border);border-radius:8px;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:24px;gap:8px}.display img{display:block;width:min(100%,480px);height:auto;aspect-ratio:1;image-rendering:pixelated}.basket-number{font:600 32px ui-monospace,monospace;letter-spacing:4px;color:#111}.details{font-size:14px;color:var(--muted);text-align:center;margin:8px 0}.empty{color:var(--muted)}.display:fullscreen{border:0;border-radius:0;width:100%;height:100%;padding:24px}.display:fullscreen img{width:min(80vw,calc(100dvh - 160px));max-height:calc(100dvh - 160px)}.login{max-width:360px;margin:48px auto}.login input{margin-bottom:20px}.login button{width:100%}.error{background:#fff0ed;color:#a33426;padding:12px 16px;border-radius:6px}@media(max-width:540px){main{padding:16px}.picker{flex-basis:100%}.controls button{flex:1}.display{min-height:340px;padding:16px}.basket-number{font-size:26px}}
.exit-fullscreen{display:none}.display:fullscreen .exit-fullscreen{display:block;position:absolute;top:16px;right:16px}
</style>
