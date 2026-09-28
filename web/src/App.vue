<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {get,formatTime,type Furnace,type Snapshot,APIError} from './api/client'
import {currentUser,refreshSession,signOut} from './auth'
import CatalogView from './components/CatalogView.vue'
import AlarmRulesView from './components/AlarmRulesView.vue'
import {catalogVersion,rulesVersion,liveAlarmCount} from './liveVersions'
import SettingsView from './components/SettingsView.vue'
import UsersView from './components/UsersView.vue'
import AuthView from './components/AuthView.vue'
import LiveStation from './components/LiveStation.vue'
import HistoryView from './components/HistoryView.vue'
import AlarmsView from './components/AlarmsView.vue'
import PersonalNotifications from './components/PersonalNotifications.vue'

const tab=ref('live'),furnaces=ref<Furnace[]>([]),selected=ref('')
const connected=ref(false),last=ref(''),alarmCount=ref(0),error=ref(''),authReady=ref(false)
const current=computed(()=>furnaces.value.find(f=>f.furnaceId===selected.value)??furnaces.value[0])
const titles:Record<string,string>={live:'入炉工作台',history:'历史追溯',alarms:'报警与通知',personal:'个人通知',settings:'设置',users:'用户管理',catalog:'料筐与批次',rules:'报警规则'}
let source:EventSource|undefined
function requireLogin(){currentUser.value=null}
function connect(){
  source?.close()
  source=new EventSource('/api/v1/events')
  source.addEventListener('state',event=>{
    const data=JSON.parse((event as MessageEvent).data) as Snapshot
    furnaces.value=data.furnaces;last.value=data.at;alarmCount.value=data.activeAlarmCount;connected.value=true;error.value='';catalogVersion.value=data.catalogVersion;rulesVersion.value=data.rulesVersion;liveAlarmCount.value=data.activeAlarmCount
  })
  source.onerror=()=>{
    connected.value=false;error.value='实时连接中断，正在重新连接。设备显示为最后接收状态。'
    void refreshSession().catch(()=>{})
  }
}
watch(currentUser,(user,old)=>{
  if(!user){source?.close();source=undefined;furnaces.value=[];tab.value='live';return}
  if(user.id===old?.id)return
  void get<Furnace[]>('/furnaces').then(items=>furnaces.value=items).catch(e=>error.value=e.message)
  connect()
})
onMounted(async()=>{
  window.addEventListener('auth-required',requireLogin)
  try{await refreshSession()}catch(e){if(!(e instanceof APIError&&e.status===401))error.value='服务暂不可用，请稍后重试'}finally{authReady.value=true}
})
onBeforeUnmount(()=>{source?.close();window.removeEventListener('auth-required',requireLogin)})
async function logout(){try{await signOut()}catch(e){error.value=(e as Error).message}}
</script>
<template>
  <div v-if="!authReady" class="initial-loading">正在连接工作台…</div>
  <AuthView v-else-if="!currentUser"/>
  <div v-else class="app-shell">
    <aside class="sidebar">
      <a class="brand" href="/"><span class="brand-symbol">炉</span><span>炉前<small>FURNACE TRACE</small></span></a>
      <div class="sidebar-label">工作空间</div>
      <nav aria-label="主导航">
        <button :class="{selected:tab==='live'}" @click="tab='live'"><span>▦</span>入炉工作台</button>
        <button :class="{selected:tab==='history'}" @click="tab='history'"><span>≡</span>历史追溯</button>
        <button :class="{selected:tab==='alarms'}" @click="tab='alarms'"><span>◉</span>报警与通知<i v-if="alarmCount">{{alarmCount}}</i></button>
        <button :class="{selected:tab==='personal'}" @click="tab='personal'"><span>↗</span>个人通知</button>
        <button :class="{selected:tab==='catalog'}" @click="tab='catalog'"><span>▤</span>料筐与批次</button>
        <button :class="{selected:tab==='rules'}" @click="tab='rules'"><span>◇</span>报警规则</button>
        <button :class="{selected:tab==='settings'}" @click="tab='settings'"><span>⚙</span>设置</button>
        <button v-if="currentUser.role==='admin'" :class="{selected:tab==='users'}" @click="tab='users'"><span>♙</span>用户管理</button>
      </nav>
      <div class="sidebar-footer"><span class="outline-label">模拟环境</span><p>真实协议 · 设备模拟</p><a v-if="currentUser.role==='admin'" href="http://localhost:31081" target="_blank" rel="noopener">打开 FUXA 现场画面 ↗</a></div>
    </aside>
    <div class="main-shell">
      <header class="topbar"><div><span class="muted">生产追溯</span><span class="separator">/</span><strong>{{titles[tab]}}</strong></div><div class="header-account"><div class="connection"><span :class="['status-dot',connected?'ok':'bad']"></span>{{connected?'实时同步':'重新连接中'}}</div><span>{{currentUser.displayName}}<small class="role-label">{{currentUser.role==='admin'?'管理员':'用户'}}</small></span><button @click="logout">退出</button></div></header>
      <main>
        <div v-if="error" class="connection-warning" role="status">{{error}}</div>
        <template v-if="tab==='live'"><div class="page-title"><div><div class="eyebrow">OPERATIONS / 入炉管控</div><h1>入炉工作台</h1><p>识别、校验、执行与归档，跟踪每一次入炉。</p></div><el-select v-if="furnaces.length>1" v-model="selected" placeholder="选择炉子" aria-label="选择炉子" style="width:190px"><el-option v-for="f in furnaces" :key="f.furnaceId" :value="f.furnaceId" :label="f.name"/></el-select></div><LiveStation v-if="current" :furnace="current" @configure-alarms="tab='rules'"/><el-skeleton v-else :rows="10" animated/></template>
        <HistoryView v-else-if="tab==='history'"/>
        <AlarmsView v-else-if="tab==='alarms'"/>
        <CatalogView v-else-if="tab==='catalog'"/><AlarmRulesView v-else-if="tab==='rules'"/><SettingsView v-else-if="tab==='settings'"/><UsersView v-else-if="tab==='users'&&currentUser.role==='admin'"/><PersonalNotifications v-else/>
      </main>
      <footer class="app-footer"><span>炉前 · 入炉追溯</span><span>{{last?formatTime(last):'等待数据'}} · 模拟 RTSP 输入</span></footer>
    </div>
  </div>
</template>
