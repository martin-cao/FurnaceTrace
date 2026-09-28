<script setup lang="ts">
import {onMounted,ref,watch} from 'vue'
import {get,post,formatTime,type AlarmPage,type Alarm,type NotificationPage} from '../api/client'
import {ElMessage} from 'element-plus'
import {rulesVersion,liveAlarmCount} from '../liveVersions'
import {currentUser} from '../auth'
const alarms=ref<AlarmPage>();const notifications=ref<NotificationPage>();const activeOnly=ref(true);const error=ref('');const selected=ref<Alarm>();const dialog=ref(false);const operator=ref('操作员');const reason=ref('已查看，正在处理');let key=''
async function load(){error.value='';try{alarms.value=await get(`/alarms?pageSize=100${activeOnly.value?'&active=true':''}`);notifications.value=await get('/notifications?pageSize=20')}catch(e){error.value=(e as Error).message}}
function acknowledge(a:Alarm){selected.value=a;key=crypto.randomUUID();dialog.value=true}
async function submit(){if(!operator.value.trim()||!reason.value.trim()){ElMessage.warning('请填写操作人和原因');return}try{await post(`/alarms/${selected.value?.id}/acknowledgements`,{operator:operator.value,reason:reason.value},key);dialog.value=false;await load()}catch(e){ElMessage.error((e as Error).message)}}
const labels:Record<string,string>={pending:'等待发送',sent:'已发送',failed:'发送失败',unknown:'结果不确定',cancelled:'已取消'}
watch([rulesVersion,liveAlarmCount],()=>void load())
onMounted(load)
</script>
<template>
  <div class="section-title"><div><div class="eyebrow">ALARMS & NOTIFICATIONS</div><h2>报警与通知</h2><p>确认表示已查看；故障恢复由设备状态和业务流程决定。</p></div><el-button @click="load">刷新</el-button></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <section class="panel table-panel"><div class="panel-heading"><h3>报警记录</h3><el-switch v-model="activeOnly" active-text="仅活动报警" @change="load"/></div><el-table :data="alarms?.items??[]" empty-text="当前没有活动报警。"><el-table-column label="报警" min-width="280"><template #default="{row}"><strong>{{row.message}}</strong><div class="mono small muted">{{row.code}} · {{row.furnaceId}}</div></template></el-table-column><el-table-column label="发生时间" min-width="190"><template #default="{row}">{{formatTime(row.raisedAt)}}</template></el-table-column><el-table-column label="状态" width="130"><template #default="{row}"><span :class="['pill',row.recoveredAt?'good':'warn']">{{row.recoveredAt?(row.resolution==='rule_disabled'?'规则已停用':row.resolution==='rule_deleted'?'规则已删除':row.resolution==='rule_migrated'?'已合并迁移':'已恢复'):row.acknowledgedAt?'已确认 · 活动':'待处理'}}</span></template></el-table-column><el-table-column label="操作" width="100"><template #default="{row}"><el-button v-if="currentUser?.role==='admin'&&!row.acknowledgedAt" link type="primary" @click="acknowledge(row)">确认已查看</el-button><span v-else class="muted small">{{row.operator}}</span></template></el-table-column></el-table></section>
  <section class="panel table-panel"><div class="panel-heading"><h3>我的 Telegram 投递</h3><span class="muted small">独立通知服务</span></div><el-table :data="notifications?.items??[]" empty-text="暂无通知任务。"><el-table-column label="类型" width="100"><template #default="{row}">{{row.kind==='ALARM'?'报警':row.kind==='REMINDER'?'持续提醒':'恢复 / 结束'}}</template></el-table-column><el-table-column label="状态" min-width="120"><template #default="{row}"><span :class="['pill',row.status==='sent'?'good':'neutral']">{{labels[row.status]}}</span></template></el-table-column><el-table-column prop="attempts" label="尝试" width="80"/><el-table-column prop="lastError" label="说明" min-width="260"/><el-table-column label="创建时间" min-width="190"><template #default="{row}">{{formatTime(row.createdAt)}}</template></el-table-column></el-table></section>
  <el-dialog v-model="dialog" title="确认已查看报警" width="min(460px,92vw)"><el-form label-position="top"><el-form-item label="操作人"><span>{{currentUser?.displayName}}</span></el-form-item><el-form-item label="备注"><el-input v-model="reason" maxlength="500"/></el-form-item></el-form><template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" @click="submit">确认</el-button></template></el-dialog>
</template>
