<script setup lang="ts">
import {computed} from 'vue'
import type {Furnace} from '../api/client'
const props=defineProps<{furnace:Furnace}>()
const device=(kind:string)=>props.furnace.devices.find(d=>d.kind===kind)
const live=computed(()=>['sensor','door','lamp'].every(k=>device(k)?.online))
const open=computed(()=>device('door')?.state?.values.doorOpen===true)
const present=computed(()=>device('sensor')?.state?.values.present===true)
const inside=computed(()=>device('sensor')?.state?.values.inFurnace===true)
const alarm=computed(()=>device('lamp')?.state?.values.lampOn===true)
const scanning=computed(()=>live.value&&props.furnace.camera.online&&props.furnace.phase==='SCANNING')
const moving=computed(()=>live.value&&inside.value&&['CLOSING','RESETTING'].includes(props.furnace.phase))
const hot=computed(()=>device('temperature')?.online&&Number(device('temperature')?.state?.values.temperatureC)>100)
const basketX=computed(()=>inside.value?220:present.value?0:-105)
const basketVisible=computed(()=>present.value||inside.value)
const uid=computed(()=>`furnace-${props.furnace.furnaceId}`)
</script>
<template>
 <div :class="['scene',{offline:!live,moving,scanning,hot}]">
  <svg viewBox="0 0 600 300" role="img" :aria-label="`炉门${open?'开启':'关闭'}，${inside?'料筐已入炉':present?'入口有料筐':'入口空闲'}${live?'':'，设备离线，显示最后状态'}`">
   <defs><pattern :id="uid+'-grid'" width="24" height="24" patternUnits="userSpaceOnUse"><path d="M24 0H0V24" fill="none" stroke="#e7edef" stroke-width="1"/></pattern><clipPath :id="uid+'-door'"><rect x="317" y="22" width="186" height="183" rx="3"/></clipPath></defs>
   <rect width="600" height="300" :fill="`url(#${uid}-grid)`"/><path d="M25 230H572" stroke="#a8b9bd" stroke-width="2"/>
   <rect x="300" y="44" width="220" height="180" rx="8" fill="#d7e3e6" stroke="#8da6ad" stroke-width="2"/>
   <rect x="317" y="62" width="186" height="142" rx="3" :fill="hot?'#7d5543':'#344f58'" class="chamber"/>
   <g v-if="hot" class="heat" fill="none" stroke="#e9ac75" stroke-width="3" opacity=".5"><path d="M355 180q-12-15 0-30t0-30M410 180q-12-15 0-30t0-30M465 180q-12-15 0-30t0-30"/></g>
   <path d="M58 207H297" stroke="#819aa1" stroke-width="12"/>
   <g v-for="x in [75,112,149,186,223,260,288]" :key="x" :style="{transformOrigin:`${x}px 220px`}" class="roller"><circle :cx="x" cy="220" r="7" fill="#b4c4c8"/><path :d="`M${x-4} 220h8`" stroke="#78919b" stroke-width="2"/></g>
   <g class="basket" :style="{transform:`translateX(${basketX}px)`,opacity:basketVisible?1:0}"><rect x="135" y="139" width="108" height="60" rx="4" fill="#daeae6" stroke="#237969" stroke-width="2"/><path d="M150 151H230M150 166H230M150 182H230" stroke="#5b9a8d" stroke-width="2"/></g>
   <g :clip-path="`url(#${uid}-door)`"><g class="door" :style="{transform:`translateY(${open?-142:0}px)`}"><rect x="320" y="65" width="180" height="137" rx="2" fill="#8da7af"/><path d="M336 86H482M336 110H482M336 134H482M336 158H482M336 182H482" stroke="#b9cacf" stroke-width="3"/></g></g>
   <text x="410" y="249" text-anchor="middle" fill="#445e66" font-size="13">热处理炉 / {{furnace.furnaceId.toUpperCase()}}</text>
   <rect x="100" y="76" width="43" height="23" rx="4" fill="#5d7881"/><path d="M141 82L157 74V101L141 94Z" fill="#365962"/>
   <path d="M131 101L167 135M149 101L210 135" stroke="#3d9e8d" stroke-dasharray="5 5"/>
   <path v-if="scanning" class="scan-line" d="M145 149H237" stroke="#169c82" stroke-width="3"/>
   <circle cx="550" cy="61" r="15" :class="{beacon:alarm}" :fill="alarm?'#dc6551':'#b5c6ca'"/><path d="M550 78V106" stroke="#93a9b0" stroke-width="4"/>
   <text x="99" y="61" fill="#66808a" font-size="12">IP CAMERA</text>
   <text x="300" y="282" text-anchor="middle" :fill="live?'#52776d':'#aa6743'" font-size="12">{{!live?'设备离线 · 动画暂停，保留最后状态':inside?'入炉到位信号已确认':present?'入口已检测到料筐':'等待下一料筐'}}</text>
  </svg>
 </div>
</template>
<style scoped>
.scene{overflow:hidden}.scene svg{display:block;width:100%}.door{transition:transform .8s ease-in-out}.basket{transition:transform 1s ease-in-out,opacity .45s}.chamber{transition:fill 1s}.moving .roller{animation:roll .7s linear infinite}.scan-line{animation:scan 1.2s ease-in-out infinite alternate}.beacon{animation:blink .9s ease-in-out infinite}.hot .heat{animation:warmth 2.5s ease-in-out infinite alternate}.offline{opacity:.65}.offline *{animation-play-state:paused!important;transition:none!important}@keyframes roll{to{transform:rotate(360deg)}}@keyframes scan{to{transform:translateY(40px)}}@keyframes blink{50%{opacity:.25}}@keyframes warmth{to{transform:translateY(-6px);opacity:.8}}@media(prefers-reduced-motion:reduce){.scene *{animation:none!important;transition:none!important}}
</style>
