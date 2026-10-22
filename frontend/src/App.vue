<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  DetectEnvironment, ListInstances, ConnectDevice, EnsureRoot,
  GetForegroundApp, InstallCertificate, StartCapture, StopCapture,
  CaptureRunning, GetProcessConnections,
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'
import type { EmuInstance, FlowEvent, ProcConnection } from './types'

// ---------- 状态 ----------
const mumuInstalled = ref(false)
const mumuDir = ref('')
const instances = ref<EmuInstance[]>([])
const serial = ref('')
const connected = ref(false)
const rooted = ref(false)
const foreground = ref('')
const targetPkg = ref('')
const capturing = ref(false)
const stepLogs = ref<string[]>([])
const flows = ref<FlowEvent[]>([])
const selectedFlow = ref<FlowEvent | null>(null)
const filterText = ref('')
const noiseFilter = ref(true)
const detailTab = ref<'resp' | 'req' | 'headers'>('resp')
const connDiag = ref<ProcConnection[]>([])
const busy = ref('')

function log(msg: string) {
  stepLogs.value.push(msg)
  if (stepLogs.value.length > 200) stepLogs.value.shift()
}

async function guard(name: string, fn: () => Promise<any>) {
  busy.value = name
  try { return await fn() } finally { busy.value = '' }
}

// ---------- 步骤动作 ----------
async function detect() {
  await guard('detect', async () => {
    const info: any = await DetectEnvironment()
    mumuInstalled.value = info.installed
    mumuDir.value = info.dir || ''
    if (!info.installed) { log('未检测到 MuMu 安装'); return }
    instances.value = (await ListInstances()) || []
    log(`检测完成: ${instances.value.length} 个实例`)
    const running = instances.value.find(i => i.running)
    if (running) serial.value = running.serial
  })
}

async function connect() {
  await guard('connect', async () => {
    await ConnectDevice(serial.value)
    connected.value = true
    log(`adb 已连接 ${serial.value}`)
  })
}

async function ensureRoot() {
  await guard('root', async () => {
    await EnsureRoot(serial.value)
    rooted.value = true
    log('root 权限 OK')
  })
}

async function detectForeground() {
  await guard('fg', async () => {
    const app: any = await GetForegroundApp(serial.value)
    foreground.value = `${app.package} / ${app.activity}`
    targetPkg.value = app.package
    log(`前台应用: ${foreground.value}`)
  })
}

async function installCert() {
  await guard('cert', async () => {
    log('安装证书(幂等)...')
    await InstallCertificate(serial.value, targetPkg.value)
    log('证书安装完成')
  })
}

async function startCapture() {
  await guard('start', async () => {
    flows.value = []
    await StartCapture(serial.value, targetPkg.value, 8888, false)
    capturing.value = true
    log('抓包已开启')
  })
}

async function stopCapture() {
  await guard('stop', async () => {
    await StopCapture()
    capturing.value = false
    log('抓包已停止, APP 已恢复直连')
  })
}

async function diagConnections() {
  await guard('diag', async () => {
    connDiag.value = (await GetProcessConnections(serial.value, targetPkg.value)) || []
    log(`进程连接诊断: ${connDiag.value.length} 条`)
  })
}

// ---------- 事件 ----------
onMounted(() => {
  EventsOn('env:log', (msg: string) => log(msg))
  EventsOn('capture:flow', (ev: FlowEvent) => {
    flows.value.push(ev)
    if (flows.value.length > 2000) flows.value.shift()
  })
  EventsOn('capture:exit', (reason: string) => {
    capturing.value = false
    log(`mitmdump 退出: ${reason}`)
  })
  CaptureRunning().then((r: boolean) => { capturing.value = r })
})

// ---------- 派生 ----------
const visibleFlows = computed(() =>
  flows.value.filter(f => {
    if (f.type === 'tls_fail') return true
    if (noiseFilter.value && /tongji|dcloud\.net/.test(f.url)) return false
    if (filterText.value && !f.url.toLowerCase().includes(filterText.value.toLowerCase())) return false
    return true
  }).reverse()
)

function statusClass(s: number): string {
  if (s >= 500) return 'st-5xx'
  if (s >= 400) return 'st-4xx'
  if (s >= 200 && s < 400) return 'st-ok'
  return 'st-pending'
}

function showDetail(f: FlowEvent) {
  selectedFlow.value = f
  detailTab.value = 'resp'
}
</script>

