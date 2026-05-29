<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed, nextTick, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

interface AgentState {
  status: string
  message: string
  code: string
  sharedDir: string
  serverUrl: string
  tunnelInfo: string
  acpEnabled: boolean
}

interface EnvStatus {
  hasNpm: boolean
  npmVer: string
  hasPip: boolean
  pipVer: string
}

interface ACPAgent {
  id: string
  name: string
  icon: string
  installType: string
  installCmd: string
  installed: boolean
  version: string
  helpUrl: string
}

type AppState = 'disconnected' | 'connecting' | 'connected'

const NPM_GUIDE = 'https://blog.csdn.net/weixin_41929531/article/details/158885541'

const wails = (window as any)['go']['main']['App']

const appState = ref<AppState>('disconnected')
const codeInput = ref('')
const selectedDir = ref('.')
const errorMessage = ref('')
const version = ref('v0.8.0')
const envStatus = ref<EnvStatus>({ hasNpm: false, npmVer: '', hasPip: false, pipVer: '' })
const agents = ref<ACPAgent[]>([])
const selectedAgent = ref('')
const showTerminal = ref(false)
const terminalReady = ref(false)

let pollTimer: ReturnType<typeof setInterval> | null = null
let xterm: Terminal | null = null
let fitAddon: FitAddon | null = null

const statusLabel = computed(() => {
  switch (appState.value) {
    case 'connecting': return '正在连接...'
    case 'connected': return '已连接'
    default: return '未连接'
  }
})

const hasAnyInstalled = computed(() => agents.value.some(a => a.installed))

const installBlocked = computed(() => {
  return !envStatus.value.hasNpm
})

async function loadEnv() {
  try {
    envStatus.value = await wails.CheckEnvironment()
  } catch (e) { console.error(e) }
}

async function loadAgents() {
  try {
    agents.value = await wails.GetACPAgents()
    const installed = agents.value.find((a: ACPAgent) => a.installed)
    if (installed) selectedAgent.value = installed.id
  } catch (e) { console.error(e) }
}

async function fetchVersion() {
  try {
    const v = await wails.GetVersion()
    if (v) version.value = v
  } catch {}
}

function initTerminal() {
  if (xterm) return

  const el = document.getElementById('terminal-container')
  if (!el) return

  xterm = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: '"SF Mono", Monaco, Menlo, Consolas, monospace',
    theme: {
      background: '#1e1e1e',
      foreground: '#d4d4d4',
      cursor: '#d4d4d4',
      selectionBackground: '#264f78',
    },
    rows: 8,
    cols: 60,
  })

  fitAddon = new FitAddon()
  xterm.loadAddon(fitAddon)
  xterm.open(el)
  fitAddon.fit()

  xterm.onData((data: string) => {
    wails.TerminalWrite(data)
  })

  terminalReady.value = true
}

watch(showTerminal, async (val) => {
  if (val) {
    await nextTick()
    initTerminal()
    try { await wails.TerminalStart() } catch {}
    setTimeout(() => fitAddon?.fit(), 100)
  }
})

async function installAgent(agent: ACPAgent) {
  if (agent.installType === 'npm' && !envStatus.value.hasNpm) {
    errorMessage.value = `安装 ${agent.name} 需要 npm。请先安装 Node.js 环境。`
    return
  }

  showTerminal.value = true
  await nextTick()
  initTerminal()

  await wails.TerminalStart()
  xterm?.focus()
  wails.TerminalRunCommand(agent.installCmd)

  setTimeout(async () => {
    await loadAgents()
    await loadEnv()
  }, 3000)
}

function openGuide() {
  wails.OpenURL(NPM_GUIDE)
}

function clearPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

function startPolling(ms: number) {
  clearPolling()
  pollTimer = setInterval(async () => {
    try {
      const state: AgentState = await wails.GetState()
      handleStateUpdate(state)
    } catch (e: any) { showError(e?.message || String(e)) }
  }, ms)
}

