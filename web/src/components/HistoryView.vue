<script setup lang="ts">
import {onMounted,ref} from 'vue'
import {get,formatTime,phases,type Cycle,type CyclePage,type CycleDetail} from '../api/client'
const data=ref<CyclePage>();const basket=ref('');const page=ref(1);const loading=ref(false);const error=ref('');const detail=ref<CycleDetail>();const detailOpen=ref(false)
async function load(){loading.value=true;error.value='';try{data.value=await get(`/cycles?page=${page.value}&pageSize=12&basketNo=${encodeURIComponent(basket.value)}`)}catch(e){error.value=(e as Error).message}finally{loading.value=false}}
async function show(id:string){try{detail.value=await get('/cycles/'+id);detailOpen.value=true}catch(e){error.value=(e as Error).message}}
function selectRow(row:Cycle){void show(row.id)}
onMounted(load)
</script>
<template>
  <div class="section-title"><div><div class="eyebrow">TRACEABILITY</div><h2>入炉记录</h2><p>从扫码到设备复位，每一步都有记录。</p></div><el-button @click="load">刷新</el-button></div>
  <section class="panel table-panel"><form class="filter-row" @submit.prevent="page=1;load()"><el-input v-model="basket" placeholder="按完整料筐编号查询" aria-label="料筐编号" clearable style="max-width:280px"/><el-button native-type="submit" type="primary">查询</el-button></form>
    <el-alert v-if="error" :title="error" type="error" :closable="false"/>
    <el-table :data="data?.items??[]" v-loading="loading" empty-text="暂无入炉记录，完成一次扫码后可在此追溯。" @row-click="selectRow" row-class-name="clickable-row">
      <el-table-column prop="basketNo" label="料筐编号" min-width="130"><template #default="{row}"><strong class="mono">{{row.basketNo||'未识别'}}</strong></template></el-table-column>
      <el-table-column prop="furnaceId" label="炉子" width="90"/>
      <el-table-column label="状态" min-width="140"><template #default="{row}"><span :class="['pill',row.status==='COMPLETED'?'good':['BLOCKED','NEEDS_INPUT'].includes(row.status)?'warn':'neutral']">{{phases[row.status]}}</span></template></el-table-column>
      <el-table-column label="开始时间" min-width="190"><template #default="{row}">{{formatTime(row.createdAt)}}</template></el-table-column>
      <el-table-column label="完成时间" min-width="190"><template #default="{row}">{{formatTime(row.completedAt)}}</template></el-table-column>
      <el-table-column label="详情" width="80"><template #default="{row}"><el-button link type="primary" @click.stop="show(row.id)">查看 →</el-button></template></el-table-column>
    </el-table>
    <div class="pagination"><span class="muted">共 {{data?.pagination.total??0}} 条记录</span><el-pagination v-model:current-page="page" :page-size="12" :total="data?.pagination.total??0" layout="prev,pager,next" @current-change="load"/></div>
  </section>
  <el-drawer v-model="detailOpen" title="入炉过程追溯" size="min(560px,95vw)"><template v-if="detail"><div class="detail-header"><h2 class="mono">{{detail.cycle.basketNo||'未识别料筐'}}</h2><span class="pill neutral">{{phases[detail.cycle.status]}}</span><p class="mono small muted">{{detail.cycle.id}}</p></div><section v-if="detail.cycle.mes" class="mes-snapshot"><h3>MES 校验记录</h3><p>{{detail.cycle.mes.message}}</p><template v-if="detail.cycle.mes.snapshot"><p>批次：{{detail.cycle.mes.snapshot.batchNo}} · 物料：{{detail.cycle.mes.snapshot.materialName}}</p><p>数量：{{detail.cycle.mes.snapshot.quantity}} · 质检：{{({pending:'待检',passed:'合格',failed:'不合格'} as Record<string,string>)[detail.cycle.mes.snapshot.qualityStatus]}}</p><p>工艺：{{detail.cycle.mes.snapshot.processSpec||'未填写'}}</p><p class="muted small">保存的是校验当时的快照，后续台账修改不覆盖这份记录。</p></template></section><el-timeline><el-timeline-item v-for="event in detail.events" :key="event.id" :timestamp="formatTime(event.at)"><strong>{{phases[event.kind]??event.kind}}</strong><p>{{event.message}}</p></el-timeline-item></el-timeline><h3>设备命令</h3><div v-for="c in detail.commands" :key="c.command.commandId" class="command-row"><strong>{{c.command.action}}</strong><span>{{c.status}}</span><span class="mono small">{{c.command.commandId.slice(0,8)}}</span></div></template></el-drawer>
</template>

<style scoped>.mes-snapshot{padding:16px;background:#edf5f2;margin:18px 0}.mes-snapshot p{font-size:12px;line-height:1.8;margin-top:7px}</style>