<template>
  <div class="app">
    <!-- 顶栏 -->
    <header class="topbar">
      <svg class="logo" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="2" y="4" width="20" height="14" rx="2"/><path d="M8 21h8M12 18v3M7 9l3 3-3 3M12 15h5"/>
      </svg>
      <h1>MuMu 抓包调试工具</h1>
      <div class="badges">
        <span class="badge" :class="mumuInstalled ? 'on' : 'off'">MuMu</span>
        <span class="badge" :class="connected ? 'on' : 'off'">ADB</span>
        <span class="badge" :class="rooted ? 'on' : 'off'">Root</span>
        <span class="badge" :class="capturing ? 'on' : 'off'">Capture</span>
      </div>
    </header>

    <div class="main">
      <!-- 左列: 步骤操作 -->
      <aside class="panel steps">
        <h2>步骤</h2>

        <div class="step">
          <div class="step-head">1. 环境检测</div>
          <button :disabled="busy !== ''" @click="detect">
            {{ busy === 'detect' ? '检测中...' : '检测 MuMu / 实例' }}
          </button>
          <div v-if="mumuDir" class="hint">{{ mumuDir }}</div>
          <select v-if="instances.length" v-model="serial" class="input">
            <option v-for="i in instances" :key="i.serial" :value="i.serial">
              {{ i.name }} #{{ i.index }} [{{ i.serial }}] {{ i.running ? '●' : '○' }}
            </option>
          </select>
        </div>

        <div class="step">
          <div class="step-head">2. 连接与 Root</div>
          <div class="row">
            <button :disabled="!serial || busy !== ''" @click="connect">连接 ADB</button>
            <button :disabled="!connected || rooted || busy !== ''" @click="ensureRoot">获取 Root</button>
          </div>
        </div>

        <div class="step">
          <div class="step-head">3. 目标应用</div>
          <div class="row">
            <button :disabled="!rooted || busy !== ''" @click="detectForeground">取前台应用</button>
            <button :disabled="!rooted || !targetPkg || busy !== ''" @click="diagConnections">连接诊断</button>
          </div>
          <input v-model="targetPkg" class="input" placeholder="包名, 如 com.gtex.timp" />
          <div v-if="foreground" class="hint">{{ foreground }}</div>
          <ul v-if="connDiag.length" class="conn-list">
            <li v-for="(c, i) in connDiag" :key="i">
              {{ c.remote }} <span class="dim">{{ c.state }}</span>
            </li>
          </ul>
        </div>

        <div class="step">
          <div class="step-head">4. 抓包</div>
          <div class="row">
            <button class="primary" :disabled="!rooted || !targetPkg || capturing || busy !== ''" @click="startCapture">
              开启抓包
            </button>
            <button class="danger" :disabled="!capturing || busy !== ''" @click="stopCapture">停止</button>
          </div>
          <button class="ghost" :disabled="!rooted || busy !== ''" @click="installCert">仅重装证书(幂等)</button>
          <p class="note">抓取 HTTP 请求/响应; IM 私有协议长连接不走代理, 不在抓包范围。</p>
        </div>

        <h2>日志</h2>
        <div class="stepl_logs">
          <div v-for="(l, i) in stepLogs" :key="i" class="logline">{{ l }}</div>
        </div>
      </aside>

      <!-- 右列: 请求列表 + 详情 -->
      <section class="panel flows">
        <div class="flows-toolbar">
          <h2>HTTP 请求 ({{ visibleFlows.length }})</h2>
          <input v-model="filterText" class="input" placeholder="URL 过滤..." />
          <label class="chk"><input type="checkbox" v-model="noiseFilter" /> 屏蔽统计噪声</label>
        </div>
        <div class="flow-table">
          <table>
            <thead>
              <tr><th>时间</th><th>方法</th><th>URL</th><th>状态</th><th>耗时</th></tr>
            </thead>
            <tbody>
              <tr v-if="!visibleFlows.length">
                <td colspan="5" class="empty">暂无数据 — 完成左侧步骤并在模拟器中操作应用</td>
              </tr>
              <tr v-for="(f, i) in visibleFlows" :key="visibleFlows.length - i"
                  :class="{ sel: selectedFlow === f }" @click="showDetail(f)">
                <td class="mono">{{ f.ts }}</td>
                <td><span class="method">{{ f.type === 'tls_fail' ? 'TLS!' : f.method }}</span></td>
                <td class="url mono">{{ f.type === 'tls_fail' ? `TLS握手失败: ${f.sni}` : f.url }}</td>
                <td><span class="status" :class="statusClass(f.status)">{{ f.status || '-' }}</span></td>
                <td class="mono">{{ f.duration_ms ? f.duration_ms + 'ms' : '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 详情 -->
        <div v-if="selectedFlow" class="detail">
          <div class="detail-tabs">
            <button :class="{ act: detailTab === 'resp' }" @click="detailTab = 'resp'">响应体</button>
            <button :class="{ act: detailTab === 'req' }" @click="detailTab = 'req'">请求体</button>
            <button :class="{ act: detailTab === 'headers' }" @click="detailTab = 'headers'">请求头</button>
            <button class="close" @click="selectedFlow = null">关闭</button>
          </div>
          <pre v-if="detailTab === 'resp'" class="body">{{ selectedFlow.resp_body || '(空)' }}{{ selectedFlow.truncated ? '\n...[已截断]' : '' }}</pre>
          <pre v-else-if="detailTab === 'req'" class="body">{{ selectedFlow.req_body || '(空)' }}</pre>
          <pre v-else class="body">{{ JSON.stringify(selectedFlow.req_headers, null, 2) }}</pre>
        </div>
      </section>
    </div>
  </div>
</template>