function handleStateUpdate(state: AgentState) {
  switch (state.status) {
    case 'connected':
      appState.value = 'connected'
      tunnelInfo.code = state.code
      tunnelInfo.sharedDir = state.sharedDir
      tunnelInfo.serverUrl = state.serverUrl
      tunnelInfo.acpEnabled = state.acpEnabled
      clearPolling()
      startPolling(1000)
      break
    case 'connecting':
      appState.value = 'connecting'
      break
    case 'disconnected':
      appState.value = 'disconnected'
      clearPolling()
      break
    case 'error':
      showError(state.message || '连接失败')
      appState.value = 'disconnected'
      clearPolling()
      break
  }
}

async function handleConnect() {
  if (!codeInput.value.trim()) { showError('请输入连接码'); return }
  clearError()

  if (selectedAgent.value) {
    try { await wails.SetACPRunner(selectedAgent.value) } catch {}
  }

  appState.value = 'connecting'
  startPolling(500)

  try {
    const err = await wails.Connect(codeInput.value, '', selectedDir.value)
    if (err) {
      showError(typeof err === 'string' ? err : err?.message || '连接失败')
      appState.value = 'disconnected'
      clearPolling()
    }
  } catch (e: any) {
    showError(e?.message || String(e))
    appState.value = 'disconnected'
    clearPolling()
  }
}

async function handleDisconnect() {
  try { await wails.Disconnect() } catch {}
  appState.value = 'disconnected'
  tunnelInfo.code = ''
  tunnelInfo.sharedDir = ''
  tunnelInfo.serverUrl = ''
  tunnelInfo.acpEnabled = false
  clearPolling()
}

async function handleSelectDir() {
  try {
    const dir = await wails.SelectDirectory()
    if (dir) selectedDir.value = dir
  } catch {}
}

function showError(msg: string) { errorMessage.value = msg }
function clearError() { errorMessage.value = '' }
function handleRetry() { clearError(); appState.value = 'disconnected'; clearPolling() }

const tunnelInfo = reactive({
  code: '', sharedDir: '', serverUrl: '', acpEnabled: false,
})

onMounted(() => {
  fetchVersion()
  loadEnv()
  loadAgents()

  if ((window as any)['runtime']) {
    (window as any)['runtime'].EventsOn('terminal:data', (data: string) => {
      xterm?.write(data)
    })
    ;(window as any)['runtime'].EventsOn('terminal:exit', () => {
      xterm?.write('\r\n\x1b[33m[进程已退出]\x1b[0m\r\n')
    })
  }
})

onUnmounted(() => {
  clearPolling()
  wails.TerminalClose?.()
})
</script>

