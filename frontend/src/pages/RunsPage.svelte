<script lang="ts">
  import CopyOutlined from '@ant-design/icons-svg/es/asn/CopyOutlined';
  import InboxOutlined from '@ant-design/icons-svg/es/asn/InboxOutlined';
  import FilterOutlined from '@ant-design/icons-svg/es/asn/FilterOutlined';
  import { createEventDispatcher, onDestroy, onMount, tick } from 'svelte';
  import { Terminal } from 'xterm';
  import { FitAddon } from '@xterm/addon-fit';

  import AntIcon from '../components/AntIcon.svelte';
  import { getAutomationRun, listAutomationEvents, listAutomationTasks, listRecentAutomationRuns, runAutomationTaskNow } from '../api/loaders';
  import { listAgentDefinitions, type AgentDefinition } from '../api/agents';
  import {
    listWorkSessionCells,
    listWorkSessionEvents,
    listWorkSessions,
    resumeWorkSession,
    sendWorkSessionMessageStream,
    stopWorkSession,
    watchWorkSession,
  } from '../api/sessions';
  import { automationRunToRun, sessionToRun, type ProductRun } from '../model/runs';
  import { CellType } from '../gen/proto/agentcompose/v1/agentcompose_pb.js';
  import { formatBeijingTime } from '../time';
  import { currentQueryParams, updateQueryParams } from '../url';

  const dispatch = createEventDispatcher<{ debug: string }>();
  type TerminalActionParams = { id?: string; text: string; running: boolean };

  let loading = true;
  let error = '';
  let activeTab: 'all' | 'work_session' | 'automation_run' = 'all';
  let statusFilter = 'all';
  let agentFilter = '';
  let triggerFilter = '';
  let taskFilter = '';
  let keyword = '';
  let runs: ProductRun[] = [];
  let agentDefinitions: AgentDefinition[] = [];
  let selectedRunId = '';
  let messageText = '';
  let activeDetailTab: 'result' | 'input' | 'events' | 'artifacts' = 'result';
  let sendingMessage = false;
  let detailLoading = false;
  let sessionAction: { runId: string; type: 'stop' | 'resume' } | null = null;
  let automationActionRunId = '';
  let copiedId = '';
  let copyNotice: { text: string; ok: boolean } | null = null;
  let watchAbort: AbortController | null = null;
  let messageAbort: AbortController | null = null;
  let copiedTimer: ReturnType<typeof setTimeout> | null = null;
  let pendingCellChunks = new Map<string, string>();
  let messageScroll: HTMLDivElement | null = null;
  let messageScrollFrame = 0;
  let terminalVisibleIds = new Set<string>();
  const terminalHeights = new Map<string, number>();
  const terminalNodeIds = new Map<Element, string>();
  let terminalObserver: IntersectionObserver | null = null;
  let terminalObserverRoot: HTMLDivElement | null = null;

  const terminalTheme = {
    background: '#07111a',
    foreground: '#d8e2ec',
    cursor: '#ffbf69',
    selectionBackground: 'rgba(255, 191, 105, 0.28)',
  };
  $: visibleRuns = filterRuns(runs, {
    tab: activeTab,
    status: statusFilter,
    agent: agentFilter,
    trigger: triggerFilter,
    task: taskFilter,
    keyword,
  });
  $: selectedRun = visibleRuns.find((run) => run.id === selectedRunId) || visibleRuns[0] || null;
  $: agentOptions = buildAgentOptions(agentDefinitions, runs);
  $: if (selectedRun?.type === 'work_session' && activeDetailTab === 'input') {
    activeDetailTab = 'result';
    updateURL(true);
  }
  $: syncTerminalObserverRoot(messageScroll);
  onMount(() => {
    syncFromURL();
    void load();
    const handleVisible = () => {
      if (document.visibilityState === 'visible') {
        resumeVisibleWatch();
        if (!loading) {
          void load();
        }
      } else {
        stopWatching();
      }
    };
    const refreshOnFocus = () => {
      if (document.visibilityState === 'visible' && !loading) {
        resumeVisibleWatch();
        void load();
      }
    };
    window.addEventListener('popstate', syncFromURL);
    window.addEventListener('focus', refreshOnFocus);
    document.addEventListener('visibilitychange', handleVisible);
    return () => {
      window.removeEventListener('popstate', syncFromURL);
      window.removeEventListener('focus', refreshOnFocus);
      document.removeEventListener('visibilitychange', handleVisible);
    };
  });

  onDestroy(() => {
    stopWatching();
    stopSendingMessage();
    if (copiedTimer) {
      clearTimeout(copiedTimer);
    }
    if (messageScrollFrame) {
      cancelAnimationFrame(messageScrollFrame);
    }
    terminalObserver?.disconnect();
    terminalObserver = null;
  });

  function tabFromValue(value: string | null): 'all' | 'work_session' | 'automation_run' {
    if (value === 'work_session' || value === 'automation_run') {
      return value;
    }
    return 'all';
  }

  function detailTabFromValue(value: string | null): typeof activeDetailTab {
    if (value === 'input' || value === 'events' || value === 'artifacts') {
      return value;
    }
    return 'result';
  }

  function syncFromURL(): void {
    const params = currentQueryParams();
    activeTab = tabFromValue(params.get('type'));
    statusFilter = normalizeStatusFilter(activeTab, params.get('status') || 'all');
    agentFilter = '';
    triggerFilter = normalizeTriggerFilter(params.get('trigger') || '');
    taskFilter = '';
    keyword = params.get('q') || '';
    selectedRunId = params.get('runId') || '';
    activeDetailTab = detailTabFromValue(params.get('detailTab'));
    if (params.has('agent') || params.has('agentId')) {
      updateURL(true);
    }
  }

  function updateURL(replace = false): void {
    updateQueryParams({
      type: activeTab === 'all' ? null : activeTab,
      status: statusFilter === 'all' ? null : statusFilter,
      agent: null,
      agentId: null,
      trigger: triggerFilter,
      taskId: null,
      q: keyword,
      runId: selectedRunId,
      detailTab: activeDetailTab === 'result' ? null : activeDetailTab,
    }, replace);
  }

  function applyFilters(replace = false): void {
    syncSelectedRunWithFilters();
    updateURL(replace);
  }

  function setTab(tab: 'all' | 'work_session' | 'automation_run'): void {
    activeTab = tab;
    statusFilter = 'all';
    applyFilters();
  }

  function updateTypeFilterValue(value: string): void {
    activeTab = tabFromValue(value);
    statusFilter = 'all';
    applyFilters();
  }

  function setStatus(status: string): void {
    statusFilter = normalizeStatusFilter(activeTab, status);
    applyFilters();
  }

  function updateStatusFilterValue(value: string): void {
    statusFilter = normalizeStatusFilter(activeTab, value || 'all');
    applyFilters();
  }

  function updateAgentFilterValue(value: string): void {
    agentFilter = value;
    applyFilters();
  }

  function updateTriggerFilterValue(value: string): void {
    triggerFilter = normalizeTriggerFilter(value);
    applyFilters();
  }

  function updateKeywordFilterValue(value: string): void {
    keyword = value;
    applyFilters();
  }

  function clearFilters(): void {
    activeTab = 'all';
    statusFilter = 'all';
    agentFilter = '';
    triggerFilter = '';
    taskFilter = '';
    keyword = '';
    applyFilters();
  }

  type RunFilterCriteria = {
    tab: typeof activeTab;
    status: string;
    agent: string;
    trigger: string;
    task: string;
    keyword: string;
  };

  function currentFilterCriteria(): RunFilterCriteria {
    return {
      tab: activeTab,
      status: statusFilter,
      agent: agentFilter,
      trigger: triggerFilter,
      task: taskFilter,
      keyword,
    };
  }

  function filterRuns(source: ProductRun[], criteria: RunFilterCriteria): ProductRun[] {
    return source.filter((run) => matchesRunFilters(run, criteria));
  }

  function matchesRunFilters(run: ProductRun, criteria = currentFilterCriteria()): boolean {
    const tabMatch = criteria.tab === 'all' || run.type === criteria.tab;
    const statusMatch = matchesRunStatusFilter(run, criteria.status);
    const agentMatch = !criteria.agent || runAgentKey(run) === criteria.agent;
    const triggerMatch = !criteria.trigger || runTriggerKey(run) === criteria.trigger;
    const taskMatch = !criteria.task || run.automationId === criteria.task;
    const keywordMatch = !criteria.keyword.trim() || `${run.id} ${run.title} ${run.agent} ${run.automation} ${run.workspace}`.toLowerCase().includes(criteria.keyword.trim().toLowerCase());
    return tabMatch && statusMatch && agentMatch && triggerMatch && taskMatch && keywordMatch;
  }

  function matchesRunStatusFilter(run: ProductRun, status = statusFilter): boolean {
    if (status === 'all') return true;
    const key = runStatusKey(run);
    if (status === 'attention') {
      return key === 'failed' || key === 'skipped' || key === 'cancelled';
    }
    return key === status;
  }

  function runStatusKey(run: ProductRun): string {
    const raw = run.rawStatus.toUpperCase();
    if (run.type === 'work_session') {
      if (raw === 'STARTING') return 'starting';
      if (raw === 'RUNNING') return 'running';
      if (raw === 'STOPPED') return 'stopped';
      if (raw === 'FAILED' || raw === 'START_FAILED') return 'failed';
    }
    if (run.type === 'automation_run') {
      if (raw === 'PENDING') return 'pending';
      if (raw === 'RUNNING') return 'running';
      if (raw === 'SUCCEEDED' || raw === 'SUCCESS') return 'succeeded';
      if (raw === 'FAILED' || raw === 'FAILURE') return 'failed';
      if (raw === 'SKIPPED') return 'skipped';
      if (raw === 'CANCELED' || raw === 'CANCELLED') return 'cancelled';
    }
    return run.status;
  }

  function runTriggerKey(run: ProductRun): string {
    if (run.trigger === '手动触发') return 'manual';
    if (run.trigger === '定时触发') return 'cron';
    if (run.trigger === '周期触发') return 'interval';
    if (run.trigger === '事件触发') return 'event';
    if (run.trigger === '延迟触发') return 'timeout';
    return run.trigger || '';
  }

  function runAgentKey(run: ProductRun): string {
    return run.agentId || '';
  }

  function buildAgentOptions(definitions: AgentDefinition[], sourceRuns: ProductRun[]): Array<{ value: string; label: string }> {
    const agentById = new Map(definitions.map((agent) => [agent.id, agent]));
    const options = new Map<string, string>();
    for (const run of sourceRuns) {
      const agentId = runAgentKey(run);
      if (!agentId) continue;
      const agent = agentById.get(agentId);
      options.set(agentId, agent?.name || run.agent || agentId);
    }
    return Array.from(options, ([value, label]) => ({ value, label }))
      .sort((left, right) => left.label.localeCompare(right.label, 'zh-Hans-CN'));
  }

  function normalizeTriggerFilter(value: string): string {
    if (value === '手动触发') return 'manual';
    if (value === '定时触发') return 'cron';
    if (value === '周期触发') return 'interval';
    if (value === '事件触发') return 'event';
    if (value === '延迟触发') return 'timeout';
    return triggerOptions().some((option) => option.value === value) ? value : '';
  }

  function normalizeStatusFilter(tab: typeof activeTab, status: string): string {
    const value = legacyStatusFilterValue(tab, status || 'all');
    if (value === 'all') return 'all';
    if (tab === 'all') {
      return ['running', 'attention', 'cancelled'].includes(value) ? value : 'all';
    }
    if (value === 'attention') return 'attention';
    if (value === 'cancelled' && tab !== 'automation_run') return 'all';
    return statusOptionsForTab(tab).some((option) => option.value === value) ? value : 'all';
  }

  function legacyStatusFilterValue(tab: typeof activeTab, status: string): string {
    if (status === '运行中') return 'running';
    if (status === '已取消') return 'cancelled';
    if (status === '失败 / 异常') return 'attention';
    if (tab === 'work_session') {
      if (status === '启动中') return 'starting';
      if (status === '已停止') return 'stopped';
      if (status === '启动失败') return 'failed';
    }
    if (tab === 'automation_run') {
      if (status === '等待中') return 'pending';
      if (status === '成功') return 'succeeded';
      if (status === '失败') return 'failed';
      if (status === '跳过') return 'skipped';
    }
    if (status === '失败' || status === '启动失败' || status === '跳过') return 'attention';
    return status;
  }

  function uniqueOptions(values: string[]): string[] {
    return Array.from(new Set(values));
  }

  function typeOptions(): Array<{ value: 'all' | 'work_session' | 'automation_run'; label: string }> {
    return [
      { value: 'all', label: '全部类型' },
      { value: 'work_session', label: '工作会话' },
      { value: 'automation_run', label: '自动化运行' },
    ];
  }

  function triggerOptions(): Array<{ value: string; label: string }> {
    return [
      { value: 'manual', label: '手动触发' },
      { value: 'cron', label: '定时触发' },
      { value: 'interval', label: '周期触发' },
      { value: 'event', label: '事件触发' },
      { value: 'timeout', label: '延迟触发' },
    ];
  }

  function statusOptions(): Array<{ value: string; label: string }> {
    return statusOptionsForTab(activeTab);
  }

  function statusOptionsForTab(tab: typeof activeTab): Array<{ value: string; label: string }> {
    if (tab === 'work_session') {
      return [
        { value: 'all', label: '全部' },
        { value: 'attention', label: '异常' },
        { value: 'starting', label: '启动中' },
        { value: 'running', label: '运行中' },
        { value: 'stopped', label: '已停止' },
        { value: 'failed', label: '启动失败' },
      ];
    }
    if (tab === 'automation_run') {
      return [
        { value: 'all', label: '全部' },
        { value: 'attention', label: '异常' },
        { value: 'pending', label: '等待中' },
        { value: 'running', label: '运行中' },
        { value: 'succeeded', label: '成功' },
        { value: 'failed', label: '失败' },
        { value: 'skipped', label: '跳过' },
        { value: 'cancelled', label: '已取消' },
      ];
    }
    return [
      { value: 'all', label: '全部' },
      { value: 'running', label: '运行中' },
      { value: 'attention', label: '失败 / 异常' },
      { value: 'cancelled', label: '已取消' },
    ];
  }

  function selectRun(run: ProductRun): void {
    if (selectedRunId === run.id) {
      return;
    }
    selectedRunId = run.id;
    if (run.type === 'work_session' && activeDetailTab === 'input') {
      activeDetailTab = 'result';
    }
    updateURL();
    void loadRunDetail(run);
  }

  function syncSelectedRunWithFilters(): void {
    const current = runs.find((run) => run.id === selectedRunId && matchesRunFilters(run));
    const nextRun = current || runs.find((run) => matchesRunFilters(run)) || null;
    if (selectedRunId === (nextRun?.id || '')) {
      return;
    }
    selectedRunId = nextRun?.id || '';
    if (nextRun) {
      void loadRunDetail(nextRun);
    }
  }

  function setDetailTab(tab: typeof activeDetailTab): void {
    if (activeDetailTab === tab) {
      return;
    }
    activeDetailTab = tab;
    updateURL();
    if (tab === 'result' && selectedRun?.type === 'work_session') {
      void scrollMessagesToBottom();
      scheduleMessageBottomCorrection();
    }
  }

  async function copyText(value: string, event?: MouseEvent): Promise<void> {
    event?.stopPropagation();
    if (!value) return;
    if (copiedTimer) {
      clearTimeout(copiedTimer);
    }
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
      } else {
        fallbackCopyText(value);
      }
      copiedId = value;
      copyNotice = { text: '已复制', ok: true };
      copiedTimer = setTimeout(() => {
        copiedId = '';
        copyNotice = null;
      }, 1400);
    } catch (err) {
      copiedId = value;
      copyNotice = { text: err instanceof Error ? err.message : '复制失败', ok: false };
      copiedTimer = setTimeout(() => {
        copiedId = '';
        copyNotice = null;
      }, 1800);
    }
  }

  function fallbackCopyText(value: string): void {
    const textarea = document.createElement('textarea');
    textarea.value = value;
    textarea.setAttribute('readonly', 'true');
    textarea.style.position = 'fixed';
    textarea.style.left = '-9999px';
    textarea.style.top = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const copied = document.execCommand('copy');
    document.body.removeChild(textarea);
    if (!copied) {
      throw new Error('复制失败');
    }
  }

  function statusClass(status: string): string {
    if (['启动失败', '失败', '跳过', '已取消'].includes(status)) return 'red';
    if (['成功', '已停止'].includes(status)) return 'green';
    if (['启动中', '等待中', '运行中', '恢复中', '停止中'].includes(status)) return 'blue';
    return 'gray';
  }

  function hasRunningAgentMessage(run: ProductRun): boolean {
    return run.status === '运行中' && run.messages.some((message) => message.role === 'agent' && message.running);
  }

  function isReplyPending(run: ProductRun): boolean {
    return hasRunningAgentMessage(run);
  }

  function canSendMessage(run: ProductRun): boolean {
    return run.type === 'work_session' && run.status === '运行中' && !isReplyPending(run);
  }

  function canOpenDebug(run: ProductRun): boolean {
    return run.type === 'work_session' && run.status === '运行中';
  }

  function messageInputHint(run: ProductRun): string {
    if (isReplyPending(run)) return '正在回复';
    if (run.status === '恢复中' || run.status === '启动中' || run.status === '等待中') return '会话启动中';
    if (run.status === '停止中') return '会话停止中';
    if (run.status === '已停止') return '会话已停止';
    if (run.status === '启动失败') return '会话启动失败';
    if (run.status !== '运行中') return '会话未运行';
    return 'Shift + Enter 换行';
  }

  function runMessages(run: ProductRun): ProductRun['messages'] {
    return run.messages;
  }

  const AGENT_RESULT_PREFIX = '__AGENT_RESULT__';

  function stripAgentResultPayload(text: string): string {
    const result = text || '';
    const index = result.lastIndexOf(AGENT_RESULT_PREFIX);
    return index >= 0 ? result.slice(0, index) : result;
  }

  function agentMessageContent(message: ProductRun['messages'][number], content: string): string {
    return message.role === 'agent' ? stripAgentResultPayload(content) : content;
  }

  function mergeMessageContent(existing: ProductRun['messages'][number], next: ProductRun['messages'][number]): string {
    return agentMessageContent(next, mergeCellContent(agentMessageContent(next, existing.content), agentMessageContent(next, next.content)));
  }

  function cellMessage(cell: Awaited<ReturnType<typeof listWorkSessionCells>>[number]): ProductRun['messages'][number] {
    const role = cell.agent ? 'agent' : 'user';
    return {
      id: cell.id,
      renderKey: cell.id,
      role,
      type: cell.type,
      agent: cell.agent,
      source: cell.source,
      content: role === 'agent' ? stripAgentResultPayload(cell.output || cell.stopReason || '') : cell.output || cell.stopReason || '',
      at: cell.createdAt || cell.id,
      running: cell.running,
      success: cell.success,
      exitCode: cell.exitCode,
      stopReason: cell.stopReason,
      agentSessionId: cell.agentSessionId,
      failed: Boolean(!cell.running && !cell.success),
    };
  }

  function latestCellAgent(cells: Awaited<ReturnType<typeof listWorkSessionCells>>): string {
    for (let index = cells.length - 1; index >= 0; index -= 1) {
      const agent = cells[index].agent?.trim();
      if (agent) return agent;
    }
    return '';
  }

  function tagValue(tags: Array<{ name: string; value: string }>, name: string): string {
    return tags.find((tag) => tag.name === name)?.value || '';
  }

  function upsertMessage(messages: ProductRun['messages'], next: ProductRun['messages'][number]): ProductRun['messages'] {
    if (!next.id) {
      return [...messages, next];
    }
    const index = messages.findIndex((message) => message.id === next.id);
    if (index < 0) {
      const pendingIndex = messages.findIndex((message) => message.role === next.role && message.running && !message.id);
      if (pendingIndex >= 0) {
        const updated = [...messages];
        const existing = updated[pendingIndex];
        updated[pendingIndex] = {
          ...next,
          renderKey: existing.renderKey || next.renderKey || next.id,
          content: mergeMessageContent(existing, next),
        };
        return updated;
      }
      return [...messages, next];
    }
    const updated = [...messages];
    const existing = updated[index];
    updated[index] = {
      ...next,
      renderKey: existing.renderKey || next.renderKey || next.id,
      content: mergeMessageContent(existing, next),
    };
    return updated;
  }

  function mergeCellContent(existing: string, incoming: string): string {
    if (!existing || existing === '-') return incoming;
    if (!incoming || incoming === '-') return existing;
    if (existing === incoming) return incoming;
    if (existing.startsWith(incoming)) return existing;
    return incoming;
  }

  function appendPendingChunk(cellId: string, chunk: string): void {
    if (!cellId) return;
    pendingCellChunks.set(cellId, `${pendingCellChunks.get(cellId) || ''}${chunk}`);
  }

  function appendAgentChunk(runId: string, cellId: string, chunk: string): boolean {
    let applied = false;
    runs = runs.map((item) => {
      if (item.id !== runId) return item;
      const messages = [...item.messages];
      let index = cellId ? messages.findIndex((message) => message.id === cellId) : -1;
      if (index < 0) {
        index = messages.findIndex((message) => message.role === 'agent' && message.running);
      }
      if (index >= 0 && messages[index].role === 'agent') {
        applied = true;
        messages[index] = { ...messages[index], content: stripAgentResultPayload(`${messages[index].content || ''}${chunk}`) };
      }
      return { ...item, messages };
    });
    return applied;
  }

  async function scrollMessagesToBottom(): Promise<void> {
    await tick();
    if (messageScrollFrame) return;
    messageScrollFrame = requestAnimationFrame(() => {
      messageScrollFrame = 0;
      if (messageScroll) {
        messageScroll.scrollTop = messageScroll.scrollHeight;
      }
    });
  }

  function isMessageScrollNearBottom(): boolean {
    if (!messageScroll) return true;
    return messageScroll.scrollHeight - messageScroll.scrollTop - messageScroll.clientHeight < 96;
  }

  function scheduleMessageBottomCorrection(delayMs = 80): void {
    const shouldCorrect = isMessageScrollNearBottom();
    window.setTimeout(() => {
      if (messageScroll && shouldCorrect) {
        messageScroll.scrollTop = messageScroll.scrollHeight;
      }
    }, delayMs);
  }

  function ensureTerminalObserver(): void {
    if (terminalObserver || !messageScroll) return;
    terminalObserverRoot = messageScroll;
    terminalObserver = new IntersectionObserver(
      (entries) => {
        let changed = false;
        for (const entry of entries) {
          const id = terminalNodeIds.get(entry.target);
          if (!id) continue;
          if (entry.isIntersecting) {
            if (!terminalVisibleIds.has(id)) {
              terminalVisibleIds.add(id);
              changed = true;
            }
          }
        }
        if (changed) {
          terminalVisibleIds = new Set(terminalVisibleIds);
        }
      },
      { root: messageScroll, rootMargin: '400px 0px' },
    );
    for (const node of terminalNodeIds.keys()) {
      terminalObserver.observe(node);
    }
  }

  function syncTerminalObserverRoot(root: HTMLDivElement | null): void {
    if (root === terminalObserverRoot) return;
    terminalObserver?.disconnect();
    terminalObserver = null;
    terminalObserverRoot = root;
    terminalVisibleIds = new Set();
    if (root) {
      ensureTerminalObserver();
    }
  }

  function trackTerminalVisibility(node: HTMLElement, id: string) {
    terminalNodeIds.set(node, id);
    terminalObserver?.observe(node);
    return {
      update(nextId: string) {
        if (id !== nextId && terminalVisibleIds.has(id)) {
          const nextVisibleIds = new Set(terminalVisibleIds);
          nextVisibleIds.delete(id);
          if (nextId) {
            nextVisibleIds.add(nextId);
          }
          terminalVisibleIds = nextVisibleIds;
        }
        id = nextId;
        terminalNodeIds.set(node, nextId);
      },
      destroy() {
        terminalObserver?.unobserve(node);
        terminalNodeIds.delete(node);
        if (id) {
          const nextVisibleIds = new Set(terminalVisibleIds);
          nextVisibleIds.delete(id);
          terminalVisibleIds = nextVisibleIds;
        }
      },
    };
  }

  function estimateTerminalHeight(message: ProductRun['messages'][number]): number {
    const cached = message.id ? terminalHeights.get(message.id) : undefined;
    if (cached) return cached;
    const text = messageTerminalText(message);
    const lines = text ? text.split('\n').length : 1;
    return Math.min(Math.max(lines, 1) * 18 + 16, 4000);
  }

  function applyPendingChunks(message: ProductRun['messages'][number]): ProductRun['messages'][number] {
    if (!message.id) return message;
    const pending = pendingCellChunks.get(message.id);
    if (!pending) return message;
    pendingCellChunks.delete(message.id);
    return {
      ...message,
      content: agentMessageContent(message, `${message.content || ''}${pending}`),
    };
  }

  function terminalRenderer(node: HTMLElement, params: TerminalActionParams) {
    const term = new Terminal({
      convertEol: true,
      disableStdin: true,
      cursorBlink: false,
      cols: 100,
      rows: 1,
      fontFamily: 'IBM Plex Mono, Fira Code, monospace',
      fontSize: 13,
      lineHeight: 1.25,
      scrollback: 0,
      theme: { ...terminalTheme, cursor: params.running ? terminalTheme.cursor : 'transparent' },
    });
    const fitAddon = new FitAddon();
    let currentText = '';
    let currentHeight = 0;
    const fallbackLineHeight = Math.ceil(13 * 1.25);
    const observer = new ResizeObserver(() => scheduleTerminalHeightSync());
    let heightFrame = 0;
    let disposed = false;

    term.loadAddon(fitAddon);
    term.open(node);
    observer.observe(node);

    function setCursorActive(active: boolean): void {
      node.classList.toggle('terminal-cursor-active', active);
    }

    const visibleRows = (text: string) => {
      const cols = Math.max(term.cols || 1, 1);
      const lines = text.length > 0 ? text.split(/\r?\n/) : [''];
      let rows = 0;
      for (const rawLine of lines) {
        const plainLine = rawLine.replace(/\u001b\[[0-9;?]*[A-Za-z]/g, '');
        rows += Math.max(1, Math.ceil(Math.max(plainLine.length, 1) / cols));
      }
      return Math.max(rows, 1);
    };

    function fitTerminalRows(text: string): number {
      fitAddon.fit();
      const cols = Math.max(term.cols || 1, 1);
      const rows = visibleRows(text);
      if (term.rows !== rows) {
        term.resize(cols, rows);
      }
      return rows;
    }

    function scheduleTerminalHeightSync(): void {
      if (disposed || heightFrame) return;
      heightFrame = requestAnimationFrame(() => {
        heightFrame = 0;
        if (!disposed) {
          syncTerminalHeight();
        }
      });
    }

    function syncTerminalHeight() {
      if (disposed || !node.isConnected) return;
      const rows = fitTerminalRows(currentText);
      if (disposed || !node.isConnected) return;
      const measuredLineHeight = Math.ceil(
        Number((term as any)?._core?._renderService?.dimensions?.css?.cell?.height) || fallbackLineHeight,
      );
      const nextHeight = rows * measuredLineHeight + 16;
      if (nextHeight > currentHeight || !params.running) {
        currentHeight = nextHeight;
        node.style.height = `${nextHeight}px`;
        if (!params.running && params.id) {
          terminalHeights.set(params.id, nextHeight);
        }
        scheduleMessageBottomCorrection(0);
      }
      term.scrollToBottom();
    }

    function cancelXtermViewportRefresh(): void {
      const core = (term as any)?._core;
      const viewport = core?.viewport;
      const frame = viewport?._refreshAnimationFrame;
      const win = core?._coreBrowserService?.window || window;
      if (typeof frame === 'number') {
        win.cancelAnimationFrame(frame);
        viewport._refreshAnimationFrame = null;
      }
    }

    function applyText(text: string) {
      if (text === currentText) {
        return;
      }
      fitTerminalRows(text);
      if (text.startsWith(currentText)) {
        term.write(text.slice(currentText.length));
      } else {
        term.reset();
        term.write(text);
        currentHeight = 0;
      }
      currentText = text;
      scheduleTerminalHeightSync();
    }

    setCursorActive(params.running);
    applyText(params.text);

    return {
      update(next: TerminalActionParams) {
        term.options.theme = { ...terminalTheme, cursor: next.running ? terminalTheme.cursor : 'transparent' };
        params = next;
        setCursorActive(next.running);
        applyText(next.text);
      },
      destroy() {
        disposed = true;
        observer.disconnect();
        if (heightFrame) {
          cancelAnimationFrame(heightFrame);
          heightFrame = 0;
        }
        cancelXtermViewportRefresh();
        term.dispose();
      },
    };
  }

  function formatRole(role: string): string {
    if (role === 'user') return 'user';
    if (role === 'agent') return 'agent';
    return 'system';
  }

  function messageStatus(message: ProductRun['messages'][number]): string {
    if (message.running) {
      return '运行中';
    }
    const exitCode = message.exitCode ?? (message.success === false ? 1 : 0);
    return exitCode === 0 ? '完成' : `退出码 ${exitCode}`;
  }

  function messageStatusTone(message: ProductRun['messages'][number]): string {
    if (message.running) return 'running';
    if (message.success === false || message.failed) return 'failed';
    return 'succeeded';
  }

  function messageTerminalText(message: ProductRun['messages'][number]): string {
    return agentMessageContent(message, message.content || (message.running ? '' : '-'));
  }

  function eventLevelTone(level: string): string {
    const normalized = level.toLowerCase();
    if (normalized === 'error' || normalized === 'failed' || normalized === 'failure') return 'error';
    if (normalized === 'warn' || normalized === 'warning') return 'warning';
    if (normalized === 'debug' || normalized === 'trace') return 'debug';
    if (normalized === 'success' || normalized === 'succeeded') return 'success';
    return 'info';
  }

  function formatTime(value: string): string {
    return formatBeijingTime(value);
  }

  function runSortTimestamp(run: ProductRun): number {
    const updatedAt = Date.parse(run.completedAt || '');
    if (!Number.isNaN(updatedAt)) return updatedAt;
    const createdAt = Date.parse(run.startedAt || '');
    return Number.isNaN(createdAt) ? 0 : createdAt;
  }

  function sortRunsByUpdatedTime(source: ProductRun[]): ProductRun[] {
    return [...source].sort((left, right) => {
      const byUpdatedTime = runSortTimestamp(right) - runSortTimestamp(left);
      if (byUpdatedTime !== 0) return byUpdatedTime;
      return right.id.localeCompare(left.id);
    });
  }

  function runDuration(run: ProductRun): string {
    const startedAt = new Date(run.startedAt).getTime();
    if (!run.startedAt || Number.isNaN(startedAt)) {
      return run.duration || '-';
    }
    const completedAt = new Date(run.completedAt).getTime();
    const endedAt = run.status === '运行中' || !run.completedAt || Number.isNaN(completedAt) ? Date.now() : completedAt;
    const seconds = Math.max(0, Math.round((endedAt - startedAt) / 1000));
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
    return `${(seconds / 3600).toFixed(1)}h`;
  }

  async function load(): Promise<void> {
    loading = true;
    error = '';
    try {
      const [sessions, tasks, agents] = await Promise.all([listWorkSessions(50), listAutomationTasks(), listAgentDefinitions()]);
      const automationRuns = await listRecentAutomationRuns(tasks.map((item) => item.id), 20);
      const taskById = new Map(tasks.map((task) => [task.id, task]));
      const agentById = new Map(agents.map((agent) => [agent.id, agent]));
      agentDefinitions = agents;
      const sessionRuns = sessions.map((session) => {
        const productRun = sessionToRun(session);
        const agentID = tagValue(session.tags, 'agent_id');
        const agent = agentID ? agentById.get(agentID) : null;
        return {
          ...productRun,
          agentId: agentID || productRun.agentId,
          agent: agent?.name || productRun.agent || agentID,
          agentProvider: agent?.provider || productRun.agentProvider,
        };
      });
      runs = sortRunsByUpdatedTime([
        ...sessionRuns,
        ...automationRuns.map((run) => {
          const productRun = automationRunToRun(run);
          const task = taskById.get(run.loaderId);
          const agent = task?.agentId ? agentById.get(task.agentId) : null;
          const agentName = agent?.name || task?.agentId || productRun.agent;
          return task
            ? {
              ...productRun,
              title: task.name || productRun.title,
              automation: task.name || productRun.automation,
              agent: agentName,
              agentId: task.agentId || productRun.agentId,
              agentProvider: agent?.provider || task.defaultAgent || productRun.agentProvider,
              workspace: task.workspaceId || productRun.workspace,
            }
            : productRun;
        }),
      ]);
      const current = runs.find((run) => run.id === selectedRunId && matchesRunFilters(run)) || runs.find((run) => matchesRunFilters(run)) || null;
      if (current) {
        const shouldSyncSelectedRun = selectedRunId !== current.id;
        selectedRunId = current.id;
        if (shouldSyncSelectedRun) {
          updateURL(true);
        }
        await loadRunDetail(current);
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function loadRunDetail(run: ProductRun): Promise<void> {
    startWatching(run);
    detailLoading = true;
    error = '';
    try {
      if (run.type === 'work_session') {
        const [cells, events] = await Promise.all([
          listWorkSessionCells(run.id).catch(() => []),
          listWorkSessionEvents(run.id).catch(() => []),
        ]);
        runs = runs.map((item) => item.id === run.id
          ? {
            ...item,
            messages: cells.map(cellMessage),
            agentProvider: latestCellAgent(cells) || item.agentProvider,
            events: events.map((event) => ({
              type: event.type,
              level: event.level,
              message: event.message,
              createdAt: event.createdAt,
            })),
          }
          : item);
      } else if (run.automationId) {
        const [detail, events] = await Promise.all([
          getAutomationRun(run.automationId, run.id).catch(() => null),
          listAutomationEvents(run.automationId, 50).catch(() => []),
        ]);
        runs = runs.map((item) => item.id === run.id
          ? {
            ...item,
            ...(detail ? automationRunToRun(detail) : {}),
            title: item.title,
            automation: item.automation,
            agent: item.agent,
            agentId: item.agentId,
            workspace: item.workspace,
            events: events
              .filter((event) => !event.runId || event.runId === run.id)
              .map((event) => ({
                type: event.type,
                level: event.level,
                message: event.message,
                createdAt: event.createdAt,
              })),
          }
          : item);
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      detailLoading = false;
      if (run.type === 'work_session') {
        void scrollMessagesToBottom();
        scheduleMessageBottomCorrection();
      }
    }
  }

  function startWatching(run: ProductRun): void {
    stopWatching();
    if (document.visibilityState === 'hidden') return;
    if (run.type !== 'work_session') return;
    const controller = new AbortController();
    watchAbort = controller;
    void watchRunLoop(run.id, controller);
  }

  function resumeVisibleWatch(): void {
    if (watchAbort || document.visibilityState === 'hidden') return;
    const run = visibleRuns.find((item) => item.id === selectedRunId) || selectedRun;
    if (run?.type === 'work_session') {
      startWatching(run);
    }
  }

  async function watchRunLoop(runId: string, controller: AbortController): Promise<void> {
    let retryDelay = 1000;
    while (!controller.signal.aborted) {
      try {
        await watchWorkSession(runId, (event) => {
          if (event.type === 'session') {
            retryDelay = 1000;
            runs = sortRunsByUpdatedTime(runs.map((item) => item.id === runId ? { ...item, ...sessionToRun(event.session), messages: item.messages, events: item.events } : item));
          } else if (event.type === 'event') {
            runs = runs.map((item) => item.id === runId
              ? { ...item, events: [...item.events, { type: event.event.type, level: event.event.level, message: event.event.message, createdAt: event.event.createdAt }] }
              : item);
          } else if (event.type === 'cell') {
            runs = runs.map((item) => item.id === runId
              ? {
                ...item,
                messages: upsertMessage(item.messages, applyPendingChunks(cellMessage(event.cell))),
              }
              : item);
            if (event.cell.agent && !event.cell.running) {
              sendingMessage = false;
            }
            void scrollMessagesToBottom();
          } else if (event.type === 'chunk') {
            const applied = appendAgentChunk(runId, event.cellId, event.chunk);
            if (!applied) {
              appendPendingChunk(event.cellId, event.chunk);
            }
            void scrollMessagesToBottom();
          }
        }, controller.signal);
      } catch (err) {
        if (!controller.signal.aborted) {
          error = err instanceof Error ? err.message : String(err);
        }
      }
      if (!controller.signal.aborted) {
        await delay(retryDelay, controller.signal);
        retryDelay = Math.min(retryDelay * 2, 30000);
      }
    }
  }

  function stopWatching(): void {
    if (watchAbort) {
      watchAbort.abort();
      watchAbort = null;
    }
  }

  function stopSendingMessage(): void {
    if (messageAbort) {
      messageAbort.abort();
      messageAbort = null;
    }
  }

  function delay(ms: number, signal: AbortSignal): Promise<void> {
    return new Promise((resolve) => {
      const timer = window.setTimeout(resolve, ms);
      signal.addEventListener(
        'abort',
        () => {
          window.clearTimeout(timer);
          resolve();
        },
        { once: true },
      );
    });
  }

  async function stopSelectedRun(run: ProductRun): Promise<void> {
    if (sessionAction) return;
    sessionAction = { runId: run.id, type: 'stop' };
    const previousStatus = run.status;
    error = '';
    runs = runs.map((item) => item.id === run.id ? { ...item, status: '停止中' } : item);
    try {
      const updated = await stopWorkSession(run.id);
      runs = sortRunsByUpdatedTime(runs.map((item) => item.id === run.id ? { ...item, ...sessionToRun(updated), agentProvider: item.agentProvider, messages: item.messages, events: item.events } : item));
    } catch (err) {
      runs = runs.map((item) => item.id === run.id ? { ...item, status: previousStatus } : item);
      error = err instanceof Error ? err.message : String(err);
    } finally {
      sessionAction = null;
    }
  }

  async function resumeSelectedRun(run: ProductRun): Promise<void> {
    if (sessionAction) return;
    sessionAction = { runId: run.id, type: 'resume' };
    const previousStatus = run.status;
    error = '';
    runs = runs.map((item) => item.id === run.id ? { ...item, status: '恢复中' } : item);
    try {
      const updated = await resumeWorkSession(run.id);
      runs = sortRunsByUpdatedTime(runs.map((item) => item.id === run.id ? { ...item, ...sessionToRun(updated), agentProvider: item.agentProvider, messages: item.messages, events: item.events } : item));
    } catch (err) {
      runs = runs.map((item) => item.id === run.id ? { ...item, status: previousStatus } : item);
      error = err instanceof Error ? err.message : String(err);
    } finally {
      sessionAction = null;
    }
  }

  async function rerunAutomationRun(run: ProductRun): Promise<void> {
    if (automationActionRunId || !run.automationId) return;
    automationActionRunId = run.id;
    error = '';
    try {
      const payload = run.input?.trim() || '{}';
      JSON.parse(payload);
      const nextRun = await runAutomationTaskNow(run.automationId, payload);
      selectedRunId = nextRun.id;
      activeTab = 'automation_run';
      activeDetailTab = 'result';
      updateURL();
      await load();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      automationActionRunId = '';
    }
  }

  async function sendMessage(run: ProductRun): Promise<void> {
    if (!messageText.trim() || !canSendMessage(run)) return;
    stopSendingMessage();
    const controller = new AbortController();
    messageAbort = controller;
    sendingMessage = true;
    error = '';
    try {
      const sentText = messageText.trim();
      const pendingMessageId = `pending-${Date.now()}`;
      const pendingRenderKey = pendingMessageId;
      messageText = '';
      runs = runs.map((item) => item.id === run.id
        ? { ...item, messages: [...item.messages, { id: pendingMessageId, renderKey: pendingRenderKey, role: 'agent', content: '', at: new Date().toISOString(), running: true, agent: run.agentProvider || 'codex', type: CellType.AGENT }] }
        : item);
      await scrollMessagesToBottom();
      await sendWorkSessionMessageStream(run.id, run.agentProvider || 'codex', sentText, (event) => {
        if (event.type === 'started' && event.runId) {
          runs = runs.map((item) => {
            if (item.id !== run.id) return item;
            const pending = item.messages.find((message) => message.id === pendingMessageId);
            if (!pending) return item;
            if (item.messages.some((message) => message.id === event.runId)) {
              const messages = item.messages
                .filter((message) => message.id !== pendingMessageId)
                .map((message) => message.id === event.runId
                  ? { ...message, renderKey: pending.renderKey || message.renderKey, content: mergeMessageContent(message, pending) }
                  : message);
              return { ...item, messages };
            }
            return {
              ...item,
              messages: item.messages.map((message) => message.id === pendingMessageId ? { ...message, id: event.runId, renderKey: message.renderKey || pendingRenderKey } : message),
            };
          });
        } else if (event.type === 'chunk' && event.chunk) {
          const applied = appendAgentChunk(run.id, event.runId, event.chunk);
          if (applied) {
            void scrollMessagesToBottom();
          }
        } else if (event.type === 'completed' && event.run) {
          runs = runs.map((item) => item.id === run.id
            ? {
              ...item,
              messages: upsertMessage(item.messages, {
                id: event.run?.id || event.runId,
                renderKey: pendingRenderKey,
                role: 'agent',
                agent: event.run?.agent || run.agentProvider || 'codex',
                content: event.run?.output || event.run?.stopReason || '',
                at: event.run?.createdAt || new Date().toISOString(),
                running: event.run?.running,
                success: event.run?.success,
                exitCode: event.run?.exitCode,
                stopReason: event.run?.stopReason,
                agentSessionId: event.run?.agentSessionId,
                failed: Boolean(!event.run?.running && !event.run?.success),
                type: CellType.AGENT,
              }),
            }
            : item);
        }
      }, controller.signal);
      await scrollMessagesToBottom();
    } catch (err) {
      if (!controller.signal.aborted) {
        error = err instanceof Error ? err.message : String(err);
        await loadRunDetail(run);
      }
    } finally {
      if (messageAbort === controller) {
        messageAbort = null;
        sendingMessage = false;
      }
    }
  }

  function handleMessageKeydown(event: KeyboardEvent, run: ProductRun): void {
    if (event.key !== 'Enter' || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) {
      return;
    }
    event.preventDefault();
    if (!messageText.trim() || !canSendMessage(run)) {
      return;
    }
    void sendMessage(run);
  }
</script>

{#if error}
  <div class="alert danger">{error}</div>
{/if}

<section class="panel runs-panel">
  <div class="runs-toolbar">
    <div class="run-command-metrics compact">
      <button class:active={activeTab === 'all'} on:click={() => setTab('all')}><span>全部</span><b>{runs.length}</b></button>
      <button class:active={activeTab === 'work_session'} on:click={() => setTab('work_session')}><span>会话</span><b>{runs.filter((item) => item.type === 'work_session').length}</b></button>
      <button class:active={activeTab === 'automation_run'} on:click={() => setTab('automation_run')}><span>自动化</span><b>{runs.filter((item) => item.type === 'automation_run').length}</b></button>
      <button class:active={statusFilter === 'attention'} on:click={() => setStatus('attention')}><span>异常</span><b>{runs.filter((item) => ['启动失败', '失败', '跳过', '已取消'].includes(item.status)).length}</b></button>
    </div>
    <div class="runs-filters">
      <select value={activeTab} on:change={(event) => updateTypeFilterValue(event.currentTarget.value)} aria-label="运行类型">{#each typeOptions() as option}<option value={option.value}>{option.label}</option>{/each}</select>
      <select value={statusFilter} on:change={(event) => updateStatusFilterValue(event.currentTarget.value)} aria-label="运行状态">{#each statusOptions() as option}<option value={option.value}>{option.label}</option>{/each}</select>
      <select value={agentFilter} on:change={(event) => updateAgentFilterValue(event.currentTarget.value)} aria-label="智能体"><option value="">智能体</option>{#each agentOptions as agent}<option value={agent.value}>{agent.label}</option>{/each}</select>
      <select value={triggerFilter} on:change={(event) => updateTriggerFilterValue(event.currentTarget.value)} aria-label="触发方式"><option value="">触发方式</option>{#each triggerOptions() as option}<option value={option.value}>{option.label}</option>{/each}</select>
      <input class="filter-keyword" bind:value={keyword} on:change={(event) => updateKeywordFilterValue(event.currentTarget.value)} placeholder="匹配名称、ID、智能体、任务、错误摘要" aria-label="关键字">
      <button on:click={load}>{loading ? '刷新中...' : '刷新'}</button>
    </div>
  </div>
  {#if loading && runs.length === 0}
    <div class="runs-master-detail loading-layout" aria-label="正在加载运行记录">
      <div class="run-list-card">
        <div class="run-list-head"><b>运行列表</b><span>加载中</span></div>
        <div class="run-list">
          {#each Array(5) as _}
            <div class="run-card skeleton-card">
              <span></span>
              <span></span>
              <span></span>
              <span></span>
            </div>
          {/each}
        </div>
      </div>
      <div class="run-detail skeleton-run-detail">
        <div class="run-detail-head skeleton-detail-head">
          <div>
            <span></span>
            <span></span>
          </div>
          <div class="toolbar">
            <span></span>
            <span></span>
          </div>
        </div>
        <div class="detail-tabs skeleton-tabs">
          {#each Array(5) as _}
            <button disabled aria-label="加载中"><span></span></button>
          {/each}
        </div>
        <div class="detail-body skeleton-detail-body">
          <div class="skeleton-content-block"></div>
        </div>
      </div>
      <aside class="run-side-panel">
        <section>
          <h3>运行信息</h3>
          <div class="side-facts skeleton-facts">
            {#each Array(6) as _}
              <div><span></span><b></b></div>
            {/each}
          </div>
        </section>
        <section>
          <h3>运行事实</h3>
          <div class="side-facts skeleton-facts">
            {#each Array(4) as _}
              <div><span></span><b></b></div>
            {/each}
          </div>
        </section>
      </aside>
    </div>
  {:else if visibleRuns.length === 0}
    <div class="empty-state">
      <div class="empty-state-icon">
        <AntIcon definition={runs.length === 0 ? InboxOutlined : FilterOutlined} />
      </div>
      {#if runs.length === 0}
        <h3>还没有运行记录</h3>
        <p>创建工作会话或运行自动化任务后，运行历史会出现在这里。</p>
      {:else}
        <h3>没有匹配的运行记录</h3>
        <p>当前共有 {runs.length} 条运行记录，但都不满足现在的筛选条件。试试调整或清除筛选。</p>
        <div class="empty-state-actions">
          <button on:click={clearFilters}>清除筛选</button>
        </div>
      {/if}
    </div>
  {:else}
    <div class="runs-master-detail">
      <div class="run-list-card">
        <div class="run-list-head">
          <b>运行列表</b>
          <span>{visibleRuns.length} 条</span>
        </div>
        <div class="run-list">
          {#each visibleRuns as run (run.id)}
            <div
              class="run-card"
              class:selected={selectedRun?.id === run.id}
              role="button"
              tabindex="0"
              on:click={() => selectRun(run)}
              on:keydown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault();
                  selectRun(run);
                }
              }}
            >
              <span class="run-card-head"><b>{run.title}</b><em class={statusClass(run.status)}>{run.status}</em></span>
              <span class="run-card-meta">{run.type === 'work_session' ? '工作会话' : '自动化运行'} · {run.agent || '-'} · {run.trigger}</span>
              <span class="run-card-time" title={`创建 ${formatTime(run.startedAt)} · 更新 ${formatTime(run.completedAt)}`}>创建 {formatTime(run.startedAt)}</span>
              <span class="run-card-time" title={`创建 ${formatTime(run.startedAt)} · 更新 ${formatTime(run.completedAt)}`}>更新 {formatTime(run.completedAt)} · {runDuration(run)} · 产出物 {run.artifacts.length}</span>
              {#if run.errorSummary}<span class="run-card-error">{run.errorSummary}</span>{/if}
            </div>
          {/each}
        </div>
      </div>
      <div class="run-detail">
        {#if selectedRun}
          <div class="run-detail-head">
            <div>
              <h2>{selectedRun.title}</h2>
              <p class="copy-line detail-id">
                <span title={selectedRun.id}>{selectedRun.id}</span>
                <span
                  class="icon-copy"
                  role="button"
                  tabindex="0"
                  title={copiedId === selectedRun.id ? '已复制' : '复制运行 ID'}
                  on:click={(event) => copyText(selectedRun.id, event)}
                  on:keydown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault();
                      void copyText(selectedRun.id);
                    }
                  }}
                >
                  <AntIcon definition={CopyOutlined} />
                </span>
                {#if copiedId === selectedRun.id && copyNotice}
                  <span class="copy-tip" class:bad={!copyNotice.ok}>{copyNotice.text}</span>
                {/if}
              </p>
            </div>
            <div class="toolbar">
              <span class="home-pill" class:blue={statusClass(selectedRun.status) === 'blue'} class:green={statusClass(selectedRun.status) === 'green'} class:red={statusClass(selectedRun.status) === 'red'} class:gray={statusClass(selectedRun.status) === 'gray'}>{selectedRun.status}</span>
              {#if selectedRun.type === 'work_session'}
                <button
                  class:waiting={sessionAction?.runId === selectedRun.id && sessionAction.type === 'stop'}
                  disabled={!['运行中', '停止中'].includes(selectedRun.status) || Boolean(sessionAction)}
                  on:click={() => stopSelectedRun(selectedRun)}
                >
                  {sessionAction?.runId === selectedRun.id && sessionAction.type === 'stop' ? '停止中...' : '停止'}
                </button>
                <button
                  class:waiting={sessionAction?.runId === selectedRun.id && sessionAction.type === 'resume'}
                  disabled={!['已停止', '启动失败', '恢复中'].includes(selectedRun.status) || Boolean(sessionAction)}
                  on:click={() => resumeSelectedRun(selectedRun)}
                >
                  {#if sessionAction?.runId === selectedRun.id && sessionAction.type === 'resume'}
                    {selectedRun.status === '启动失败' ? '重新启动...' : '恢复中...'}
                  {:else}
                    {selectedRun.status === '启动失败' ? '重新启动' : '恢复会话'}
                  {/if}
                </button>
              {:else if selectedRun.type === 'automation_run'}
                <button
                  class:waiting={automationActionRunId === selectedRun.id}
                  disabled={['等待中', '运行中'].includes(selectedRun.status) || Boolean(automationActionRunId)}
                  on:click={() => rerunAutomationRun(selectedRun)}
                >
                  {automationActionRunId === selectedRun.id ? '运行中...' : '重新运行'}
                </button>
              {/if}
              <button disabled={!canOpenDebug(selectedRun)} on:click={() => dispatch('debug', selectedRun.id)}>进入调试工具</button>
            </div>
          </div>
          <div class="detail-tabs">
            {#each [
              ['result', selectedRun.type === 'work_session' ? '会话内容' : '运行结果'],
              ...(selectedRun.type === 'automation_run' ? [['input', '运行输入']] : []),
              ['events', '事件'],
              ['artifacts', '产出物'],
            ] as tab}
              <button class:active={activeDetailTab === tab[0]} on:click={() => setDetailTab(tab[0] as typeof activeDetailTab)}>{tab[1]}</button>
            {/each}
          </div>
          <div class="detail-body">
            {#if detailLoading}
              <div class="alert info">正在加载运行详情...</div>
            {/if}
            {#if activeDetailTab === 'result'}
              {#if selectedRun.type === 'work_session'}
                <div class="run-chat-pane">
                  <div class="message-stack" bind:this={messageScroll}>
                    {#if detailLoading && runMessages(selectedRun).length === 0}
                      <div class="skeleton-content-block" aria-label="正在加载会话内容"></div>
                    {:else if runMessages(selectedRun).length === 0}
                      <div class="run-result-summary">
                        <span class="home-pill" class:blue={statusClass(selectedRun.status) === 'blue'} class:green={statusClass(selectedRun.status) === 'green'} class:red={statusClass(selectedRun.status) === 'red'} class:gray={statusClass(selectedRun.status) === 'gray'}>{selectedRun.status}</span>
                        <h3>{selectedRun.status === '已停止' ? '会话已停止' : selectedRun.status === '启动失败' ? '会话启动失败' : '暂无会话消息'}</h3>
                        <p>{selectedRun.errorSummary || '当前会话没有可展示的消息内容，可查看事件或进入调试工具。'}</p>
                        <div class="facts-grid compact">
                          <div><b>开始时间</b><span>{formatTime(selectedRun.startedAt)}</span></div>
                          <div><b>更新时间</b><span>{formatTime(selectedRun.completedAt)}</span></div>
                          <div><b>智能体</b><span>{selectedRun.agent || '-'}</span></div>
                          <div><b>Workspace</b><span>{selectedRun.workspace || '-'}</span></div>
                        </div>
                      </div>
                    {:else}
                      {#each runMessages(selectedRun) as message (message.renderKey || message.id || `${message.role}-${message.at}`)}
                        <article class="message-card" class:failed={message.failed} class:running={message.running}>
                          <div class="message-cell-head">
                            <div class="message-cell-summary">
                              <div class="message-title-row">
                                {#if message.id}
                                  <span
                                    class="message-cell-id"
                                    role="button"
                                    tabindex="0"
                                    title={copiedId === message.id ? '已复制 cell id' : '复制 cell id'}
                                    on:click={(event) => copyText(message.id || '', event)}
                                    on:keydown={(event) => {
                                      if (event.key === 'Enter' || event.key === ' ') {
                                        event.preventDefault();
                                        void copyText(message.id || '');
                                      }
                                    }}
                                  >
                                    {message.id}
                                  </span>
                                  <span
                                    class="message-cell-copy"
                                    role="button"
                                    tabindex="0"
                                    title={copiedId === message.id ? '已复制 cell id' : '复制 cell id'}
                                    on:click={(event) => copyText(message.id || '', event)}
                                    on:keydown={(event) => {
                                      if (event.key === 'Enter' || event.key === ' ') {
                                        event.preventDefault();
                                        void copyText(message.id || '');
                                      }
                                    }}
                                  >
                                    <AntIcon definition={CopyOutlined} />
                                  </span>
                                {/if}
                                <span class={`message-status ${messageStatusTone(message)}`}>{messageStatus(message)}</span>
                                {#if message.id && copiedId === message.id && copyNotice}
                                  <span class="message-copy-tip" class:bad={!copyNotice.ok}>{copyNotice.text}</span>
                                {/if}
                              </div>
                              {#if message.source}
                                <pre class="message-source">{message.source}</pre>
                              {/if}
                            </div>
                            <div class="message-cell-meta">
                              <span>{formatTime(message.at)}</span>
                            </div>
                          </div>
                          {#if message.role === 'agent'}
                            <div
                              class="run-terminal-block"
                              class:running={message.running}
                              use:trackTerminalVisibility={message.id || ''}
                            >
                              {#if message.running || !message.id || terminalVisibleIds.has(message.id)}
                                <div
                                  class="run-terminal-frame"
                                  use:terminalRenderer={{
                                    id: message.id,
                                    text: messageTerminalText(message),
                                    running: Boolean(message.running),
                                  }}
                                ></div>
                              {:else}
                                <div
                                  class="run-terminal-frame terminal-placeholder"
                                  style:height={`${estimateTerminalHeight(message)}px`}
                                  aria-hidden="true"
                                ></div>
                              {/if}
                            </div>
                          {:else}
                            <pre class="run-terminal-static">{messageTerminalText(message)}</pre>
                          {/if}
                        </article>
                      {/each}
                    {/if}
                  </div>
                  <div class="run-message-composer" class:disabled={!canSendMessage(selectedRun)}>
                    <textarea
                      bind:value={messageText}
                      rows="3"
                      placeholder={canSendMessage(selectedRun) ? '输入消息，Enter 发送' : messageInputHint(selectedRun)}
                      disabled={!canSendMessage(selectedRun)}
                      on:keydown={(event) => handleMessageKeydown(event, selectedRun)}
                    ></textarea>
                    <div class="run-input-actions">
                      <span class:waiting={!canSendMessage(selectedRun)}>
                        {messageInputHint(selectedRun)}
                      </span>
                      <button
                        class="run-send-button"
                        class:waiting={!canSendMessage(selectedRun)}
                        disabled={!messageText.trim() || !canSendMessage(selectedRun)}
                        title={canSendMessage(selectedRun) ? '发送' : messageInputHint(selectedRun)}
                        aria-label={canSendMessage(selectedRun) ? '发送消息' : messageInputHint(selectedRun)}
                        on:click={() => sendMessage(selectedRun)}
                      >
                        发送
                      </button>
                    </div>
                  </div>
                </div>
              {:else}
                {#if selectedRun.output}
                  <pre>{selectedRun.output}</pre>
                {:else}
                  <div class="empty">暂无运行结果。</div>
                {/if}
                {#if selectedRun.errorSummary}
                  <div class="alert danger">{selectedRun.errorSummary}</div>
                {/if}
              {/if}
            {:else if activeDetailTab === 'input'}
              {#if selectedRun.type === 'automation_run'}
                {#if selectedRun.input}
                  <pre>{selectedRun.input}</pre>
                {:else}
                  <div class="empty">暂无运行输入。</div>
                {/if}
              {:else}
                <div class="empty">工作会话无独立运行输入。</div>
              {/if}
            {:else if activeDetailTab === 'events'}
              {#if selectedRun.events.length === 0}
                <div class="empty">暂无事件。</div>
              {:else}
                <div class="event-list">
                  {#each [...selectedRun.events].reverse() as event}
                    <div class={`list-item event-item event-${eventLevelTone(event.level)}`}>
                      <span>
                        <b>
                          <span>{event.type}</span>
                          <em class="event-level-pill">{event.level || 'info'}</em>
                        </b>
                        <small>{event.message} · {formatTime(event.createdAt)}</small>
                      </span>
                    </div>
                  {/each}
                </div>
              {/if}
            {:else if activeDetailTab === 'artifacts'}
              {#if selectedRun.artifacts.length === 0}
                <div class="empty">暂无已登记产出物。</div>
              {:else}
                <div class="event-list">
                  {#each selectedRun.artifacts as artifact}
                    <div class="list-item"><span><b>{artifact.name}</b><small>{artifact.mimeType} · {artifact.size} · {artifact.source}</small></span></div>
                  {/each}
                </div>
              {/if}
            {/if}
          </div>
        {:else}
          <div class="empty">请选择一条运行记录。</div>
        {/if}
      </div>
      <aside class="run-side-panel">
        {#if selectedRun}
          <section>
            <h3>运行信息</h3>
            <div class="side-facts">
              <div><span>运行 ID</span><b title={selectedRun.id}>{selectedRun.id}</b></div>
              <div><span>类型</span><b>{selectedRun.type === 'work_session' ? '工作会话' : '自动化运行'}</b></div>
              <div><span>状态</span><b><span class={`home-pill ${statusClass(selectedRun.status)}`}>{selectedRun.status}</span></b></div>
              <div><span>智能体</span><b title={selectedRun.agent || '-'}>{selectedRun.agent || '-'}</b></div>
              <div><span>触发</span><b>{selectedRun.trigger}</b></div>
              <div><span>Workspace</span><b title={selectedRun.workspace || '-'}>{selectedRun.workspace || '-'}</b></div>
            </div>
          </section>
          <section>
            <h3>运行事实</h3>
            <div class="side-facts">
              <div><span>开始时间</span><b title={formatTime(selectedRun.startedAt)}>{formatTime(selectedRun.startedAt)}</b></div>
              <div><span>完成时间</span><b title={formatTime(selectedRun.completedAt)}>{formatTime(selectedRun.completedAt)}</b></div>
              <div><span>持续时间</span><b>{runDuration(selectedRun)}</b></div>
              <div><span>产出物</span><b>{selectedRun.artifacts.length}</b></div>
            </div>
          </section>
        {:else}
          <div class="empty">请选择一条运行记录。</div>
        {/if}
      </aside>
    </div>
  {/if}
</section>
