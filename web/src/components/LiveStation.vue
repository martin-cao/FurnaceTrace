<script setup lang="ts">
import {computed,ref} from 'vue'
import type {Furnace} from '../api/client'
import {phases,reasons,formatTime,post} from '../api/client'
import FurnaceScene from './FurnaceScene.vue'
import CameraFeed from './CameraFeed.vue'
import TemperaturePanel from './TemperaturePanel.vue'
import {ElMessage} from 'element-plus'
import {currentUser} from '../auth'
defineEmits<{(e:'configure-alarms'):void}>()
const props=defineProps<{furnace:Furnace}>()
const device=(kind:string)=>props.furnace.devices.find(d=>d.kind===kind)
const controlDevices=computed(()=>props.furnace.devices.filter(d=>d.kind!=='temperature'))
const deviceLabels:Record<string,string>={sensor:'到位传感器',door:'炉门执行器',lamp:'报警器'}
const doorOpen=computed(()=>device('door')?.state?.values?.doorOpen===true)
const present=computed(()=>device('sensor')?.state?.values?.present===true)
const lamp=computed(()=>device('lamp')?.state?.values?.lampOn===true)
const steps=['SCANNING','VALIDATING','OPENING','WAITING_ENTRY','CLOSING','RESETTING']
const stepIndex=computed(()=>steps.indexOf(props.furnace.phase))
const dialog=ref<'manual'|'reset'|null>(null);const operator=ref('操作员');const reason=ref('');const rawCode=ref('');const busy=ref(false)
let key=''
function open(kind:'manual'|'reset'){dialog.value=kind;reason.value=kind==='reset'?'终止异常周期并重新开始':'';rawCode.value='';key=crypto.randomUUID()}
async function submit(){
  if(!operator.value.trim()||!reason.value.trim()||(dialog.value==='manual'&&!rawCode.value.trim())){ElMessage.warning('请填写完整的操作信息');return}
  busy.value=true
  try{const f=props.furnace;await post(`/furnaces/${f.furnaceId}/${dialog.value==='manual'?'manual-scans':'resets'}`,dialog.value==='manual'?{cycleId:f.cycle?.id,rawCode:rawCode.value,operator:operator.value,reason:reason.value}:{operator:operator.value,reason:reason.value},key);ElMessage.success('请求已提交，等待设备反馈');dialog.value=null}catch(err){ElMessage.error((err as Error).message)}finally{busy.value=false}
}
</script>
<template>
  <div class="station-heading"><div><div class="eyebrow">ENTRY CONTROL / {{furnace.furnaceId.toUpperCase()}}</div><h2>{{furnace.name}}</h2></div><span :class="['pill',furnace.ready?'good':'warn']">{{furnace.ready?'入炉链路可用':'入炉链路待恢复'}}</span></div>
  <div class="live-grid">
    <section class="panel process-panel"><div class="panel-heading"><h3>现场状态</h3><span class="muted small">经 FUXA 采集</span></div>
      <div class="furnace-drawing">
        <FurnaceScene :furnace="furnace"/>
      </div>
      <div class="device-strip"><div v-for="d in controlDevices" :key="d.deviceId"><span :class="['status-dot',d.online?'ok':'bad']"></span><strong>{{deviceLabels[d.kind]}}</strong><span>{{d.online?(d.kind==='door'?(doorOpen?'已打开':'已关闭'):d.kind==='sensor'?(present?'料筐到位':'入口空闲'):(lamp?'报警中':'正常')):'离线'}}</span></div></div>
      <TemperaturePanel :furnace="furnace" @configure-alarms="$emit('configure-alarms')"/>
    </section>
    <section class="panel"><div class="panel-heading"><h3>扫码摄像头</h3><span class="muted small">{{furnace.camera.online?'视频流在线':'等待视频流'}}</span></div><CameraFeed :url="furnace.previewUrl" :online="furnace.camera.online"/></section>
  </div>
  <section class="panel cycle-panel"><div class="panel-heading"><div><div class="eyebrow">CURRENT CYCLE</div><h3>{{phases[furnace.phase]??furnace.phase}}</h3></div><div class="actions"><el-button v-if="currentUser?.role==='admin'&&furnace.phase==='NEEDS_INPUT'" type="primary" @click="open('manual')">人工补录</el-button></div></div>
    <p v-if="['BLOCKED','NEEDS_INPUT'].includes(furnace.phase)" class="muted">系统将自动关门复位，随后继续扫码。</p>
    <div class="cycle-meta"><div><span>料筐编号</span><strong class="mono">{{furnace.cycle?.basketNo||'等待识别'}}</strong></div><div><span>开始时间</span><strong>{{formatTime(furnace.cycle?.createdAt)}}</strong></div><div><span>周期编号</span><strong class="mono">{{furnace.cycle?.id.slice(0,8)||'—'}}</strong></div></div>
    <ol class="process-steps"><li v-for="(s,i) in steps" :key="s" :class="{current:i===stepIndex,done:i<stepIndex}"><span>{{i<stepIndex?'✓':String(i+1).padStart(2,'0')}}</span>{{phases[s]}}</li></ol>
    <div v-if="furnace.cycle?.reason" :class="['cycle-note',['BLOCKED','NEEDS_INPUT'].includes(furnace.phase)?'danger':'']">{{reasons[furnace.cycle.reason]??furnace.cycle.reason}}</div>
    <p v-else-if="furnace.phase==='IDLE'" class="muted">摄像头持续扫码；将料筐二维码放入画面后，自动开始 MES 校验。</p>
  </section>
  <el-dialog :model-value="dialog!==null" :title="dialog==='manual'?'人工补录料筐':'人工复位'" width="min(480px,92vw)" @close="dialog=null">
    <p class="dialog-help">{{dialog==='manual'?'补录后仍需通过 MES 校验，操作将写入审计记录。':'确认后自动关门、复位并终止本周期，不需要手动关门或清空入口。'}}</p>
    <el-form label-position="top" @submit.prevent="submit"><el-form-item v-if="dialog==='manual'" label="二维码原文或料筐编号"><el-input v-model="rawCode" maxlength="2048"/></el-form-item><el-form-item label="操作人"><span>{{currentUser?.displayName}}</span></el-form-item><el-form-item label="操作原因"><el-input v-model="reason" type="textarea" maxlength="500"/></el-form-item></el-form>
    <template #footer><el-button @click="dialog=null">取消</el-button><el-button type="primary" :loading="busy" @click="submit">提交</el-button></template>
  </el-dialog>
</template>
