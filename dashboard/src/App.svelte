<script>
  import { onMount } from 'svelte'
  import { createTable, getCoreRowModel, getFilteredRowModel, getPaginationRowModel } from '@tanstack/table-core'
  import hljs from 'highlight.js/lib/core'
  import bash from 'highlight.js/lib/languages/bash'
  import css from 'highlight.js/lib/languages/css'
  import go from 'highlight.js/lib/languages/go'
  import ini from 'highlight.js/lib/languages/ini'
  import javascript from 'highlight.js/lib/languages/javascript'
  import json from 'highlight.js/lib/languages/json'
  import markdown from 'highlight.js/lib/languages/markdown'
  import plaintext from 'highlight.js/lib/languages/plaintext'
  import python from 'highlight.js/lib/languages/python'
  import typescript from 'highlight.js/lib/languages/typescript'
  import xml from 'highlight.js/lib/languages/xml'
  import yaml from 'highlight.js/lib/languages/yaml'

  let state = { skills: [], producers: [], agents: [], repo: '', repoLabel: '', head: '', dirty: false }
  let page = 'skills'
  let query = ''
  let updateFilter = 'all'
  let usageFilter = 'all'
  let pageIndex = 0
  let pageSize = 30
  let selected = null
  let loading = true
  let working = ''
  let error = ''
  let toast = ''
  let updateRun = { producer: '', phase: 'idle', message: '' }
  let finderFiles = []
  let finderFile = null
  let finderLoading = false
  let finderError = ''
  let copied = false
  let addSource = false
  let sourceForm = { id: '', root: '', note: '', build: 'make skill', output: 'dist/skills' }

  const pageNames = { skills: '我的技能', agents: 'Agent', activity: '最近动态' }
  const agentLabel = { 'codex.global': 'Codex', 'claude.global': 'Claude', 'pi.global': 'Pi' }
  const pageSizes = [15, 30, 50, 100]
  const pageSizeKey = 'sm.dashboard.pageSize'
  const updateFilterLabel = { updated: '有更新', current: '已是最新', error: '有问题' }
  const usageFilterLabel = { 'claude.global': 'Claude 在用', 'codex.global': 'Codex 在用', 'pi.global': 'Pi 在用', unused: '未使用' }
  const languages = { bash, css, go, ini, javascript, json, markdown, plaintext, python, typescript, xml, yaml }
  for (const [name, language] of Object.entries(languages)) hljs.registerLanguage(name, language)
  const columns = [
    { accessorKey: 'id' },
    { accessorKey: 'description' },
    { accessorKey: 'note' },
    {
      id: 'updateScope',
      accessorFn: skill => skill.update,
      enableGlobalFilter: false,
      filterFn: (row, _column, value) => row.original.update === value
    },
    {
      id: 'usageScope',
      accessorFn: skill => skill.agents,
      enableGlobalFilter: false,
      filterFn: (row, _column, value) => {
        const skill = row.original
        if (value === 'unused') return skill.agents.length === 0
        return skill.agents.includes(value)
      }
    }
  ]

  $: table = createTable({
    data: state.skills,
    columns,
    state: {
      globalFilter: query,
      columnFilters: [
        ...(updateFilter === 'all' ? [] : [{ id: 'updateScope', value: updateFilter }]),
        ...(usageFilter === 'all' ? [] : [{ id: 'usageScope', value: usageFilter }])
      ],
      pagination: { pageIndex, pageSize }
    },
    onStateChange: () => {},
    renderFallbackValue: null,
    globalFilterFn: 'includesString',
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel()
  })
  $: visibleSkills = table.getRowModel().rows.map(row => row.original)
  $: filteredCount = table.getFilteredRowModel().rows.length
  $: pageCount = Math.max(1, table.getPageCount())
  $: skillGroups = (() => {
    const producers = new Map(state.producers.map(producer => [producer.id, producer]))
    const groups = new Map()
    for (const skill of visibleSkills) {
      const key = skill.producer || ''
      if (!groups.has(key)) groups.set(key, { id: key, producer: producers.get(key), skills: [] })
      groups.get(key).skills.push(skill)
    }
    return [...groups.values()].sort((left, right) => {
      if (!left.id) return 1
      if (!right.id) return -1
      return left.id.localeCompare(right.id)
    })
  })()
  $: usedCount = state.skills.filter(skill => skill.agents.length).length
  $: selectedProducer = selected?.producer ? state.producers.find(producer => producer.id === selected.producer) : null
  $: producerCommand = selectedProducer ? `cd ${shellWord(selectedProducer.rootLabel || selectedProducer.root)} && ${selectedProducer.buildArgv.map(shellWord).join(' ')}` : ''
  $: unsyncedAgents = state.agents.filter(agent => !agent.synced)
  $: libraryStatus = loading ? '正在读取…' : state.dirty ? '技能库有尚未提交的变更' : unsyncedAgents.length ? `${unsyncedAgents.map(agent => agent.name).join('、')} 需要同步` : '所有 Agent 已同步'
  $: highlightedCode = finderFile?.preview ? hljs.highlight(finderFile.content, { language: languages[finderFile.language] ? finderFile.language : 'plaintext' }).value : ''
  $: codeLines = finderFile?.preview ? finderFile.content.split('\n').length : 0

  async function api(path, options) {
    const response = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...options })
    const body = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(body.error || `请求失败 (${response.status})`)
    return body
  }

  async function refresh() {
    loading = true
    try {
      const selectedID = new URLSearchParams(location.search).get('skill') || selected?.id
      state = await api('/api/state')
      if (selectedID) {
        const skill = state.skills.find(item => item.id === selectedID) || null
        if (skill && selected?.id !== skill.id) await openSkill(skill)
        else selected = skill
      }
      error = ''
    }
    catch (cause) { error = cause.message }
    finally { loading = false }
  }

  async function toggleGrant(skill, agent) {
    const enabled = !skill.agents.includes(agent)
    working = `正在同步 ${agentLabel[agent]}…`
    try {
      state = await api(`/api/skills/${encodeURIComponent(skill.id)}/grants`, { method: 'POST', body: JSON.stringify({ consumer: agent, enabled }) })
      selected = state.skills.find(item => item.id === skill.id)
      showToast('修改已同步到 Agent')
    } catch (cause) { error = cause.message; await refresh() }
    finally { working = '' }
  }

  async function updateProducer(id) {
    working = `正在更新 ${id}…`
    updateRun = { producer: id, phase: 'running', message: '正在运行生成命令…' }
    try {
      state = await api(`/api/producers/${encodeURIComponent(id)}/update`, { method: 'POST' })
      if (selected) selected = state.skills.find(skill => skill.id === selected.id) || null
      updateRun = { producer: id, phase: 'success', message: '更新完成，所有 Agent 已同步' }
      showToast(`${id} 已更新`)
    }
    catch (cause) {
      updateRun = { producer: id, phase: 'error', message: `运行失败：${cause.message}` }
      error = cause.message
    }
    finally { working = '' }
  }

  async function createProducer() {
    working = '正在添加技能来源…'
    try {
      state = await api('/api/producers', { method: 'POST', body: JSON.stringify(sourceForm) })
      addSource = false
      sourceForm = { id: '', root: '', note: '', build: 'make skill', output: 'dist/skills' }
      showToast('技能来源已添加')
    } catch (cause) { error = cause.message }
    finally { working = '' }
  }

  function showToast(message) { toast = message; setTimeout(() => toast === message && (toast = ''), 1800) }
  function shellWord(word) { return /^[A-Za-z0-9_./:@%+=,-]+$/.test(word) ? word : `'${word.replaceAll("'", "'\\''")}'` }
  function changeUpdateFilter(event) { updateFilter = event.currentTarget.value; pageIndex = 0 }
  function changeUsageFilter(event) { usageFilter = event.currentTarget.value; pageIndex = 0 }
  function clearFilters() { updateFilter = 'all'; usageFilter = 'all'; pageIndex = 0 }
  function updateQuery(event) { query = event.currentTarget.value; pageIndex = 0 }
  function changePageSize(event) {
    pageSize = Number(event.currentTarget.value)
    pageIndex = 0
    localStorage.setItem(pageSizeKey, String(pageSize))
  }
  async function openSkill(skill) {
    selected = skill
    if (updateRun.producer !== skill.producer) updateRun = { producer: '', phase: 'idle', message: '' }
    finderFiles = []
    finderFile = null
    finderError = ''
    finderLoading = true
    try {
      const result = await api(`/api/skills/${encodeURIComponent(skill.id)}/files`)
      finderFiles = result.files
      const first = finderFiles.find(file => !file.directory && file.path === 'SKILL.md') || finderFiles.find(file => !file.directory)
      if (first) await openFile(first.path)
    } catch (cause) { finderError = cause.message }
    finally { finderLoading = false }
  }
  async function openFile(path) {
    finderLoading = true
    finderError = ''
    copied = false
    try { finderFile = await api(`/api/skills/${encodeURIComponent(selected.id)}/file?path=${encodeURIComponent(path)}`) }
    catch (cause) { finderError = cause.message }
    finally { finderLoading = false }
  }
  async function copyFile() {
    if (!finderFile?.preview) return
    await navigator.clipboard.writeText(finderFile.content)
    copied = true
    setTimeout(() => copied = false, 1400)
  }
  function closeDetail() { selected = null; finderFiles = []; finderFile = null; finderError = '' }
  function handleKeydown(event) { if (event.key === 'Escape' && selected) closeDetail() }
  function fileName(path) { return path.split('/').at(-1) }
  function fileDepth(path) { return Math.max(0, path.split('/').length - 1) }
  function formatBytes(size) { return size < 1024 ? `${size} B` : size < 1024 * 1024 ? `${(size / 1024).toFixed(1)} KB` : `${(size / 1024 / 1024).toFixed(1)} MB` }
  onMount(() => {
    const saved = Number(localStorage.getItem(pageSizeKey))
    if (pageSizes.includes(saved)) pageSize = saved
    refresh()
  })
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="app">
  <aside class="sidebar">
    <div class="brand"><div class="logo">sm</div><div><b>技能管理</b><small>SKILL MANAGER</small></div></div>
    <div class="nav-label">管理</div>
    <nav class="nav">
      <button class:active={page === 'skills'} on:click={() => page = 'skills'}><span class="nav-icon">▦</span><span>我的技能</span><em>{state.skills.length}</em></button>
      <button class:active={page === 'agents'} on:click={() => page = 'agents'}><span class="nav-icon">♙</span><span>Agent</span><em>{state.agents.length}</em></button>
      <button class:active={page === 'activity'} on:click={() => page = 'activity'}><span class="nav-icon">⌁</span><span>最近动态</span></button>
    </nav>
    <div class="source"><small>技能库位置</small><code>{state.repoLabel || state.repo || '~/.sm'}</code><span class:warn={state.dirty || unsyncedAgents.length}><i></i>{libraryStatus}</span></div>
  </aside>

  <main>
    <header class="topbar"><div class="crumb">技能库 / <b>{pageNames[page]}</b></div></header>
    {#if error}<div class="error"><span>{error}</span><button on:click={() => error = ''}>×</button></div>{/if}

    {#if page === 'skills'}
      <section class="page">
        <div class="page-head"><div><h1>我的技能</h1><p class="subtitle">管理技能来源，并决定每个 Agent 可以使用哪些技能。</p></div><button class="btn primary" on:click={() => addSource = true}>＋ 添加来源</button></div>
        <div class="summary"><div class="stat"><b>{state.skills.length}</b><span>个技能</span></div><div class="stat"><b>{usedCount}</b><span>正在使用</span></div><div class="stat"><b>{state.skills.length - usedCount}</b><span>暂未使用</span></div></div>
        <div class="toolbar"><label class="search"><span>⌕</span><input value={query} on:input={updateQuery} placeholder="搜索技能"></label><label class="filter-select"><span>更新状态</span><select value={updateFilter} on:change={changeUpdateFilter}><option value="all">全部</option><option value="updated">有更新</option><option value="current">已是最新</option><option value="error">有问题</option></select></label><label class="filter-select"><span>使用范围</span><select value={usageFilter} on:change={changeUsageFilter}><option value="all">全部</option><option value="claude.global">Claude 在用</option><option value="codex.global">Codex 在用</option><option value="pi.global">Pi 在用</option><option value="unused">未使用</option></select></label>{#if updateFilter !== 'all' || usageFilter !== 'all'}<button class="clear-filters" on:click={clearFilters}>清除筛选</button>{/if}</div>
        {#if updateFilter !== 'all' || usageFilter !== 'all'}<div class="filter-chips">{#if updateFilter !== 'all'}<button on:click={() => { updateFilter = 'all'; pageIndex = 0 }}>{updateFilterLabel[updateFilter]} <span>×</span></button>{/if}{#if usageFilter !== 'all'}<button on:click={() => { usageFilter = 'all'; pageIndex = 0 }}>{usageFilterLabel[usageFilter]} <span>×</span></button>{/if}<small>共 {filteredCount} 个技能</small></div>{/if}
        <div class="matrix"><div class="matrix-head"><div>技能</div><div>使用状态</div><div></div></div>
          {#each skillGroups as group (group.id)}
            {#if !group.producer || group.producer.skillCount > 1}<div class="group-head"><div><strong>{group.id || '直接维护'}</strong><span>{group.skills.length} 个技能{group.producer ? ` · ${group.producer.statusLabel}` : ''}</span></div></div>{/if}
            {#each group.skills as skill (skill.id)}
              <div class="skill" role="button" tabindex="0" on:click={() => openSkill(skill)} on:keydown={(event) => event.key === 'Enter' && openSkill(skill)}>
                <div><div class="skill-title"><strong>{skill.id}</strong>{#if skill.update === 'updated'}<span class="update-indicator">可更新</span>{:else if skill.update === 'error'}<span class="bad">有问题</span>{/if}</div><small>{skill.note || skill.description}</small></div>
                <div class="usage">{#if skill.agents.length}{#each skill.agents as agent}<span>{agentLabel[agent] || agent}</span>{/each}{:else}<small>暂未使用</small>{/if}</div>
                <div class="arrow">›</div>
              </div>
            {/each}
          {:else}<div class="empty">没有符合条件的技能</div>{/each}
        </div>
        <div class="pagination"><span>共 {filteredCount} 个技能</span><div><label>每页 <select value={pageSize} on:change={changePageSize}>{#each pageSizes as size}<option value={size}>{size}</option>{/each}</select></label><button class="page-button" disabled={pageIndex === 0} on:click={() => pageIndex -= 1}>上一页</button><span>{pageIndex + 1} / {pageCount}</span><button class="page-button" disabled={pageIndex + 1 >= pageCount} on:click={() => pageIndex += 1}>下一页</button></div></div>
      </section>
    {:else if page === 'agents'}
      <section class="page"><h1>Agent</h1><p class="subtitle">查看每个 Agent 当前能使用的技能。</p><div class="agent-grid">{#each state.agents as agent}<div class="agent-card"><div class="agent-icon">{agent.short}</div><h3>{agent.name}</h3><p>全局环境</p><div class="agent-count">{agent.skillCount}<span>个技能</span></div><div class:bad={!agent.synced} class="agent-status">● {agent.synced ? '已同步' : '需要同步'}</div></div>{/each}</div></section>
    {:else}
      <section class="page"><h1>最近动态</h1><p class="subtitle">技能库和 Agent 的最近变化。</p><div class="activity-list"><div class="event"><time>当前</time><span>{state.dirty ? '技能库有尚未提交的变化' : '技能库与 Agent 已同步'}</span><code>{state.head}</code></div></div></section>
    {/if}
  </main>
</div>

{#if selected}
  <button class="detail-backdrop" aria-label="关闭" on:click={closeDetail}></button>
  <div class="detail-dialog" role="dialog" aria-modal="true" aria-label={`${selected.id} Skill Finder`}>
    <header class="detail-head"><div class="detail-icon">SK</div><div><h2>{selected.id}</h2><p>{selected.note || selected.description}</p></div><span class="readonly-badge">只读浏览</span><span class="file-count">{finderFiles.filter(file => !file.directory).length} 个文件</span><button class="close detail-close" on:click={closeDetail}>×</button></header>
    <div class="detail-workspace">
      <aside class="finder-pane"><div class="finder-head"><strong>文件</strong><span>FINDER</span></div><div class="file-tree">
        {#if finderLoading && !finderFiles.length}<div class="finder-message">正在读取文件…</div>
        {:else if finderError && !finderFiles.length}<div class="finder-message bad">{finderError}</div>
        {:else}{#each finderFiles as file (file.path)}<button class:active={finderFile?.path === file.path} class:directory={file.directory} disabled={file.directory} style={`--depth:${fileDepth(file.path)}`} on:click={() => !file.directory && openFile(file.path)}><i>{file.directory ? '▾' : '◇'}</i><span>{fileName(file.path)}</span>{#if !file.directory}<em>{formatBytes(file.size)}</em>{/if}</button>{/each}{/if}
      </div><div class="finder-path">{state.repoLabel || state.repo}/skills/{selected.id}</div></aside>
      <section class="code-pane"><div class="code-head"><span>{selected.id} / <b>{finderFile?.path || '选择文件'}</b></span>{#if finderFile?.preview}<button class:done={copied} on:click={copyFile}>{copied ? '已复制' : '复制内容'}</button>{/if}</div>
        <div class="code-body">{#if finderLoading && !finderFile}<div class="code-message">正在加载文件…</div>{:else if finderError}<div class="code-message bad">{finderError}</div>{:else if finderFile && !finderFile.preview}<div class="code-message"><strong>无法预览</strong><span>{finderFile.unavailable}</span></div>{:else if finderFile}<div class="code-view"><div class="line-numbers">{#each Array(codeLines) as _, index}<span>{index + 1}</span>{/each}</div><pre><code class="hljs">{@html highlightedCode}</code></pre></div>{:else}<div class="code-message">此 Skill 没有可预览的文件</div>{/if}</div>
      </section>
      <aside class="inspector-pane">
        <div class="inspector-section"><div class="section-label">备注</div><div class="inspector-card"><p class="skill-note">{selected.note || selected.description}</p></div></div>
        <div class="inspector-section"><div class="section-label">可在哪些 Agent 中使用</div><div class="access-list compact">{#each state.agents as agent}<div class="access"><div class="mini-icon">{agent.short}</div><div><strong>{agent.name}</strong><small>全局环境</small></div><button class:on={selected.agents.includes(agent.id)} class="toggle" disabled={!!working} on:click={() => toggleGrant(selected, agent.id)} aria-label={`切换 ${agent.name}`}></button></div>{/each}</div></div>
        <div class="inspector-section"><div class="section-label">技能来源</div><div class="source-card"><div class="source-top"><div><strong>{selected.producer || '直接维护'}</strong><span>{selected.producer ? '由此来源生成并负责后续更新' : '直接在技能库中维护'}</span></div>{#if selected.producer && selected.update === 'updated'}<button class="btn source-update" disabled={!!working} on:click={() => updateProducer(selected.producer)}>{updateRun.producer === selected.producer && updateRun.phase === 'running' ? '更新中…' : '更新'}</button>{/if}</div>{#if selectedProducer}<div class="command-box"><small>更新时将运行</small><code>{producerCommand}</code><span>命令完成后会自动收编产物并同步所有 Agent。</span></div>{/if}{#if updateRun.producer === selected.producer && updateRun.phase !== 'idle'}<div class:error={updateRun.phase === 'error'} class:success={updateRun.phase === 'success'} class:running={updateRun.phase === 'running'} class="run-status"><i></i>{updateRun.message}</div>{/if}</div></div>
        {#if finderFile}<div class="inspector-section"><div class="section-label">文件信息</div><div class="inspector-card file-meta"><span>类型</span><b>{finderFile.language}</b><span>大小</span><b>{formatBytes(finderFile.size)}</b><span>编码</span><b>{finderFile.preview ? 'UTF-8' : '—'}</b></div></div>{/if}
        <div class="inspector-section"><div class="section-label">技能库位置</div><div class="path-card">{state.repoLabel || state.repo}/skills/{selected.id}</div></div>
      </aside>
    </div>
  </div>
{/if}

{#if addSource}
  <div class="modal-bg"><form class="modal" on:submit|preventDefault={createProducer}><div class="modal-head"><h3>添加技能来源</h3><button type="button" class="close" on:click={() => addSource = false}>×</button></div><div class="modal-body"><p>选择一个能够生成技能的项目，并告诉 sm 如何生成和从哪里收取产物。</p><label>来源 ID<input bind:value={sourceForm.id} required placeholder="example"></label><label>备注（可选）<input bind:value={sourceForm.note} placeholder="用一句话说明这些技能的用途"></label><label>项目位置<input bind:value={sourceForm.root} required placeholder="/absolute/path/to/repo"></label><label>生成方式<input bind:value={sourceForm.build} required></label><label>产物位置<input bind:value={sourceForm.output} required></label></div><div class="modal-foot"><button type="button" class="btn" on:click={() => addSource = false}>取消</button><button class="btn primary" disabled={!!working}>添加来源</button></div></form></div>
{/if}
{#if toast}<div class="toast">{toast}</div>{/if}
