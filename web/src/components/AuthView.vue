<script setup lang="ts">
import {ref} from 'vue'
import {signIn,register} from '../auth'
const mode=ref<'login'|'register'>('login')
const username=ref(''),password=ref(''),displayName=ref(''),error=ref(''),busy=ref(false)
async function submit(){
  busy.value=true;error.value=''
  try{
    if(mode.value==='login')await signIn(username.value,password.value)
    else await register(username.value,password.value,displayName.value)
    password.value=''
  }catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
</script>
<template>
  <main class="auth-shell">
    <section class="auth-intro"><div class="brand"><span class="brand-symbol">炉</span><span>炉前<small>FURNACE TRACE</small></span></div><h1>让每一次入炉<br>都有迹可循。</h1><p>现场状态、过程追溯与属于你的通知。<br>登录后，在个人通知中配对 Telegram。</p><span class="outline-label">入炉追溯工作台</span></section>
    <section class="auth-card panel"><div class="eyebrow">YOUR WORKSPACE</div><h2>{{mode==='login'?'登录工作台':'创建你的账户'}}</h2><p class="muted auth-help">{{mode==='login'?'使用账户继续访问生产视图。':'注册后即可查看系统，并设置个人通知。'}}</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false"/>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名"><el-input v-model="username" aria-label="用户名" autocomplete="username" placeholder="3–32 位字母、数字或下划线" maxlength="32"/></el-form-item>
        <el-form-item v-if="mode==='register'" label="显示名称"><el-input v-model="displayName" aria-label="显示名称" autocomplete="name" placeholder="你的名字" maxlength="50"/></el-form-item>
        <el-form-item label="密码"><el-input v-model="password" aria-label="密码" type="password" show-password :autocomplete="mode==='login'?'current-password':'new-password'" placeholder="至少 8 个字符" maxlength="72"/></el-form-item>
        <el-button type="primary" native-type="submit" :loading="busy" class="auth-submit">{{mode==='login'?'登录':'创建账户'}}</el-button>
      </el-form>
      <p class="auth-switch">{{mode==='login'?'还没有账户？':'已经有账户？'}} <button @click="mode=mode==='login'?'register':'login';error=''">{{mode==='login'?'创建账户':'返回登录'}}</button></p>
      <p class="small muted">管理员账户请联系系统负责人。</p>
    </section>
  </main>
</template>
