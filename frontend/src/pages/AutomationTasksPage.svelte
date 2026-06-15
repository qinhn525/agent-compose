<script lang="ts">
  import { onMount } from 'svelte';
  import ExperimentOutlined from '@ant-design/icons-svg/es/asn/ExperimentOutlined';
  import FilterOutlined from '@ant-design/icons-svg/es/asn/FilterOutlined';

  import AntIcon from '../components/AntIcon.svelte';
  import {
    deleteAutomationTask,
    getAutomationTask,
    listAutomationTasks,
    runAutomationTaskNow,
    saveAutomationTask,
    setAutomationTaskEnabled,
    setAutomationTriggerEnabled,
    validateAutomationTask,
    type AutomationTrigger,
    type AutomationTask,
  } from '../api/loaders';
  import { listAgentDefinitions, type AgentDefinition } from '../api/agents';
  import { listCapabilitySets, type CapabilitySet } from '../api/config';
  import { appPath } from '../paths';

  type EnvItem = { name: string; value: string; secret: boolean };

  type DraftTask = {
    id: string;
    name: string;
    description: string;
    enabled: boolean;
    configMode: 'form' | 'code';
    agentId: string;
    capsetIds: string[];
    defaultAgent: string;
    triggerType: 'cron' | 'interval' | 'event' | 'timeout';
    triggerName: string;
    taskInput: string;
    guestImage: string;
    concurrencyPolicy: 'skip_if_running' | 'parallel';
    sessionPolicy: 'reuse_session' | 'new_session';
    loaderScript: string;
    envItems: EnvItem[];
    codeValidationStatus: 'unvalidated' | 'passed' | 'failed';
  };

  let loading = true;
  let error = '';
  let tasks: AutomationTask[] = [];
  let agents: AgentDefinition[] = [];
  let capsets: CapabilitySet[] = [];
  let keyword = '';
  let triggerFilter = '';
  let drawerOpen = false;
  let debugTask: AutomationTask | null = null;
  let draft: DraftTask = emptyDraft();
  let debugPayload = '{}';
  let saving = false;
  let validating = false;
  let draftLoading = false;
  let runningTaskId = '';
  let actionMessage = '';
  let draftTriggers: AutomationTrigger[] = [];
  let codeScrollTop = 0;
  let codeScrollLeft = 0;

  $: activeAgents = agents.filter((agent) => !agent.deletedAt && agent.enabled);
  $: agentsById = new Map(agents.map((agent) => [agent.id, agent]));
  $: selectedDraftAgent = agentsById.get(draft.agentId) ?? null;
  $: filteredTasks = tasks.filter((task) =>
    [task.name, task.description, task.defaultAgent, agentLabel(task.agentId), task.id].join(' ').toLowerCase().includes(keyword.toLowerCase()) &&
    (!triggerFilter || task.runtime === triggerFilter),
  );

  onMount(load);

  async function load(): Promise<void> {
    loading = true;
    error = '';
    try {
      [tasks, agents] = await Promise.all([
        listAutomationTasks(),
        listAgentDefinitions(),
      ]);
      // Capability sets are optional: a missing/unreachable gateway must not
      // block the task list.
      try {
        capsets = await listCapabilitySets();
      } catch {
        capsets = [];
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  function emptyDraft(): DraftTask {
    return {
      id: '',
      name: '',
      description: '',
      enabled: false,
      configMode: 'code',
      agentId: '',
      capsetIds: [],
      defaultAgent: '',
      triggerType: 'cron',
      triggerName: '',
      taskInput: '',
      guestImage: '',
      concurrencyPolicy: 'skip_if_running',
      sessionPolicy: 'new_session',
      loaderScript: defaultCodeTemplate(),
      envItems: [],
      codeValidationStatus: 'unvalidated',
    };
  }

  function defaultCodeTemplate(): string {
    return `function main(payload) {
  scheduler.log("manual run", { payload });
  return { ok: true, payload: payload ?? null };
}

scheduler.interval("heartbeat", function heartbeat() {
  scheduler.log("heartbeat", { at: new Date().toISOString() });
}, 60000);
`;
  }

  function openCreate(): void {
    draft = emptyDraft();
    // New tasks default to every available capset selected.
    draft.capsetIds = capsets.map((capset) => capset.id);
    draftTriggers = [];
    drawerOpen = true;
  }

  function toggleTaskCapset(id: string, checked: boolean): void {
    const ids = checked ? [...draft.capsetIds, id] : draft.capsetIds.filter((value) => value !== id);
    draft = { ...draft, capsetIds: ids };
  }

  async function openEdit(task: AutomationTask): Promise<void> {
    draft = {
      id: task.id,
      name: task.name,
      description: task.description,
      enabled: task.enabled,
      configMode: 'code',
      agentId: task.agentId,
      capsetIds: task.capsetIds,
      defaultAgent: task.defaultAgent,
      triggerType: triggerTypeFromRuntime(task.runtime),
      triggerName: task.triggerCount > 0 ? '默认触发规则' : '',
      taskInput: '',
      guestImage: task.guestImage,
      concurrencyPolicy: task.concurrencyPolicy === 'parallel' ? 'parallel' : 'skip_if_running',
      sessionPolicy: task.sessionPolicy === 'reuse_session' ? 'reuse_session' : 'new_session',
      loaderScript: defaultCodeTemplate(),
      envItems: [],
      codeValidationStatus: 'unvalidated',
    };
    drawerOpen = true;
    draftTriggers = [];
    draftLoading = true;
    error = '';
    try {
      const detail = await getAutomationTask(task.id);
      draftTriggers = detail.triggers;
      draft = {
        ...draft,
        id: detail.id,
        name: detail.name,
        description: detail.description,
        enabled: detail.enabled,
        configMode: 'code',
        loaderScript: detail.script || defaultCodeTemplate(),
        agentId: detail.agentId,
        capsetIds: detail.capsetIds,
        defaultAgent: detail.defaultAgent,
        guestImage: detail.guestImage,
        concurrencyPolicy: detail.concurrencyPolicy === 'parallel' ? 'parallel' : 'skip_if_running',
        sessionPolicy: detail.sessionPolicy === 'reuse_session' ? 'reuse_session' : 'new_session',
        envItems: detail.envItems.map((item) => ({ ...item })),
        codeValidationStatus: 'passed',
      };
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      draftLoading = false;
    }
  }

  async function validateDraft(): Promise<void> {
    if (draftLoading) return;
    validating = true;
    error = '';
    try {
      const result = await validateAutomationTask(scriptForDraft(draft), 'scheduler');
      draftTriggers = result.triggers;
      draft.codeValidationStatus = 'passed';
      actionMessage = '校验通过';
    } catch (err) {
      draft.codeValidationStatus = 'failed';
      error = err instanceof Error ? err.message : String(err);
    } finally {
      validating = false;
    }
  }

  async function saveDraft(debug = false): Promise<void> {
    if (draftLoading) return;
    if (!draft.name.trim()) {
      error = '任务名称必填';
      return;
    }
    const selectedAgent = agentsById.get(draft.agentId);
    if (!selectedAgent || selectedAgent.deletedAt) {
      error = '请选择可用智能体';
      return;
    }
    saving = true;
    error = '';
    try {
      const script = scriptForDraft(draft);
      const validation = await validateAutomationTask(script, 'scheduler');
      draftTriggers = validation.triggers;
      draft.codeValidationStatus = 'passed';
      const task = await saveAutomationTask({
        id: draft.id || undefined,
        name: draft.name,
        description: draft.description,
        runtime: 'scheduler',
        script,
        workspaceId: selectedAgent.workspaceId,
        driver: selectedAgent.driver,
        guestImage: draft.guestImage || selectedAgent.guestImage,
        agentId: selectedAgent.id,
        capsetIds: draft.capsetIds,
        defaultAgent: selectedAgent.provider,
        sessionPolicy: draft.sessionPolicy,
        concurrencyPolicy: draft.concurrencyPolicy,
        enabled: draft.enabled && draft.codeValidationStatus !== 'failed',
        envItems: draft.envItems,
      });
      tasks = [task, ...tasks.filter((item) => item.id !== task.id)];
      drawerOpen = false;
      actionMessage = draft.id ? '自动化任务已更新' : '自动化任务已创建';
      if (debug && draft.codeValidationStatus !== 'failed') {
        debugTask = task;
        debugPayload = payloadForDraft(draft);
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function toggleTask(task: AutomationTask): Promise<void> {
    error = '';
    try {
      const updated = await setAutomationTaskEnabled(task.id, !task.enabled);
      tasks = tasks.map((item) => item.id === task.id ? updated : item);
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    }
  }

  async function toggleTrigger(trigger: AutomationTrigger): Promise<void> {
    if (!draft.id) return;
    error = '';
    try {
      const detail = await setAutomationTriggerEnabled(draft.id, trigger.triggerId, !trigger.enabled);
      draftTriggers = detail.triggers;
      tasks = tasks.map((item) => item.id === detail.id ? detail : item);
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    }
  }

  function triggerKindLabel(kind: string): string {
    if (kind.includes('INTERVAL')) return '周期触发';
    if (kind.includes('EVENT')) return '事件触发';
    if (kind.includes('TIMEOUT')) return '延迟触发';
    if (kind.includes('CRON')) return '定时触发';
    return kind || '-';
  }

  async function deleteTask(task: AutomationTask): Promise<void> {
    error = '';
    try {
      await deleteAutomationTask(task.id);
      tasks = tasks.filter((item) => item.id !== task.id);
      actionMessage = '自动化任务已删除';
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    }
  }

  async function runDebugTask(): Promise<void> {
    if (!debugTask) return;
    error = '';
    try {
      JSON.parse(debugPayload || '{}');
    } catch {
      error = '模拟触发上下文必须是合法 JSON';
      return;
    }
    try {
      const run = await runAutomationTaskNow(debugTask.id, debugPayload || '{}');
      closeDebugDrawer();
      window.location.assign(appPath(`/runs?type=automation_run&runId=${encodeURIComponent(run.id)}`));
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    }
  }

  async function runTaskNow(task: AutomationTask): Promise<void> {
    runningTaskId = task.id;
    error = '';
    try {
      const detail = await getAutomationTask(task.id);
      const run = await runAutomationTaskNow(task.id, payloadForTask(detail));
      window.location.assign(appPath(`/runs?type=automation_run&runId=${encodeURIComponent(run.id)}`));
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      runningTaskId = '';
    }
  }

  function closeDrawer(): void {
    drawerOpen = false;
  }

  function closeDrawerFromKey(event: KeyboardEvent): void {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      closeDrawer();
    }
  }

  function closeDebugDrawer(): void {
    debugTask = null;
    debugPayload = '{}';
  }

  function closeDebugDrawerFromKey(event: KeyboardEvent): void {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      closeDebugDrawer();
    }
  }

  function triggerTypeFromRuntime(runtime: string): DraftTask['triggerType'] {
    if (runtime === 'interval' || runtime === 'event' || runtime === 'timeout' || runtime === 'cron') {
      return runtime;
    }
    return 'cron';
  }

  function scriptForDraft(item: DraftTask): string {
    if (item.configMode === 'code') {
      return item.loaderScript;
    }
    return formScript(item);
  }

  function payloadForDraft(item: DraftTask): string {
    const provider = providerForDraft(item);
    return JSON.stringify({
      taskInput: item.taskInput,
      triggerName: item.triggerName || 'manual',
      agent: provider,
    }, null, 2);
  }

  function payloadForTask(task: AutomationTask): string {
    return JSON.stringify({
      taskInput: task.description || task.name,
      triggerName: 'manual',
      agent: task.defaultAgent || 'codex',
    }, null, 2);
  }

  function formScript(item: DraftTask): string {
    const taskInput = JSON.stringify(item.taskInput || '');
    const triggerName = JSON.stringify(item.triggerName || 'default');
    const agent = JSON.stringify(providerForDraft(item));
    const handlerName = safeIdentifier(item.triggerName || item.triggerType || 'default');
    const body = `function main(payload) {
  const taskInput = payload && payload.taskInput ? payload.taskInput : ${taskInput};
  scheduler.log("automation task started", { taskInput, agent: ${agent} });
  return { ok: true, taskInput, agent: ${agent}, payload: payload ?? null };
}
`;
    if (item.triggerType === 'interval') {
      return `${body}
scheduler.interval(${triggerName}, function ${handlerName}(payload) {
  return main(payload);
}, 60000);
`;
    }
    if (item.triggerType === 'timeout') {
      return `${body}
scheduler.timeout(${triggerName}, function ${handlerName}(payload) {
  return main(payload);
}, 60000);
`;
    }
    if (item.triggerType === 'event') {
      return `${body}
scheduler.on("agent-compose.*", ${triggerName}, function ${handlerName}(event) {
  return main(event);
});
`;
    }
    return `${body}
scheduler.cron(${triggerName}, "0 8 * * *", function ${handlerName}(payload) {
  return main(payload);
});
`;
  }

  function triggerSummary(task: AutomationTask): string {
    if (task.triggerCount <= 0) return '未配置';
    if (task.triggerCount === 1) return '1 条规则';
    return `${task.triggerCount} 条规则`;
  }

  function safeIdentifier(value: string): string {
    const normalized = value.replace(/[^A-Za-z0-9_$]/g, '_').replace(/^[^A-Za-z_$]+/, '');
    return normalized || 'handler';
  }

  function providerForDraft(item: DraftTask): string {
    return agentsById.get(item.agentId)?.provider || item.defaultAgent || 'codex';
  }

  function agentLabel(agentId: string): string {
    const agent = agentsById.get(agentId);
    if (!agent) return agentId || '未绑定智能体';
    return agent.deletedAt ? `${agent.name}（已删除）` : agent.name;
  }

  function selectDraftAgent(agentId: string): void {
    const agent = agentsById.get(agentId);
    draft.agentId = agentId;
    if (!agent) {
      draft.defaultAgent = '';
      return;
    }
    draft.defaultAgent = agent.provider;
    if (!draft.guestImage && agent.guestImage) {
      draft.guestImage = agent.guestImage;
    }
  }

  function addEnvItem(): void {
    draft.envItems = [...draft.envItems, { name: '', value: '', secret: false }];
  }

  function removeEnvItem(index: number): void {
    draft.envItems = draft.envItems.filter((_, itemIndex) => itemIndex !== index);
  }

  function syncCodeScroll(event: Event): void {
    const target = event.currentTarget as HTMLTextAreaElement;
    codeScrollTop = target.scrollTop;
    codeScrollLeft = target.scrollLeft;
  }

  function highlightedJavaScript(source: string): string {
    const pattern = /(\/\*[\s\S]*?\*\/|\/\/[^\n]*|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:const|let|var|function|return|if|else|for|while|switch|case|break|continue|try|catch|finally|throw|new|class|extends|import|export|from|async|await|true|false|null|undefined)\b|\b\d+(?:\.\d+)?\b|\b[A-Za-z_$][\w$]*(?=\s*\())/g;
    return escapeHTML(source).replace(pattern, (token) => {
      const className = javascriptTokenClass(token);
      return className ? `<span class="${className}">${token}</span>` : token;
    });
  }

  function javascriptTokenClass(token: string): string {
    if (token.startsWith('//') || token.startsWith('/*')) return 'tok-comment';
    if (token.startsWith('"') || token.startsWith("'") || token.startsWith('`')) return 'tok-string';
    if (/^\d/.test(token)) return 'tok-number';
    if (/^(const|let|var|function|return|if|else|for|while|switch|case|break|continue|try|catch|finally|throw|new|class|extends|import|export|from|async|await|true|false|null|undefined)$/.test(token)) return 'tok-keyword';
    return 'tok-function';
  }

  function escapeHTML(value: string): string {
    return value
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }
</script>

{#if error}
  <div class="alert danger">{error}</div>
{/if}
{#if actionMessage}
  <div class="alert success">{actionMessage}</div>
{/if}

<section class="panel tasks-panel">
  <div class="list-toolbar">
    <div class="run-command-metrics compact">
      <button><span>全部</span><b>{tasks.length}</b></button>
      <button><span>启用</span><b>{tasks.filter((task) => task.enabled).length}</b></button>
      <button><span>暂停</span><b>{tasks.filter((task) => !task.enabled).length}</b></button>
      <button><span>触发规则</span><b>{tasks.reduce((total, task) => total + task.triggerCount, 0)}</b></button>
    </div>
    <div class="filters">
      <input class="filter-keyword" placeholder="按任务名称、描述、智能体、触发规则筛选" bind:value={keyword}>
      <select bind:value={triggerFilter}><option value="">触发类型</option><option value="cron">定时触发</option><option value="interval">周期触发</option><option value="event">事件触发</option><option value="timeout">延迟触发</option></select>
      <button on:click={load}>{loading ? '刷新中...' : '刷新'}</button>
      <button class="primary" on:click={openCreate}>创建自动化任务</button>
    </div>
  </div>

  {#if loading && tasks.length === 0}
    <div class="empty">加载中...</div>
  {:else if filteredTasks.length === 0}
    <div class="empty-state">
      <div class="empty-state-icon">
        <AntIcon definition={tasks.length === 0 ? ExperimentOutlined : FilterOutlined} />
      </div>
      {#if tasks.length === 0}
        <h3>还没有自动化任务</h3>
        <p>自动化任务通过触发规则（定时 / 周期 / 事件）自动运行智能体。创建第一个任务后即可在这里管理。</p>
        <div class="empty-state-actions">
          <button class="primary" on:click={openCreate}>创建自动化任务</button>
        </div>
      {:else}
        <h3>没有匹配的自动化任务</h3>
        <p>当前共有 {tasks.length} 个任务，但都不满足筛选条件。试试调整或清除筛选。</p>
        <div class="empty-state-actions">
          <button on:click={() => { keyword = ''; triggerFilter = ''; }}>清除筛选</button>
        </div>
      {/if}
    </div>
  {:else}
    <div class="task-list compact">
      <div class="task-list-head">
        <span>任务</span>
        <span>触发</span>
        <span>状态</span>
        <span>运行</span>
        <span>操作</span>
      </div>
      {#each filteredTasks as task}
        <section class="task-row">
          <div class="task-main">
            <div class="task-title">
              <b>{task.name || task.id}</b>
              <span class="tag">scheduler</span>
            </div>
            <p>{task.description || '无描述'}</p>
            <small>{agentLabel(task.agentId)} · {task.defaultAgent || '-'}</small>
          </div>
          <div class="task-cell">
            <b>{task.triggerCount}</b>
            <small>{triggerSummary(task)}</small>
          </div>
          <div class="task-cell">
            <span class="chip {task.enabled ? 'green' : 'amber'}">{task.enabled ? '已启用' : '已暂停'}</span>
            <small>{task.lastError ? '存在错误' : '健康'}</small>
          </div>
          <div class="task-cell">
            <b>空闲</b>
            <small>最近运行 -</small>
          </div>
          <div class="task-actions">
            <button on:click={() => toggleTask(task)}>{task.enabled ? '暂停' : '开启'}</button>
            <button disabled={Boolean(task.lastError)} on:click={() => { debugTask = task; debugPayload = '{}'; }}>调试</button>
            <button on:click={() => openEdit(task)}>编辑</button>
            <button disabled={runningTaskId === task.id} on:click={() => runTaskNow(task)}>{runningTaskId === task.id ? '运行中...' : '运行'}</button>
            <button on:click={() => deleteTask(task)}>删除</button>
          </div>
        </section>
      {/each}
    </div>
  {/if}
</section>

{#if drawerOpen}
  <div class="drawer-mask" role="button" tabindex="0" aria-label="关闭抽屉" on:click={closeDrawer} on:keydown={closeDrawerFromKey}></div>
  <aside class="drawer wide">
    <div class="drawer-head">
      <h2>{draft.id ? '编辑自动化任务' : '创建自动化任务'}</h2>
      <div class="toolbar">
        <button on:click={() => (drawerOpen = false)}>取消</button>
        <button disabled={draftLoading || validating || saving} on:click={validateDraft}>{validating ? '校验中...' : '校验'}</button>
        <button disabled={draftLoading || saving} on:click={() => saveDraft(false)}>{saving ? '保存中...' : '保存'}</button>
        <button class="primary" disabled={draftLoading || saving || draft.codeValidationStatus === 'failed'} on:click={() => saveDraft(true)}>保存并调试</button>
      </div>
    </div>
    <div class="drawer-body">
      {#if draftLoading}
        <div class="alert">正在加载后端任务配置...</div>
      {/if}
      <form class="drawer-form" on:submit|preventDefault={() => saveDraft(false)}>
        <section class="form-section">
          <h3>基础信息</h3>
          <label class="form-item"><span>任务名称</span><input bind:value={draft.name} required></label>
          <label class="form-item"><span>描述</span><textarea rows="2" bind:value={draft.description}></textarea></label>
          <fieldset class="radio-field">
            <legend>任务状态</legend>
            <div class="radio-group">
              <label><input type="radio" bind:group={draft.enabled} value={true} disabled={draft.codeValidationStatus === 'failed'}>任务已启用</label>
              <label><input type="radio" bind:group={draft.enabled} value={false}>任务已暂停</label>
            </div>
          </fieldset>
        </section>

        <section class="form-section">
          <div class="form-section-head">
            <h3>任务编排</h3>
            <div class="detail-tabs mode-tabs" aria-label="任务编排方式">
              <button type="button" class:active={draft.configMode === 'code'} on:click={() => (draft.configMode = 'code')}>代码编排</button>
              <button type="button" class:active={draft.configMode === 'form'} on:click={() => (draft.configMode = 'form')}>表单配置</button>
            </div>
          </div>
          {#if draft.configMode === 'code'}
            <span class="chip {draft.codeValidationStatus === 'failed' ? 'red' : draft.codeValidationStatus === 'passed' ? 'green' : 'amber'}">{draft.codeValidationStatus === 'failed' ? '校验失败' : draft.codeValidationStatus === 'passed' ? '校验通过' : '未校验'}</span>
            <label class="form-item">
              <div class="code-editor-wrap">
                <pre class="code-highlight" aria-hidden="true" style={`transform: translate(${-codeScrollLeft}px, ${-codeScrollTop}px);`}>{@html highlightedJavaScript(draft.loaderScript)}</pre>
                <textarea
                  class="code-editor"
                  rows="18"
                  spellcheck="false"
                  bind:value={draft.loaderScript}
                  on:input={() => (draft.codeValidationStatus = 'unvalidated')}
                  on:scroll={syncCodeScroll}
                ></textarea>
              </div>
            </label>
          {:else}
            <div class="inline-fields">
              <label>触发类型<select bind:value={draft.triggerType}><option value="cron">定时触发</option><option value="interval">周期触发</option><option value="event">事件触发</option><option value="timeout">延迟触发</option></select></label>
              <label>规则名称<input bind:value={draft.triggerName} placeholder="默认触发规则"></label>
            </div>
            <label class="form-item"><span>任务输入</span><textarea rows="5" bind:value={draft.taskInput} placeholder="任务输入，必填多行文本 / Markdown"></textarea></label>
          {/if}
        </section>

        <section class="form-section">
          <h3>平台配置</h3>
          <label class="form-item">
            <span>关联智能体</span>
            <select bind:value={draft.agentId} on:change={(event) => selectDraftAgent(event.currentTarget.value)} required>
              <option value="">请选择智能体</option>
              {#each activeAgents as agent}
                <option value={agent.id}>{agent.name} · {agent.provider}</option>
              {/each}
              {#if draft.agentId && !activeAgents.some((agent) => agent.id === draft.agentId)}
                <option value={draft.agentId}>{agentLabel(draft.agentId)}</option>
              {/if}
            </select>
          </label>
          {#if draft.agentId}
            <div class="descriptions-small">
              <div><span>Provider</span><b>{providerForDraft(draft)}</b></div>
              <div><span>运行环境</span><b>{selectedDraftAgent?.driver || '默认'}</b></div>
              <div><span>工作区</span><b>{selectedDraftAgent?.workFiles.workspaceName || selectedDraftAgent?.workspaceId || '默认'}</b></div>
              <div><span>Guest 镜像</span><b>{selectedDraftAgent?.guestImage || '默认'}</b></div>
            </div>
          {/if}
          <div class="form-item">
            <span>能力集（可多选）</span>
            {#if capsets.length === 0}
              <p class="form-muted">无可用能力集</p>
            {:else}
              <div class="capset-checks">
                {#each capsets as capset}
                  <label class="capset-check">
                    <input type="checkbox" checked={draft.capsetIds.includes(capset.id)} on:change={(event) => toggleTaskCapset(capset.id, event.currentTarget.checked)}>
                    <span>{capset.name || capset.id}</span>
                  </label>
                {/each}
              </div>
            {/if}
          </div>
          <label class="form-item"><span>Guest 镜像</span><input bind:value={draft.guestImage} placeholder="使用智能体配置"></label>
          <section class="form-section agent-env-section">
            <div class="form-section-head">
              <h3>环境变量</h3>
              <button type="button" on:click={addEnvItem}>添加变量</button>
            </div>
            {#if draft.envItems.length === 0}
              <p class="form-muted">未配置环境变量。</p>
            {:else}
              <div class="agent-env-list">
                {#each draft.envItems as item, index}
                  <div class="agent-env-row">
                    <label class="form-item"><span>名称</span><input bind:value={item.name} placeholder="ENV_NAME"></label>
                    <label class="form-item"><span>值</span><input bind:value={item.value} type={item.secret ? 'password' : 'text'} placeholder="变量值"></label>
                    <label class="form-item checkbox-row"><input type="checkbox" bind:checked={item.secret}><span>敏感</span></label>
                    <button type="button" on:click={() => removeEnvItem(index)}>删除</button>
                  </div>
                {/each}
              </div>
            {/if}
          </section>
          <fieldset class="radio-field">
            <legend>并发策略</legend>
            <div class="radio-group">
              <label><input type="radio" bind:group={draft.concurrencyPolicy} value="skip_if_running">已有运行时跳过新触发</label>
              <label><input type="radio" bind:group={draft.concurrencyPolicy} value="parallel" on:change={() => (draft.sessionPolicy = 'new_session')}>允许并行运行</label>
            </div>
          </fieldset>
          <fieldset class="radio-field">
            <legend>会话策略</legend>
            <div class="radio-group">
              <label><input type="radio" bind:group={draft.sessionPolicy} value="reuse_session" disabled={draft.concurrencyPolicy === 'parallel'}>继续使用同一会话</label>
              <label><input type="radio" bind:group={draft.sessionPolicy} value="new_session">每次执行新建会话</label>
            </div>
          </fieldset>
        </section>

        <section class="form-section">
          <h3>触发规则</h3>
          {#if draftTriggers.length === 0}
            <div class="empty">校验后展示脚本识别出的触发规则。</div>
          {:else}
            <div class="config-list">
              {#each draftTriggers as trigger}
                <div class="config-list-item">
                  <div>
                    <b>{trigger.triggerId || '自动生成触发规则'}</b>
                    <p>{triggerKindLabel(trigger.kind)} · {trigger.topic || trigger.intervalMs || trigger.specJson || '-'}</p>
                  </div>
                  <div class="toolbar">
                    <span class="chip {trigger.enabled ? 'green' : 'amber'}">{trigger.enabled ? '已启用' : '已暂停'}</span>
                    <button disabled={!draft.id} on:click={() => toggleTrigger(trigger)}>{trigger.enabled ? '暂停' : '开启'}</button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        </section>

      </form>
    </div>
  </aside>
{/if}

{#if debugTask}
  <div class="drawer-mask" role="button" tabindex="0" aria-label="关闭调试抽屉" on:click={closeDebugDrawer} on:keydown={closeDebugDrawerFromKey}></div>
  <aside class="drawer">
    <div class="drawer-head">
      <h2>自动化任务调试运行</h2>
      <div class="toolbar">
        <button on:click={() => (debugTask = null)}>取消</button>
        <button class="primary" on:click={runDebugTask}>运行</button>
      </div>
    </div>
    <div class="drawer-body">
      <div class="descriptions-small">
        <div><span>任务名称</span><b>{debugTask.name}</b></div>
        <div><span>任务状态</span><b>{debugTask.enabled ? '任务已启用' : '任务已暂停'}，调试运行不受暂停影响</b></div>
      </div>
      <label class="form-item"><span>模拟触发上下文 JSON</span><textarea rows="8" bind:value={debugPayload} placeholder={'{}'}></textarea></label>
    </div>
  </aside>
{/if}

<style>
  .capset-checks {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 18px;
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: #fbfdff;
  }
  .capset-check {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    cursor: pointer;
  }
  .capset-check input {
    width: auto;
    margin: 0;
  }
</style>
