<script setup lang="ts">
import type {RuleCondition,RuleConditionLeaf} from '../api/client'
const props=withDefaults(defineProps<{modelValue:RuleCondition,depth?:number}>(),{depth:1})
const emit=defineEmits<{(e:'update:modelValue',value:RuleCondition):void}>()
const fields=[['temperatureC','炉温（°C）'],['entryPresent','入口到位'],['inFurnace','炉内到位'],['doorOpen','炉门已打开'],['doorClosed','炉门已关闭'],['sensorOnline','到位传感器在线'],['doorOnline','炉门执行器在线'],['lampOnline','报警器在线'],['temperatureOnline','炉温传感器在线'],['cameraOnline','摄像头在线'],['scanFailed','扫码失败'],['mesRejected','MES 拒绝入炉'],['mesUnavailable','MES 通信失败'],['doorTimeout','开关门超时'],['entryTimeout','入炉到位超时'],['resetTimeout','复位超时'],['workflowFault','入炉执行异常待处理']]
const ops=[['gt','大于 >'],['gte','大于等于 ≥'],['lt','小于 <'],['lte','小于等于 ≤'],['eq','等于 ='],['ne','不等于 ≠']]
const leaf=():RuleCondition=>({type:'condition',field:'temperatureC',operator:'gt',value:800})
function typeChanged(e:Event){const type=(e.target as HTMLSelectElement).value;if(type==='group')emit('update:modelValue',{type:'group',logic:'AND',children:[props.modelValue]});else emit('update:modelValue',leaf())}
function patch(p:Partial<RuleConditionLeaf>){if(props.modelValue.type==='condition')emit('update:modelValue',{...props.modelValue,...p})}
function fieldChanged(e:Event){const field=(e.target as HTMLSelectElement).value as RuleConditionLeaf['field'];patch({field,operator:field==='temperatureC'?'gt':'eq',value:field==='temperatureC'?800:true})}
function add(group=false){if(props.modelValue.type==='group')emit('update:modelValue',{...props.modelValue,children:[...props.modelValue.children,group?{type:'group',logic:'AND',children:[leaf()]}:leaf()]})}
function child(i:number,value:RuleCondition){if(props.modelValue.type==='group')emit('update:modelValue',{...props.modelValue,children:props.modelValue.children.map((c,n)=>n===i?value:c)})}
function remove(i:number){if(props.modelValue.type==='group')emit('update:modelValue',{...props.modelValue,children:props.modelValue.children.filter((_,n)=>n!==i)})}
function logic(e:Event){if(props.modelValue.type==='group')emit('update:modelValue',{...props.modelValue,logic:(e.target as HTMLSelectElement).value as 'AND'|'OR'})}
</script>
<template>
 <div class="condition-box">
  <div class="condition-row"><select :value="modelValue.type" aria-label="条件类型" @change="typeChanged"><option value="condition">单个条件</option><option value="group" :disabled="depth>=4">条件组合</option></select>
  <template v-if="modelValue.type==='condition'"><select :value="modelValue.field" aria-label="条件字段" @change="fieldChanged"><option v-for="[id,label] in fields" :key="id" :value="id">{{label}}</option></select><select :value="modelValue.operator" aria-label="比较方式" @change="patch({operator:($event.target as HTMLSelectElement).value as RuleConditionLeaf['operator']})"><option v-for="[id,label] in ops.filter(o=>modelValue.type==='condition'&&(modelValue.field==='temperatureC'||o[0]==='eq'||o[0]==='ne'))" :key="id" :value="id">{{label}}</option></select><input v-if="modelValue.field==='temperatureC'" :value="modelValue.value" type="number" step="0.1" aria-label="温度阈值" @input="patch({value:($event.target as HTMLInputElement).value.trim()===''?NaN:Number(($event.target as HTMLInputElement).value)})"/><select v-else :value="String(modelValue.value)" aria-label="条件值" @change="patch({value:($event.target as HTMLSelectElement).value==='true'})"><option value="true">是 / true</option><option value="false">否 / false</option></select></template>
  <select v-else :value="modelValue.logic" aria-label="组合逻辑" @change="logic"><option value="AND">AND · 所有条件满足</option><option value="OR">OR · 任意条件满足</option></select></div>
  <template v-if="modelValue.type==='group'"><div v-for="(c,i) in modelValue.children" :key="i" class="condition-child"><RuleConditionEditor :model-value="c" :depth="depth+1" @update:model-value="child(i,$event)"/><button type="button" :disabled="modelValue.children.length===1" class="remove-condition" aria-label="删除子条件" @click="remove(i)">×</button></div><div class="condition-add"><el-button size="small" :disabled="modelValue.children.length>=16" @click="add()">添加条件</el-button><el-button size="small" :disabled="depth>=3||modelValue.children.length>=16" @click="add(true)">添加子组合</el-button></div></template>
 </div>
</template>
<style scoped>
.condition-box{border:1px solid #d3e0dc;border-radius:6px;padding:12px;background:#f8fbfa}.condition-row{display:flex;gap:8px;flex-wrap:wrap}.condition-row select,.condition-row input{padding:8px;border:1px solid #cddbd7;border-radius:4px;background:white;color:#244b44;font:inherit;font-size:12px;max-width:100%}.condition-row input{width:115px}.condition-child{display:flex;align-items:flex-start;gap:5px;margin-top:10px}.condition-child>.condition-box{flex:1;min-width:0}.remove-condition{border:0;background:transparent;font-size:22px;color:#a55345;cursor:pointer}.remove-condition:disabled{opacity:.3}.condition-add{margin-top:10px}
</style>
