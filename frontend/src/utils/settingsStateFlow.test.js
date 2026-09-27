import test, { beforeEach, mock } from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {pathToFileURL,fileURLToPath} from 'node:url';
import {beginLatestRequest} from './latestRequest.js';
import {readFileSync} from 'node:fs';
const root=fileURLToPath(new URL('../..',import.meta.url));
const require=createRequire(root+'/package.json');
const {createPinia,setActivePinia}=await import(pathToFileURL(require.resolve('pinia')));
const Vue=await import(pathToFileURL(require.resolve('vue')));
const apis={};
const modules={sessions:['createSession','deleteSession','listMessagePage','getMessageWindow','listSessions','renameSession','setSessionArchived'],models:['createModel','createProvider','deleteModel','deleteProvider','getModelState','testModel','diagnoseModel','updateModel','updateProvider','updateMultimediaConfig'],skills:['getSkillState','deleteSkill'],proactive:['getProactiveSettings','getProactiveStatus','listProactiveRecords','listRecentNotifications','runProactiveHeartbeat','updateProactiveSettings'],mcp:['listMCPServers','testMCPConnection','discoverMCPTools','refreshMCPTools']};
for(const [name,exports] of Object.entries(modules)){
  mock.module(pathToFileURL(root+'/src/api/'+name+'.js'),{namedExports:Object.fromEntries(exports.map(key=>[key,(...args)=>apis[key](...args)]))});
}
mock.module(pathToFileURL(require.resolve('@wailsio/runtime')),{namedExports:{Events:{On:()=>()=>{}}}});
const {useSessionStore}=await import(pathToFileURL(root+'/src/stores/sessions.js'));
const {useModelStore}=await import(pathToFileURL(root+'/src/stores/models.js'));
const {useSkillStore}=await import(pathToFileURL(root+'/src/stores/skills.js'));
const {useProactiveStore}=await import(pathToFileURL(root+'/src/stores/proactive.js'));
const {useMCPStore}=await import(pathToFileURL(root+'/src/stores/mcp.js'));
function deferred(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return{promise,resolve,reject};}
beforeEach(()=>{
  setActivePinia(createPinia());
  for(const keys of Object.values(modules))for(const key of keys)apis[key]=async()=>({});
  apis.listRecentNotifications=async()=>[];
});
test('删除会话后旧列表不能复活会话',async()=>{
  const old=deferred();apis.listSessions=()=>old.promise;
  const store=useSessionStore();store.agentID='agent';store.cacheAgentSessions('agent',[{id:'deleted'}]);
  const load=store.loadAgentSessions('agent',{force:true});
  await store.remove('deleted');assert.equal(store.items.length,0);
  old.resolve([{id:'deleted'}]);await load;
  assert.equal(store.items.length,0);
});
test('模型配置保存后旧 load 不能回滚页面配置',async()=>{
  const old=deferred();apis.getModelState=()=>old.promise;
  apis.updateMultimediaConfig=async()=>({imageModelID:'new'});
  const store=useModelStore();const load=store.load();
  await store.updateMultimediaConfig({imageModelID:'new'});assert.equal(store.multimedia.imageModelID,'new');
  old.resolve({revision:1,multimedia:{imageModelID:'old'}});await load;
  assert.equal(store.multimedia.imageModelID,'new');
});
test('Skill 设置页启用后不能丢掉保存后的刷新',async()=>{
  const old=deferred();let calls=0;
  const before={agents:[{id:'agent',enabledSkills:[]}],skills:[{name:'skill',valid:true}]};
  const after={agents:[{id:'agent',enabledSkills:['skill']}],skills:before.skills};
  const agentStore={items:[{id:'agent',enabledSkills:[]}],selectedID:'agent',applySkillSelection(id,skills){this.items=this.items.map(a=>a.id===id?{...a,enabledSkills:skills}:a);}};
  // 执行真实 SFC 的脚本，替换导入依赖与生命周期；不复制页面的业务函数。
  const source=readFileSync(root+'/src/components/settings/SkillSettings.vue','utf8').split('<script setup>')[1].split('</script>')[0].replace(/\bimport[\s\S]*?from\s+["'][^"']+["'];?/g,'');
  const bindings={beginLatestRequest,computed:Vue.computed,ref:Vue.ref,onMounted:()=>{},watch:()=>{},defineProps:()=>({initialAgentId:'agent'}),defineEmits:()=>()=>{},Message:{success:()=>{},error:e=>{throw new Error(e)}},t:s=>s,useAgentStore:()=>agentStore,getSkillState:()=>++calls===1?old.promise:Promise.resolve(after),enableSkillForAgent:async()=>{},disableSkillForAgent:async()=>{}};
  const panel=Function(...Object.keys(bindings),source+';return {load,setSkillEnabled,agents,skills,selectedAgentID};')(...Object.values(bindings));
  panel.agents.value=before.agents;panel.skills.value=before.skills;panel.selectedAgentID.value='agent';
  const load=panel.load();await panel.setSkillEnabled({name:'skill'},true);
  old.resolve(before);await load;
  assert.deepEqual(panel.agents.value[0].enabledSkills,['skill']);
});
test('MCP 保存后 load 不能只复用保存前快照',async()=>{
  const old=deferred();let calls=0;apis.listMCPServers=()=>++calls===1?old.promise:Promise.resolve([{id:'mcp',enabled:false,updatedAt:'new'}]);
  const store=useMCPStore();const load=store.load();
  // 设置组件在后端保存成功后调用的刷新，与保存前的 load 重叠。
  const refreshAfterSave=store.load({force:true});old.resolve([{id:'mcp',enabled:true,updatedAt:'old'}]);
  await Promise.all([load,refreshAfterSave]);
  assert.equal(store.servers[0].enabled,false);
});
test('主动助手保存后旧设置响应不能撤销保存结果',async()=>{
  const old=deferred();apis.getProactiveSettings=()=>old.promise;apis.listProactiveRecords=async()=>[];
  apis.updateProactiveSettings=async()=>({enabled:true});
  const store=useProactiveStore();const load=store.load();
  await store.save({enabled:true});assert.equal(store.settings.enabled,true);
  old.resolve({enabled:false});await load;
  assert.equal(store.settings.enabled,true);
});
test('主动助手终态事件不能被旧记录列表覆盖',async()=>{
  const old=deferred();apis.listProactiveRecords=()=>old.promise;
  const store=useProactiveStore();const load=store.refreshRecords();
  store.upsertRecord({id:'record',status:'succeeded'});
  old.resolve([{id:'record',status:'executing'}]);await load;
  assert.equal(store.records[0].status,'succeeded');
});