<template>
  <div class="app-container">
    <header class="header">
      <div class="header__logo">📁</div>
      <h1 class="header__title">TeamWorker 文件共享</h1>
      <div class="header__version">{{ version }}</div>
    </header>

    <Transition name="fade">
      <div v-if="errorMessage" class="error-banner">
        <span class="error-banner__icon">⚠️</span>
        <span class="error-banner__msg">{{ errorMessage }}</span>
        <button class="btn btn--sm btn--ghost" @click="handleRetry">重试</button>
      </div>
    </Transition>

    <!-- ═══ Disconnected ═══ -->
    <template v-if="appState === 'disconnected'">
      <div class="form-group">
        <label class="form-label">连接码</label>
        <input v-model="codeInput" class="input"
          placeholder="ABCD-EFGH 或 ABCD-EFGH@http://server:8082"
          @keyup.enter="handleConnect" />
      </div>

      <div class="form-group">
        <label class="form-label">共享目录</label>
        <div class="dir-selector">
          <span class="dir-selector__path" :title="selectedDir">{{ selectedDir }}</span>
          <button class="dir-selector__btn" @click="handleSelectDir">选择目录</button>
        </div>
      </div>

      <div class="divider" />

      <div class="form-group">
        <label class="form-label">外部智能体</label>

        <div v-if="envStatus.hasNpm" class="env-ok">
          ✅ npm {{ envStatus.npmVer }}
        </div>
        <div v-else class="env-warning">
          <span>⚠️ 未检测到 npm</span>
          <a class="env-warning__link" href="#" @click.prevent="openGuide">
            如何安装 Node.js/npm →
          </a>
        </div>

        <div class="agent-grid">
          <div v-for="agent in agents" :key="agent.id"
            class="agent-card"
            :class="{
              'agent-card--selected': selectedAgent === agent.id,
              'agent-card--installed': agent.installed,
              'agent-card--not-installed': !agent.installed,
            }"
            @click="agent.installed && (selectedAgent = agent.id)"
          >
            <div class="agent-card__icon">{{ agent.icon }}</div>
            <div class="agent-card__name">{{ agent.name }}</div>
            <div class="agent-card__status">
              <span v-if="agent.installed" class="agent-installed">
                ✅ {{ agent.version?.split('\n')[0]?.substring(0, 12) }}
              </span>
              <span v-else class="agent-not-installed">未安装</span>
            </div>
            <button v-if="!agent.installed"
              class="btn btn--sm btn--primary agent-card__install"
              :disabled="agent.installType === 'npm' && !envStatus.hasNpm"
              @click.stop="installAgent(agent)"
            >
              {{ agent.installType === 'npm' && !envStatus.hasNpm ? '需 npm' : '安装' }}
            </button>
          </div>
        </div>

        <Transition name="fade">
          <div v-if="installBlocked && !envStatus.hasNpm" class="npm-help-banner">
            <div class="npm-help-banner__icon">💡</div>
            <div class="npm-help-banner__content">
              <p>安装智能体需要 <strong>npm</strong>（Node.js 包管理器）。</p>
              <p>请先安装 Node.js，然后重启本应用。</p>
              <a class="npm-help-banner__link" href="#" @click.prevent="openGuide">
                📖 Node.js / npm 安装教程 →
              </a>
            </div>
          </div>
        </Transition>
      </div>

      <div class="form-group">
        <div class="terminal-header" @click="showTerminal = !showTerminal">
          <span class="form-label" style="cursor:pointer">
            {{ showTerminal ? '▼ 终端' : '▶ 终端' }}
          </span>
        </div>
        <Transition name="fade">
          <div v-if="showTerminal" class="terminal-wrapper">
            <div id="terminal-container" class="terminal-xterm" />
          </div>
        </Transition>
      </div>

      <div class="mt-auto">
        <button class="btn btn--primary btn--block"
          :disabled="!codeInput.trim()"
          @click="handleConnect"
        >
          连接
        </button>
      </div>
    </template>

    <!-- ═══ Connecting ═══ -->
    <template v-if="appState === 'connecting'">
      <div class="spinner-area">
        <div class="spinner" />
        <span class="spinner-area__text">正在连接...</span>
      </div>
      <div class="mt-auto">
        <button class="btn btn--ghost btn--block" @click="handleRetry">取消</button>
      </div>
    </template>

    <!-- ═══ Connected ═══ -->
    <template v-if="appState === 'connected'">
      <div class="status-badge status-badge--connected">
        <span class="status-badge__dot" />
        <span>已连接</span>
      </div>

      <div class="info-card">
        <div class="info-card__row">
          <span class="info-card__label">共享目录</span>
          <span class="info-card__value" :title="tunnelInfo.sharedDir">
            {{ tunnelInfo.sharedDir }}
          </span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">连接码</span>
          <span class="info-card__value">{{ tunnelInfo.code }}</span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">服务器</span>
          <span class="info-card__value">{{ tunnelInfo.serverUrl }}</span>
        </div>
        <div v-if="selectedAgent" class="info-card__row">
          <span class="info-card__label">智能体</span>
          <span class="info-card__value">{{ selectedAgent }}</span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">隧道</span>
          <span class="info-card__value info-card__value--small">
            {{ tunnelInfo.acpEnabled ? 'MCP + ACP' : 'MCP only' }}
          </span>
        </div>
      </div>

      <div class="mt-auto">
        <button class="btn btn--danger-outline btn--block" @click="handleDisconnect">
          断开连接
        </button>
      </div>
    </template>
  </div>
</template>
