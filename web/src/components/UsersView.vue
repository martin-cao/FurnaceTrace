<script setup lang="ts">
import {onMounted,ref} from 'vue'
import {ElMessage,ElMessageBox} from 'element-plus'
import {call,get,formatTime,type ManagedUser,type ManagedUserPage} from '../api/client'
import {currentUser} from '../auth'
const users=ref<ManagedUser[]>([]),total=ref(0),page=ref(1),error=ref(''),busy=ref('')
async function load(){try{const result=await get<ManagedUserPage>(`/users?page=${page.value}&pageSize=20`);users.value=result.items;total.value=result.pagination.total;error.value=''}catch(e){error.value=(e as Error).message}}
async function save(u:ManagedUser){try{await ElMessageBox.confirm(`将 ${u.username} 设为${u.role==='admin'?'管理员':'普通用户'}，${u.enabled?'启用':'停用'}账户。角色或状态改变后，对方需要重新登录。`,'更新用户权限',{confirmButtonText:'保存更改',cancelButtonText:'取消'});busy.value=u.id;const updated=await call<ManagedUser>('PUT',`/users/${u.id}/access`,{role:u.role,enabled:u.enabled});users.value=users.value.map(item=>item.id===updated.id?updated:item);ElMessage.success('用户权限已更新')}catch(e){if(e instanceof Error)ElMessage.error(e.message)}finally{busy.value=''}}
onMounted(load)
</script>
<template>
 <div class="section-title"><div><div class="eyebrow">USER MANAGEMENT</div><h1>用户管理</h1><p>管理账户角色与启用状态；权限由后端执行校验。</p></div><el-button @click="load">刷新列表</el-button></div>
 <el-alert v-if="error" :title="error" type="error" :closable="false"/>
 <section class="panel table-panel"><el-table :data="users" empty-text="暂无用户"><el-table-column label="账户" min-width="180"><template #default="{row}"><strong>{{row.displayName}}</strong><p class="small muted">{{row.username}} {{row.id===currentUser?.id?'· 当前账户':''}}</p></template></el-table-column><el-table-column label="角色" min-width="170"><template #default="{row}"><el-select v-model="row.role" :disabled="row.id===currentUser?.id||!!busy" :aria-label="row.username+' 的角色'"><el-option label="普通用户" value="user"/><el-option label="管理员" value="admin"/></el-select></template></el-table-column><el-table-column label="启用" width="100"><template #default="{row}"><el-switch v-model="row.enabled" :disabled="row.id===currentUser?.id||!!busy" :aria-label="row.username+' 账户启用'"/></template></el-table-column><el-table-column label="注册时间" min-width="180"><template #default="{row}">{{formatTime(row.createdAt)}}</template></el-table-column><el-table-column label="操作" width="120"><template #default="{row}"><el-button :disabled="row.id===currentUser?.id||!!busy" :loading="busy===row.id" @click="save(row)">保存更改</el-button></template></el-table-column></el-table><div class="pagination"><span class="muted">共 {{total}} 个账户</span><el-pagination v-model:current-page="page" :total="total" :page-size="20" layout="prev,pager,next" @current-change="load"/></div></section>
 <section class="panel roles"><h2>角色权限说明</h2><table><thead><tr><th>功能</th><th>普通用户</th><th>管理员</th></tr></thead><tbody><tr><td>查看现场、视频、入炉历史和报警</td><td>允许</td><td>允许</td></tr><tr><td>本人账户、密码和 Telegram 通知</td><td>允许</td><td>允许</td></tr><tr><td>补录、复位、确认报警、台账与报警规则</td><td>无权限</td><td>允许</td></tr><tr><td>摄像头 RTSP、FUXA 和用户管理</td><td>无权限</td><td>允许</td></tr></tbody></table><p>新用户通过登录页自主注册，默认普通用户。不能修改自己的角色或停用自己；系统保留至少一名启用的管理员。</p><p>停用会撤销登录，并暂停 Telegram 投递；恢复启用保留个人偏好。通知服务短暂不可用时会继续重试同步，已经开始发送的消息可能仍到达。</p></section>
</template>
<style scoped>
.roles{padding:24px;margin-top:22px}.roles h2{font-size:16px;margin-bottom:16px}.roles table{border-collapse:collapse;width:100%;font-size:13px}.roles th,.roles td{text-align:left;padding:12px;border-bottom:1px solid var(--line)}.roles th{background:#edf5f2}.roles p{margin-top:15px;color:var(--muted);font-size:12px;line-height:1.8}
</style>
