<script setup lang="ts">
import {computed} from 'vue'
import {formatTime,type Furnace} from '../api/client'
const props=defineProps<{furnace:Furnace}>()
defineEmits<{(e:'configure-alarms'):void}>()
const sensor=computed(()=>props.furnace.devices.find(d=>d.kind==='temperature'))
const reading=computed(()=>{const v=sensor.value?.state?.values?.temperatureC;return sensor.value?.online&&typeof v==='number'&&Number.isFinite(v)?v.toFixed(1):'—'})
</script>
<template><section v-if="sensor" class="temperature-panel"><div class="temperature-heading"><h3>炉温传感器</h3><span :class="['pill',sensor.online?'good':'warn']">{{sensor.online?'在线':'无新数据'}}</span></div><div class="temperature-reading"><strong class="mono">{{reading}}</strong><span>°C</span></div><p class="muted small">FUXA 回传 · {{formatTime(sensor.state?.observedAt)}}</p><p class="muted small">模拟温度在 FUXA 现场页面输入。温度、设备与流程报警统一在报警规则中配置。</p><el-button link type="primary" @click="$emit('configure-alarms')">查看报警规则 →</el-button></section></template>
<style scoped>.temperature-panel{border-top:1px solid var(--line);padding:18px 22px}.temperature-heading{display:flex;align-items:center;justify-content:space-between}.temperature-heading h3{font-size:14px}.temperature-reading{display:flex;gap:9px;align-items:baseline;margin:12px 0 5px}.temperature-reading strong{font-size:29px;font-weight:600;color:#176e63}.temperature-reading>span{color:var(--muted);font-size:15px}.temperature-panel p{margin-bottom:9px;line-height:1.7}</style>
