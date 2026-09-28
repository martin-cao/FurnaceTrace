export type Batch = components['schemas']['Batch']
export type Basket = components['schemas']['Basket']
export type BatchPage = components['schemas']['BatchPage']
export type BasketPage = components['schemas']['BasketPage']
export type BatchInput = components['schemas']['BatchInput']
export type BasketInput = components['schemas']['BasketInput']
export type AlarmRule = components['schemas']['AlarmRule']
export type AlarmRuleInput = components['schemas']['AlarmRuleInput']
export type RuleConditionLeaf = components['schemas']['RuleConditionLeaf']
export type RuleCondition = components['schemas']['RuleCondition']
export type RuleChannels = components['schemas']['RuleChannels']
import type { components } from './schema'
export type Furnace = components['schemas']['Furnace']
export type Cycle = components['schemas']['Cycle']
export type CycleDetail = components['schemas']['CycleDetail']
export type Alarm = components['schemas']['Alarm']
export type Notification = components['schemas']['Notification']
export type Snapshot = components['schemas']['StreamSnapshot']
export type CyclePage = components['schemas']['CyclePage']
export type AlarmPage = components['schemas']['AlarmPage']
export type NotificationPage = components['schemas']['NotificationPage']
export type CameraConfig = components['schemas']['CameraConfig']
export type ManagedUser = components['schemas']['ManagedUser']
export type ManagedUserPage = components['schemas']['ManagedUserPage']
export type User = components['schemas']['User']
export type SessionResponse = components['schemas']['SessionResponse']
export type TelegramState = components['schemas']['TelegramState']
export type Preferences = components['schemas']['NotificationPreferences']
export class APIError extends Error { constructor(public status:number,message:string){super(message)} }
export async function call<T>(method:string,path:string,body?:unknown,key?:string):Promise<T> {
  const headers:Record<string,string>={}
  if(body!==undefined)headers['Content-Type']='application/json'
  if(key)headers['Idempotency-Key']=key
  const controller=new AbortController()
  const timeout=setTimeout(()=>controller.abort(),15000)
  try {
    const res=await fetch('/api/v1'+path,{method,headers,credentials:'same-origin',signal:controller.signal,body:body===undefined?undefined:JSON.stringify(body)})
    if(!res.ok){const data=await res.json().catch(()=>null);if(res.status===401&&!path.startsWith('/auth/'))window.dispatchEvent(new Event('auth-required'));throw new APIError(res.status,data?.error?.message??'请求失败，请稍后重试')}
    if(res.status===204)return undefined as T
    return await res.json()
  } catch(e) {
    if(controller.signal.aborted)throw new APIError(0,'请求超时，结果尚未确认；请刷新核实后再试。')
    throw e
  } finally {clearTimeout(timeout)}
}
export async function get<T>(path:string):Promise<T> {return call<T>('GET',path)}
export async function post(path:string,body:unknown,key:string):Promise<void> {
  await call('POST',path,body,key)
}
export const phases:Record<string,string>={IDLE:'持续扫码中',SCANNING:'识别二维码',VALIDATING:'MES 校验',OPENING:'炉门开启中',WAITING_ENTRY:'等待入炉到位',CLOSING:'炉门关闭中',RESETTING:'设备复位中',NEEDS_INPUT:'识别异常，自动复位',BLOCKED:'自动恢复中',COMPLETED:'入炉完成',ABORTED:'已中止'}
export const reasons:Record<string,string>={MES_REJECTED:'MES 拒绝本次入炉，请核实料筐状态。',MES_UNAVAILABLE:'MES 通信失败，本次流程将自动复位。',SCAN_TIMEOUT:'未识别到二维码，可以人工补录料筐编号。',INVALID_BARCODE:'二维码中的料筐编号无效。',AMBIGUOUS:'画面中存在多个二维码，请保留一个。',CAMERA_OFFLINE:'摄像头视频中断。',DEVICE_OFFLINE:'设备连接中断，流程已暂停。',DEVICE_RESTARTED:'设备在执行期间重启，需要人工确认现场状态。',RECOVERY_REQUIRED:'业务服务重启，需要人工确认未完成操作。',BASKET_OCCUPIED:'该料筐已有占用记录，请核实后处理。',OPEN_TIMEOUT:'炉门打开反馈超时，系统正在自动恢复。',CLOSE_TIMEOUT:'炉门关闭反馈超时。',ENTRY_TIMEOUT:'料筐入炉反馈超时，本次流程将自动复位。',RESET_TIMEOUT:'设备复位反馈超时。'}
export const formatTime=(v?:string|null)=>v?new Date(v).toLocaleString('zh-CN',{hour12:false}):'—'
