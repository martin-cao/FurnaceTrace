import {ref} from 'vue'
import {call,get,type User,type SessionResponse} from './api/client'

export const currentUser=ref<User|null>(null)
export async function refreshSession(){currentUser.value=await get<User>('/me')}
export async function signIn(username:string,password:string){
  const result=await call<SessionResponse>('POST','/auth/login',{username,password})
  currentUser.value=result.user
}
export async function register(username:string,password:string,displayName:string){
  const result=await call<SessionResponse>('POST','/auth/register',{username,password,displayName})
  currentUser.value=result.user
}
export async function signOut(){await call('POST','/auth/logout');currentUser.value=null}