test('会话元数据修改和 Agent 删除使旧列表及选择流程失效', async () => {
  for (const action of ['rename', 'archive', 'forgetAgent', 'deleteSelected']) {
    setActivePinia(createPinia());
    const old = deferred();
    apis.listSessions = () => old.promise;
    apis.renameSession = async () => ({id:'s',agentID:'a',title:'new'});
    apis.setSessionArchived = async () => ({id:'s',agentID:'a',archived:true});
    const store = useSessionStore();
    store.agentID = 'a'; store.selectedID = 's';
    store.cacheAgentSessions('a',[{id:'s',title:'old'}]);
    const load = store.loadForAgent('a');
    if (action === 'rename') await store.rename('s','new');
    if (action === 'archive') await store.setArchived('s',true);
    if (action === 'forgetAgent') store.forgetAgent('a');
    if (action === 'deleteSelected') await store.remove('s');
    old.resolve([{id:'s',title:'old'}]); await load;
    assert.equal(store.loading,false);
    if (action === 'rename') assert.equal(store.items[0].title,'new');
    else if (action === 'archive') {
      assert.equal(store.items[0].archived,true);
      assert.equal(store.selectedID,'');
    } else {
      assert.equal(store.items.length,0);
      assert.equal(store.selectedID,'');
    }
  }
});

test('列表乱序与旧错误不能干扰新请求 loading，也不影响其他 Agent', async () => {
  for (const fails of [false,true]) {
    setActivePinia(createPinia());
    const old = deferred(), next = deferred(); let calls = 0;
    apis.listSessions = id => id === 'other' ? Promise.resolve([{id:'other-session'}]) : [old.promise,next.promise][calls++];
    const store = useSessionStore(); store.agentID = 'a';
    const first = store.loadAgentSessions('a',{force:true});
    const second = store.loadAgentSessions('a',{force:true});
    await store.loadAgentSessions('other');
    if (fails) old.reject(new Error('stale')); else old.resolve([{id:'old'}]);
    await first;
    assert.equal(store.loadingAgents.a,true);
    assert.equal(store.itemsByAgent.other[0].id,'other-session');
    next.resolve([{id:'new'}]); await second;
    assert.equal(store.items[0].id,'new');
    assert.equal(store.loadingAgents.a,undefined);
  }
});

test('MCP 强制刷新后，旧请求结束不能清除新请求或其 loading', async () => {
  const old = deferred(), next = deferred(); let calls = 0;
  apis.listMCPServers = () => [old.promise,next.promise][calls++];
  const store = useMCPStore();
  const first = store.load(); const second = store.load({force:true});
  old.reject(new Error('stale')); await first;
  assert.equal(store.loading,true);
  const shared = store.load();
  assert.equal(calls,2);
  next.resolve([{id:'new'}]); await Promise.all([second,shared]);
  assert.equal(store.loading,false);
  assert.equal(store.servers[0].id,'new');
});

test('模型乱序响应不撤销新目录，保存失败也不吞掉正常读取', async () => {
  const old = deferred(), next = deferred(); let calls = 0;
  apis.getModelState = () => [old.promise,next.promise][calls++];
  const store = useModelStore();
  const first = store.load(), second = store.load();
  next.resolve({revision:2,models:[{id:'new'}]}); await second;
  old.resolve({revision:1,models:[{id:'old'}]}); await first;
  assert.equal(store.models[0].id,'new'); assert.equal(store.revision,2);
  const pending = deferred(); apis.getModelState = () => pending.promise;
  apis.updateMultimediaConfig = async () => {throw new Error('save failed')};
  const load = store.load();
  await assert.rejects(store.updateMultimediaConfig({}),/save failed/);
  pending.resolve({revision:3,models:[{id:'valid'}]}); await load;
  assert.equal(store.models[0].id,'valid');
});

test('Skill Store 删除后的目录刷新不能被旧读取覆盖', async () => {
  const old = deferred(); let calls = 0;
  apis.getSkillState = () => ++calls === 1 ? old.promise : Promise.resolve({skills:[]});
  const store = useSkillStore(); const load = store.load();
  await store.remove('deleted');
  old.resolve({skills:[{name:'deleted'}]}); await load;
  assert.deepEqual(store.items,[]);
});

test('主动助手的状态和记录独立加载，当前错误仍报告，旧错误不回滚事件', async () => {
  const old = deferred(); apis.listProactiveRecords = () => old.promise;
  apis.getProactiveStatus = async () => ({running:true});
  const store = useProactiveStore(); const load = store.refreshRecords();
  store.upsertRecord({id:'r',status:'succeeded'});
  await store.refreshStatus();
  old.reject(new Error('stale')); await load;
  assert.equal(store.status.running,true);
  assert.equal(store.records[0].status,'succeeded');
  apis.listProactiveRecords = async () => {throw new Error('current failure')};
  await assert.rejects(store.refreshRecords(),/current failure/);
});
