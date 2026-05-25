<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed } from 'vue'

interface AgentState {
  status: string
  message: string
  code: string
  sharedDir: string
  serverUrl: string
  tunnelInfo: string
}

type AppState = 'disconnected' | 'connecting' | 'connected'

const wails = (window as any)['go']['main']['App']

const appState = ref<AppState>('disconnected')
const codeInput = ref('')
const selectedDir = ref('.')
const errorMessage = ref('')
const tunnelInfo = reactive({
  code: '',
  sharedDir: '',
  serverUrl: '',
})
const version = ref('v0.7.0')

let pollTimer: ReturnType<typeof setInterval> | null = null

const statusLabel = computed(() => {
  switch (appState.value) {
    case 'connecting': return '正在连接...'
    case 'connected': return '已连接'
    default: return '未连接'
  }
})

function clearPolling() {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

function startPolling(intervalMs: number) {
  clearPolling()
  pollTimer = setInterval(async () => {
    try {
      const state: AgentState = await wails.GetState()
      handleStateUpdate(state)
    } catch (e: any) {
      showError(e?.message || String(e))
    }
  }, intervalMs)
}

function handleStateUpdate(state: AgentState) {
  switch (state.status) {
    case 'connected':
      appState.value = 'connected'
      tunnelInfo.code = state.code
      tunnelInfo.sharedDir = state.sharedDir
      tunnelInfo.serverUrl = state.serverUrl
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
    default:
      if (state.message) {
        showError(state.message)
      }
  }
}

function parseCodeInput(raw: string): { code: string; serverUrl: string } {
  const atIdx = raw.lastIndexOf('@')
  if (atIdx > 0) {
    return {
      code: raw.substring(0, atIdx).trim(),
      serverUrl: raw.substring(atIdx + 1).trim(),
    }
  }
  return { code: raw.trim(), serverUrl: '' }
}

async function handleConnect() {
  if (!codeInput.value.trim()) {
    showError('请输入连接码')
    return
  }

  clearError()
  const { code, serverUrl } = parseCodeInput(codeInput.value)

  appState.value = 'connecting'
  startPolling(500)

  try {
    const err = await wails.Connect(code, serverUrl, selectedDir.value)
    if (err) {
      showError(typeof err === 'string' ? err : err?.message || '连接失败')
      appState.value = 'disconnected'
      clearPolling()
      return
    }
  } catch (e: any) {
    showError(e?.message || String(e))
    appState.value = 'disconnected'
    clearPolling()
  }
}

async function handleDisconnect() {
  try {
    await wails.Disconnect()
  } catch (e: any) {
    showError(e?.message || String(e))
  }
  appState.value = 'disconnected'
  tunnelInfo.code = ''
  tunnelInfo.sharedDir = ''
  tunnelInfo.serverUrl = ''
  clearPolling()
}

async function handleSelectDir() {
  try {
    const dir = await wails.SelectDirectory()
    if (dir) {
      selectedDir.value = dir
    }
  } catch (e: any) {
    showError(e?.message || String(e))
  }
}

function showError(msg: string) {
  errorMessage.value = msg
}

function clearError() {
  errorMessage.value = ''
}

function handleRetry() {
  clearError()
  appState.value = 'disconnected'
  clearPolling()
}

async function fetchVersion() {
  try {
    const v = await wails.GetVersion()
    if (v) version.value = v
  } catch {
    // fallback to default
  }
}

onMounted(() => {
  fetchVersion()
})

onUnmounted(() => {
  clearPolling()
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
        <button class="btn btn--sm btn--ghost error-banner__action" @click="handleRetry">重试</button>
      </div>
    </Transition>

    <template v-if="appState === 'disconnected'">
      <div class="form-group">
        <label class="form-label">连接码</label>
        <input
          v-model="codeInput"
          class="input"
          placeholder="请输入连接码，如 ABCD-EFGH@http://server:8082"
          @keyup.enter="handleConnect"
        />
      </div>

      <div class="form-group">
        <label class="form-label">共享目录</label>
        <div class="dir-selector">
          <span class="dir-selector__path" :title="selectedDir">{{ selectedDir }}</span>
          <button class="dir-selector__btn" @click="handleSelectDir">选择目录</button>
        </div>
      </div>

      <div class="mt-auto">
        <button
          class="btn btn--primary btn--block"
          :disabled="!codeInput.trim()"
          @click="handleConnect"
        >
          连接
        </button>
      </div>
    </template>

    <template v-if="appState === 'connecting'">
      <div class="spinner-area">
        <div class="spinner" />
        <span class="spinner-area__text">正在连接...</span>
      </div>

      <div class="mt-auto">
        <button class="btn btn--ghost btn--block" @click="handleRetry">
          取消
        </button>
      </div>
    </template>

    <template v-if="appState === 'connected'">
      <div class="status-badge status-badge--connected">
        <span class="status-badge__dot" />
        <span>已连接</span>
      </div>

      <div class="info-card">
        <div class="info-card__row">
          <span class="info-card__label">共享目录</span>
          <span class="info-card__value" :title="tunnelInfo.sharedDir">{{ tunnelInfo.sharedDir }}</span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">连接码</span>
          <span class="info-card__value">{{ tunnelInfo.code }}</span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">服务器</span>
          <span class="info-card__value">{{ tunnelInfo.serverUrl }}</span>
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
