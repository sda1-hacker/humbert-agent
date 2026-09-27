import test, { mock } from 'node:test';
import assert from 'node:assert/strict';
import { createPinia, setActivePinia } from 'pinia';

const waiting=[];
mock.module('../api/sessions.js', {namedExports:{
  createSession:()=>{}, deleteSession:()=>{}, getMessageWindow:()=>new Promise(resolve=>waiting.push(resolve)), listSessions:()=>{}, renameSession:()=>{},setSessionArchived:()=>{},
  listMessagePage:()=>new Promise(resolve=>waiting.push(resolve)),
}});
const {useSessionStore}=await import('../stores/sessions.js');

test('older message request must not overwrite a newer page of the same session',async()=>{
 waiting.length=0;
 setActivePinia(createPinia());
 const store=useSessionStore();store.selectedID='same-session';
 const oldRequest=store.refreshMessages('same-session');
 const newRequest=store.refreshMessages('same-session');
 waiting[1]({messages:[{id:'new-answer',role:'assistant',content:'new answer'}],hasMore:false});
 await newRequest;
 assert.equal(store.messages[0].id,'new-answer');
 waiting[0]({messages:[{id:'old-message',role:'user',content:'old input'}],hasMore:false});
 await oldRequest;
 assert.equal(store.messages[0].id,'new-answer','late stale IPC response replaced current messages');
});

// 搜索定位和最新页刷新使用同一个版本，分页不能把旧窗口混入新回答。
test('search window rejects an earlier refresh and is superseded by a later refresh',async()=>{
 waiting.length=0;setActivePinia(createPinia());
 const store=useSessionStore();store.selectedID='s';store.jumpTargetID='target';
 const stale=store.refreshMessages();const search=store.loadSearchWindow('target');
 waiting[1]({messages:[{id:'target'}]});await search;
 waiting[0]({messages:[{id:'stale'}]});await stale;
 assert.equal(store.messages[0].id,'target');assert.equal(store.searchWindowActive,true);
 const oldSearch=store.loadSearchWindow('target');const refresh=store.refreshMessages();
 waiting[3]({messages:[{id:'latest'}]});await refresh;
 waiting[2]({messages:[{id:'target'}]});assert.equal(await oldSearch,false);
 assert.equal(store.messages[0].id,'latest');assert.equal(store.searchWindowActive,false);
});
test('old pagination cannot merge into a replacement page with the same cursor',async()=>{
 waiting.length=0;setActivePinia(createPinia());
 const store=useSessionStore();store.selectedID='s';store.messages=[{id:'base'}];
 store.messageHasMore=true;store.messageBeforeID='same';
 const older=store.loadOlderMessages();const refresh=store.refreshMessages();
 waiting[1]({messages:[{id:'new'}],hasMore:true,nextBeforeID:'same'});await refresh;
 const nextOlder=store.loadOlderMessages();
 waiting[0]({messages:[{id:'stale'}]});assert.equal(await older,0);
 assert.equal(store.loadingOlderMessages,true);
 waiting[2]({messages:[{id:'valid-older'}]});await nextOlder;
 assert.deepEqual(store.messages.map(x=>x.id),['valid-older','new']);
 assert.equal(store.loadingOlderMessages,false);
});
test('reset invalidates a request even when the same session is selected again',async()=>{
 waiting.length=0;setActivePinia(createPinia());const store=useSessionStore();store.selectedID='s';
 const stale=store.refreshMessages();store.resetMessagePage();store.messages=[{id:'current'}];
 waiting[0]({messages:[{id:'stale'}]});await stale;
 assert.equal(store.messages[0].id,'current');
});

test('pagination started during refresh cannot commit into the new window',async()=>{
 waiting.length=0;setActivePinia(createPinia());
 const store=useSessionStore();store.selectedID='s';store.messageHasMore=true;store.messageBeforeID='same';
 const refresh=store.refreshMessages();const older=store.loadOlderMessages();
 waiting[0]({messages:[{id:'fresh'}],hasMore:true,nextBeforeID:'same'});await refresh;
 waiting[1]({messages:[{id:'old-window'}]});assert.equal(await older,0);
 assert.deepEqual(store.messages.map(x=>x.id),['fresh']);assert.equal(store.loadingOlderMessages,false);
});
