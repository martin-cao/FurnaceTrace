<script setup lang="ts">
import {onMounted,ref} from 'vue'
import {ElMessage,ElMessageBox} from 'element-plus'
import {call,get,formatTime,type TelegramState,type Preferences,type Furnace} from '../api/client'
import {currentUser} from '../auth'
const state=ref<TelegramState>(),furnaces=ref<Furnace[]>([]),preferences=ref<Preferences>()
const code=ref(''),busy=ref(false),loading=ref(true),error=ref('')
const categories=[{value:'scan',label:'扫码异常',hint:'扫码超时、码不清晰或需要人工补录'},{value:'mes',label:'MES 校验异常',hint:'料筐不存在、质量异常或 MES 通信失败'},{value:'device',label:'设备连接异常',hint:'摄像头、SCADA 和设备离线或重启'},{value:'workflow',label:'入炉流程异常',hint:'开关门、入炉到位和复位超时'},{value:'temperature',label:'炉温超限',hint:'炉温超过可配置上限，或恢复到正常范围'},{value:'rules',label:'自定义规则',hint:'自定义组合条件触发、定时提醒与恢复'}] as const
async function load(){
  loading.value=true;error.value=''
  try{const [binding,items]=await Promise.all([get<TelegramState>('/me/telegram'),get<Furnace[]>('/furnaces')]);state.value=binding;preferences.value=structuredClone(binding.preferences);furnaces.value=items}catch(e){error.value=(e as Error).message}finally{loading.value=false}
}
async function pair(){
  busy.value=true;error.value=''
  try{state.value=await call<TelegramState>('POST','/me/telegram/pair',{code:code.value});code.value='';ElMessage.success('Telegram 配对成功')}catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
async function unpair(){
  try{await ElMessageBox.confirm('解绑会取消尚未发送的个人通知，已开始发送的消息可能仍会到达。','解除 Telegram 绑定',{confirmButtonText:'解绑',cancelButtonText:'取消'});await call('DELETE','/me/telegram');await load();ElMessage.success('已解绑')}catch(e){if(e instanceof Error)error.value=e.message}
}
async function save(){
  if(!preferences.value)return
  busy.value=true;error.value=''
  try{preferences.value=await call<Preferences>('PUT','/me/notification-preferences',preferences.value);ElMessage.success('个人通知偏好已保存')}catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
onMounted(load)
</script>
<template>
  <div class="section-title"><div><div class="eyebrow">PERSONAL NOTIFICATIONS</div><h1>个人通知</h1><p>{{currentUser?.displayName}}，选择你关心的炉子与事件。</p></div><el-button @click="load">刷新状态</el-button></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="settings-error"/>
  <el-skeleton v-if="loading" :rows="7" animated/>
  <div v-else-if="state&&preferences" class="settings-grid">
    <section class="panel pairing-panel"><div class="panel-heading"><h2>Telegram 配对</h2><span :class="['pill',state.binding?'good':'neutral']">{{state.binding?'已绑定':'未绑定'}}</span></div>
      <div class="settings-body" v-if="state.binding"><div class="linked-account"><span class="telegram-symbol">↗</span><div><strong>{{state.binding.displayName}}</strong><p class="small muted">绑定于 {{formatTime(state.binding.pairedAt)}}</p></div></div><p class="muted">通知会发送到你与 Bot 的私聊。其他用户看不到你的绑定或投递记录。</p><el-button @click="unpair">解除绑定</el-button></div>
      <div class="settings-body" v-else><ol class="pairing-steps"><li><span>01</span><div><strong>打开同一个 Bot</strong><p v-if="state.botUsername"><a class="text-link" :href="'https://t.me/'+encodeURIComponent(state.botUsername)" target="_blank" rel="noopener noreferrer">@{{state.botUsername}} ↗</a></p><p v-else class="muted">{{state.botConfigured?'Bot 正在连接，请稍后刷新。':'Bot 尚未配置，请联系管理员。'}}</p></div></li><li><span>02</span><div><strong>私聊发送 /start</strong><p class="muted">Bot 会回复一个 10 分钟内有效的配对码。</p></div></li><li><span>03</span><div><strong>填写配对码</strong><form @submit.prevent="pair"><el-input v-model="code" aria-label="Telegram 配对码" placeholder="ABCD-EFGH" maxlength="20" class="pair-code"/><el-button type="primary" native-type="submit" :loading="busy" :disabled="!state.botConfigured||code.trim().length<8">完成配对</el-button></form></div></li></ol></div>
      <div class="settings-footer"><span :class="['status-dot',state.botOnline?'ok':'bad']"></span>{{state.botOnline?'Bot 连接正常':'Bot 连接暂不可用'}}<span class="muted">配对码仅用于绑定本人账户</span></div>
    </section>
    <section class="panel preferences-panel"><div class="panel-heading"><h2>通知偏好</h2><el-switch v-model="preferences.enabled" active-text="启用通知" aria-label="启用通知"/></div>
      <div class="settings-body"><h3>关注的炉子</h3><el-checkbox-group v-model="preferences.furnaceIds" class="furnace-options"><el-checkbox v-for="f in furnaces" :key="f.furnaceId" :value="f.furnaceId">{{f.name}}</el-checkbox></el-checkbox-group><p class="small muted">没有选中任何炉子时，不发送通知。</p><h3 class="category-heading">通知类别</h3><el-checkbox-group v-model="preferences.categories" class="category-options"><el-checkbox v-for="item in categories" :key="item.value" :value="item.value"><strong>{{item.label}}</strong><span>{{item.hint}}</span></el-checkbox></el-checkbox-group><div class="recovery-option"><div><strong>故障恢复通知</strong><p class="small muted">所选类别的故障解除后，再通知我。</p></div><el-switch v-model="preferences.recoveries" aria-label="故障恢复通知"/></div><p v-if="!state.binding" class="small muted">可以先保存偏好，完成配对后才会接收通知。</p><el-button type="primary" :loading="busy" @click="save">保存偏好</el-button></div>
    </section>
  </div>
</template>
