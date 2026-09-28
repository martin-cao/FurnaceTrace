<script setup lang="ts">
import {onBeforeUnmount,ref,watch} from 'vue'
import Hls from 'hls.js'
const props=defineProps<{url:string;online:boolean}>()
const video=ref<HTMLVideoElement>();const playing=ref(false)
let hls:Hls|undefined
watch(()=>[props.url,video.value,props.online],()=>{
  hls?.destroy();hls=undefined;playing.value=false
  if(!video.value||!props.url||!props.online)return
  if(Hls.isSupported()){hls=new Hls({enableWorker:true,lowLatencyMode:true});hls.loadSource(props.url);hls.attachMedia(video.value);hls.on(Hls.Events.MANIFEST_PARSED,()=>{void video.value?.play().catch(()=>{})})}
  else{video.value.src=props.url;void video.value.play().catch(()=>{})}
},{flush:'post',immediate:true})
onBeforeUnmount(()=>hls?.destroy())
</script>
<template>
  <div class="camera-frame">
    <video ref="video" muted autoplay playsinline controls aria-label="入炉摄像头实时画面" @playing="playing=true" />
    <div v-if="!online||!playing" class="camera-overlay"><span class="camera-mark">⌗</span><strong>{{online?'正在连接画面':'摄像头暂无画面'}}</strong><span>{{online?'RTSP 视频通过 HLS 提供预览':'视频恢复后会自动连接'}}</span></div>
    <div class="camera-caption"><span :class="['status-dot',online?'ok':'bad']"></span>{{online?'LIVE':'OFFLINE'}} <span class="mono">CAM / 01</span></div>
  </div>
</template>
