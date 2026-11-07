<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import {
  DetectEnvironment, ListInstances, ConnectDevice, DisconnectDevice, EnsureRoot,
  GetForegroundApp, InstallCertificate, StartCapture, StopCapture,
  CaptureRunning, GetProcessConnections, OpenAdbShell,
  SaveResponseBody, CopyText, HexDump,
  PauseCapture, ResumeCapture, CapturePaused, ClearFlowBodies,
} from '../wailsjs/go/main/App'
import { EventsOn, WindowMinimise, WindowToggleMaximise, Quit } from '../wailsjs/runtime/runtime'
import type { EmuInstance, FlowEvent, ProcConnection } from './types'

// ---------- 状态 ----------
const mumuInstalled = ref(false)
const mumuDir = ref('')
const detected = ref(false)
const instances = ref<EmuInstance[]>([])
const serial = ref('')
const connected = ref(false)
const rooted = ref(false)
const foreground = ref('')
const targetPkg = ref('')
const capturing = ref(false)
const paused = ref(false)
const stepLogs = ref<string[]>([])
const flows = ref<FlowEvent[]>([])
const selectedFlow = ref<FlowEvent | null>(null)
const filterText = ref('')
const noiseFilter = ref(true)
const detailTab = ref<'resp' | 'hex' | 'req' | 'reqhead' | 'resphead'>('resp')
const connDiag = ref<ProcConnection[]>([])
const busy = ref('')
const tab = ref<'flows' | 'logs'>('logs')

function log(msg: string) {
  stepLogs.value.push(msg)
  if (stepLogs.value.length > 500) stepLogs.value.shift()
}

async function guard(name: string, fn: () => Promise<any>) {
  busy.value = name
  try {
    return await fn()
  } catch (e: any) {
    log(`${name} 失败: ${e}`)
    tab.value = 'logs' // 失败时切到运行日志, 让用户看到原因
  } finally {
    busy.value = ''
  }
}

// ---------- 实例状态派生 ----------
const runningInstances = computed(() => instances.value.filter(i => i.running))
const selInst = computed(() => instances.value.find(i => i.serial === serial.value))
const hasRunning = computed(() => runningInstances.value.length > 0)
const selRunning = computed(() => !!selInst.value?.running)
// 无运行中实例时的提示文案(空串 = 不显示提示框)
const instHint = computed(() => {
  if (!detected.value) return ''
  if (instances.value.length && !hasRunning.value) return '模拟器未开机，请先打开 MuMu 模拟器'
  if (mumuInstalled.value) return '未检测到 MuMu 实例'
  return '未检测到 MuMu 模拟器安装'
})

// ---------- 步骤动作 ----------
async function detect() {
  await guard('detect', async () => {
    const info: any = await DetectEnvironment()
    detected.value = true
    mumuInstalled.value = info.installed
    mumuDir.value = info.dir || ''
    if (!info.installed) { log('未检测到 MuMu 安装'); instances.value = []; return }
    instances.value = (await ListInstances()) || []
    const run = runningInstances.value
    if (!run.length) {
      serial.value = ''
      log('未检测到运行中的模拟器实例, 请先打开 MuMu')
    } else {
      if (!selRunning.value) serial.value = run[0].serial
      log(`检测完成: ${instances.value.length} 个实例, ${run.length} 个运行中`)
    }
  })
}

async function toggleConnect() {
  if (connected.value) {
    await guard('disconnect', async () => {
      await DisconnectDevice(serial.value)
      connected.value = false
      rooted.value = false
      connDiag.value = []
      log(`adb 已断开 ${serial.value}`)
    })
    return
  }
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

async function openShell() {
  await guard('shell', async () => {
    await OpenAdbShell(serial.value)
    log(`已打开 ADB Shell 窗口 (${serial.value}), 窗口内输入 exit 可退出`)
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
    tab.value = 'flows'
    await StartCapture(serial.value, targetPkg.value, 8888, false)
    capturing.value = true
    log('抓包已开启')
  })
}

async function stopCapture() {
  await guard('stop', async () => {
    await StopCapture()
    capturing.value = false
    paused.value = false
    log('抓包已停止, APP 已恢复直连')
  })
}

// 暂停/恢复记录: 暂停期间流量仍经代理转发(APP 不受影响), 只是不再新增请求行
async function togglePause() {
  await guard('pause', async () => {
    if (paused.value) {
      await ResumeCapture()
      paused.value = false
    } else {
      await PauseCapture()
      paused.value = true
    }
  })
}

// 清空 HTTP 请求日志: 同时删除落盘的完整响应体临时文件(否则文件会持续堆积)
async function clearFlows() {
  flows.value = []
  selectedFlow.value = null
  rowMenu.value = null
  copyMenuOpen.value = false
  resetHex()
  await guard('clear', async () => {
    await ClearFlowBodies()
    log('HTTP 请求日志已清空')
  })
}

function clearLogs() {
  stepLogs.value = []
}

async function diagConnections() {
  await guard('diag', async () => {
    connDiag.value = (await GetProcessConnections(serial.value, targetPkg.value)) || []
    log(`进程连接诊断: ${connDiag.value.length} 条`)
  })
}

// ---------- 事件 ----------
let evSeq = 0
onMounted(() => {
  EventsOn('env:log', (msg: string) => log(msg))
  EventsOn('capture:flow', (ev: FlowEvent) => {
    ev._id = ++evSeq
    if (ev.type === 'flow') {
      // 同 method+URL 的 pending 行(仅有请求无响应)原地替换为完成态: 一行 = 一个请求。
      // OPTIONS 预检与真实请求 method 不同, 各自成行, 不再把同一请求显示成两行
      const i = flows.value.findIndex(f => f.type === 'request' && f.method === ev.method && f.url === ev.url)
      if (i >= 0) {
        if (selectedFlow.value === flows.value[i]) selectedFlow.value = ev
        flows.value.splice(i, 1, ev)
      } else {
        flows.value.push(ev)
      }
    } else {
      flows.value.push(ev)
    }
    if (flows.value.length > 2000) flows.value.shift()
  })
  EventsOn('capture:exit', (reason: string) => {
    capturing.value = false
    paused.value = false
    log(`mitmdump 退出: ${reason}`)
  })
  CaptureRunning().then((r: boolean) => { capturing.value = r })
  CapturePaused().then((p: boolean) => { paused.value = p })
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { rowMenu.value = null; copyMenuOpen.value = false; lightboxUrl.value = '' }
  })
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
  resetHex()
}

// 重置 HEX 查看器状态(切换请求/清空列表后按需重新加载)
function resetHex() {
  hexText.value = ''
  hexLoaded.value = 0
  hexHasMore.value = false
  hexTotal.value = 0
  hexLoadedKey = ''
}

// ---------- 媒体预览 ----------
const lightboxUrl = ref('')

// 响应是否为可预览媒体(图片/视频/音频)
function mediaKind(f: FlowEvent | null): 'image' | 'video' | 'audio' | null {
  if (!f || f.type !== 'flow') return null
  const ct = f.resp_content_type || ''
  if (ct.startsWith('image/')) return 'image'
  if (ct.startsWith('video/')) return 'video'
  if (ct.startsWith('audio/')) return 'audio'
  return null
}

function mediaDataUrl(f: FlowEvent): string {
  return `data:${f.resp_content_type || 'application/octet-stream'};base64,${f.resp_body_b64}`
}

function fmtSize(n: number): string {
  if (n >= 1048576) return (n / 1048576).toFixed(1) + ' MB'
  if (n >= 1024) return (n / 1024).toFixed(1) + ' KB'
  return n + ' B'
}

// 媒体大小: 已内联按 base64 长度还原, 未内联从占位文本(<binary N bytes>)提取
function mediaSizeText(f: FlowEvent): string {
  if (f.resp_body_b64) return fmtSize(Math.floor(f.resp_body_b64.length * 3 / 4))
  const m = /<binary (\d+) bytes/.exec(f.resp_body || '')
  return m ? fmtSize(parseInt(m[1], 10)) : '未知大小'
}

// ---------- 另存为 / 复制为 ----------
const copyMenuOpen = ref(false)

const CT_EXT: Record<string, string> = {
  'image/jpeg': '.jpg', 'image/png': '.png', 'image/gif': '.gif', 'image/webp': '.webp',
  'image/svg+xml': '.svg', 'image/bmp': '.bmp', 'image/vnd.microsoft.icon': '.ico',
  'video/mp4': '.mp4', 'video/webm': '.webm', 'video/quicktime': '.mov',
  'audio/mpeg': '.mp3', 'audio/aac': '.aac', 'audio/ogg': '.ogg', 'audio/wav': '.wav',
  'application/json': '.json', 'text/html': '.html', 'text/plain': '.txt',
  'text/css': '.css', 'text/xml': '.xml', 'application/xml': '.xml',
  'application/javascript': '.js', 'application/pdf': '.pdf', 'application/octet-stream': '.bin',
}

function extOf(ct: string): string {
  return CT_EXT[(ct || '').split(';')[0].trim().toLowerCase()] ?? ''
}

// 另存默认文件名: URL 路径末段 + 按 content-type 推断的扩展名
function suggestedName(f: FlowEvent): string {
  let name = ''
  try {
    name = decodeURIComponent(new URL(f.url).pathname.split('/').pop() || '')
  } catch {
    name = ''
  }
  name = name.split('?')[0].replace(/[\\/:*?"<>|]/g, '_').slice(0, 80)
  if (!name) name = 'response'
  const ext = extOf(f.resp_content_type)
  if (ext && !name.toLowerCase().endsWith(ext)) name += ext
  return name
}

async function saveResponse() {
  const f = selectedFlow.value
  if (!f?.resp_body_file) return
  await guard('save', async () => {
    const p: string = await SaveResponseBody(f.resp_body_file, suggestedName(f))
    if (p) log(`响应体已另存: ${p}`)
  })
}

// 命令生成: 跳过 HTTP/2 伪头(:开头); body 为二进制占位/解码失败时不可复现, 省略
function cmdHeaders(f: FlowEvent): [string, string][] {
  return Object.entries(f.req_headers || {}).filter(([k]) => k && !k.startsWith(':'))
}

function usableBody(f: FlowEvent): string {
  const b = f.req_body || ''
  if (!b || b.startsWith('<binary ') || b.startsWith('<body decode error')) return ''
  return b
}

function shQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`
}

function psQuote(s: string): string {
  return `'${s.replace(/'/g, `''`)}'`
}

function toCurl(f: FlowEvent): string {
  const lines: string[] = [`curl ${shQuote(f.url)}`]
  if (f.method && f.method !== 'GET') lines.push(`-X ${f.method}`)
  for (const [k, v] of cmdHeaders(f)) lines.push(`-H ${shQuote(`${k}: ${v}`)}`)
  const b = usableBody(f)
  if (b) lines.push(`--data-raw ${shQuote(b)}`)
  return lines.join(' \\\n  ')
}

function toPowerShell(f: FlowEvent): string {
  const hdrs = cmdHeaders(f)
  const ct = hdrs.find(([k]) => k.toLowerCase() === 'content-type')
  const rest = hdrs.filter(([k]) => k.toLowerCase() !== 'content-type')
  const out: string[] = []
  if (rest.length) {
    out.push(`$headers = @{ ${rest.map(([k, v]) => `${psQuote(k)}=${psQuote(v)}`).join('; ')} }`)
  }
  const seg = [`Invoke-WebRequest -Uri ${psQuote(f.url)}`]
  if (f.method && f.method !== 'GET') seg.push(`-Method ${f.method}`)
  if (rest.length) seg.push('-Headers $headers')
  if (ct) seg.push(`-ContentType ${psQuote(ct[1])}`)
  const b = usableBody(f)
  if (b) seg.push(`-Body ${psQuote(b)}`)
  out.push(seg.join(' '))
  return out.join('\n')
}

async function copyAs(kind: 'curl' | 'ps') {
  copyMenuOpen.value = false
  const f = selectedFlow.value
  if (!f) return
  await guard('copy', async () => {
    await CopyText(kind === 'curl' ? toCurl(f) : toPowerShell(f))
    log(`已复制为 ${kind === 'curl' ? 'cURL' : 'PowerShell'}: ${f.method} ${f.url.slice(0, 100)}`)
  })
}

// ---------- HEX 查看器(响应体十六进制, 64KB 分页) ----------
const HEX_CHUNK = 65536
const hexText = ref('')
const hexLoading = ref(false)
const hexLoaded = ref(0)
const hexHasMore = ref(false)
const hexTotal = ref(0)
let hexLoadedKey = ''

watch(detailTab, (t) => {
  if (t === 'hex') void ensureHex(false)
})

async function ensureHex(more = false) {
  const f = selectedFlow.value
  if (!f?.resp_body_file || hexLoading.value) return
  const off = more ? hexLoaded.value : 0
  if (!more && hexLoadedKey === `${f._id}@0`) return
  hexLoading.value = true
  try {
    const r = await HexDump(f.resp_body_file, off, HEX_CHUNK)
    if (selectedFlow.value !== f) return // 加载期间已切换选中项
    hexText.value = more ? hexText.value + r.text : r.text
    hexLoaded.value = r.next_offset
    hexHasMore.value = r.has_more
    hexTotal.value = r.total
    if (!more) hexLoadedKey = `${f._id}@0`
  } catch (e: any) {
    log(`HEX 查看失败: ${e}`)
  } finally {
    hexLoading.value = false
  }
}

// 头部展示: name: value 行式(比 JSON.stringify 可读)
function headersText(h: Record<string, string> | undefined): string {
  if (!h || !Object.keys(h).length) return ''
  return Object.entries(h).map(([k, v]) => `${k}: ${v}`).join('\n')
}

function fmtHeaders(h: Record<string, string> | undefined): string {
  return headersText(h) || '(空)'
}

// ---------- 请求行右键菜单: 复制 URL / cURL / 请求头 / 响应头 / 响应体 ----------
const rowMenu = ref<{ x: number; y: number; flow: FlowEvent } | null>(null)

function openRowMenu(e: MouseEvent, f: FlowEvent) {
  showDetail(f)
  const W = 210, H = 250
  rowMenu.value = {
    x: Math.min(e.clientX, window.innerWidth - W),
    y: Math.min(e.clientY, window.innerHeight - H),
    flow: f,
  }
}

// 响应体可复制文本: 文本 body 直接用; 二进制(图片/音视频)用内联 base64 data URL 兜底
function bodyForCopy(f: FlowEvent): string {
  const b = f.resp_body || ''
  if (b && !b.startsWith('<binary ') && !b.startsWith('<body decode error')) {
    return f.truncated ? `${b}\n...[已截断]` : b
  }
  if (f.resp_body_b64) {
    return `data:${f.resp_content_type || 'application/octet-stream'};base64,${f.resp_body_b64}`
  }
  return b
}

async function copyFromMenu(kind: 'url' | 'curl' | 'reqhead' | 'resphead' | 'respbody') {
  const f = rowMenu.value?.flow
  rowMenu.value = null
  if (!f) return
  let text = ''
  let label = ''
  switch (kind) {
    case 'url': text = f.url || ''; label = 'URL'; break
    case 'curl': text = toCurl(f); label = 'cURL'; break
    case 'reqhead': text = headersText(f.req_headers); label = '请求头'; break
    case 'resphead': text = headersText(f.resp_headers); label = '响应头'; break
    case 'respbody': text = bodyForCopy(f); label = '响应体'; break
  }
  if (!text) {
    log(`无可复制的${label || '内容'} (${f.method || '-'} ${f.url || ''})`)
    return
  }
  await guard('copy', async () => {
    await CopyText(text)
    log(`已复制${label}: ${f.method || ''} ${(f.url || '').slice(0, 100)}`)
  })
}
</script>

<template>
  <div class="app">
    <!-- 自定义标题栏(可拖拽) -->
    <header class="titlebar">
      <div class="tb-left">
        <svg class="tb-logo" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="2" y="4" width="20" height="14" rx="1"/><path d="M8 21h8M12 18v3M7 9l3 3-3 3M12 15h5"/>
        </svg>
        <span class="tb-title">MUMU // CAPTURE</span>
        <span class="tb-sub">模拟器 HTTP 抓包终端</span>
      </div>
      <div class="tb-btns">
        <button class="tb-btn" title="最小化" @click="WindowMinimise()">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M5 12h14"/></svg>
        </button>
        <button class="tb-btn" title="最大化/还原" @click="WindowToggleMaximise()">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="5" y="5" width="14" height="14"/></svg>
        </button>
        <button class="tb-btn tb-close" title="退出" @click="Quit()">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>
      </div>
    </header>

    <div class="main">
      <!-- 左列: 步骤操作 -->
      <aside class="panel steps">
        <div class="panel-tag">PROC // 操作序列</div>

        <div class="step">
          <div class="step-head"><i>01</i>环境检测</div>
          <button :disabled="busy !== ''" @click="detect">
            {{ busy === 'detect' ? '检测中...' : '检测 MuMu / 实例' }}
          </button>
          <div v-if="mumuDir" class="hint">{{ mumuDir }}</div>

          <!-- 仅存在运行中实例时才显示选框, 否则只提示 -->
          <select v-if="hasRunning" v-model="serial" class="input">
            <option v-for="i in instances" :key="i.serial" :value="i.serial" :disabled="!i.running">
              {{ i.name }} #{{ i.index }} [{{ i.serial }}] {{ i.running ? 'ONLINE' : 'OFFLINE' }}
            </option>
          </select>
          <div v-else-if="instHint" class="warn-box">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 3l10 18H2L12 3z"/><path d="M12 10v5M12 18h.01"/>
            </svg>
            {{ instHint }}
          </div>
        </div>

        <div class="step">
          <div class="step-head"><i>02</i>连接与 Root</div>
          <div class="row">
            <button :disabled="!serial || !selRunning || busy !== ''" @click="toggleConnect">
              {{ connected ? '断开 ADB' : '连接 ADB' }}
            </button>
            <button :disabled="!connected || rooted || busy !== ''" @click="ensureRoot">获取 Root</button>
          </div>
          <div class="row">
            <button :disabled="!connected || busy !== ''" @click="openShell">打开 ADB Shell</button>
          </div>
        </div>

        <div class="step">
          <div class="step-head"><i>03</i>目标应用</div>
          <div class="row">
            <button :disabled="!rooted || busy !== ''" @click="detectForeground">取前台应用</button>
            <button :disabled="!rooted || !targetPkg || busy !== ''" @click="diagConnections">连接诊断</button>
          </div>
          <input v-model="targetPkg" class="input" placeholder="包名" spellcheck="false" />
          <div v-if="foreground" class="hint">{{ foreground }}</div>
          <ul v-if="connDiag.length" class="conn-list">
            <li v-for="(c, i) in connDiag" :key="i">
              {{ c.remote }} <span class="dim">{{ c.state }}</span>
            </li>
          </ul>
        </div>

        <div class="step">
          <div class="step-head"><i>04</i>抓包</div>
          <div class="row">
            <button class="primary" :disabled="!rooted || !targetPkg || capturing || busy !== ''" @click="startCapture">
              开启抓包
            </button>
            <button class="danger" :disabled="!capturing || busy !== ''" @click="stopCapture">停止</button>
          </div>
          <button class="ghost" :disabled="!capturing || busy !== ''" :class="{ 'pause-on': paused }" @click="togglePause">
            {{ paused ? '恢复抓包' : '暂停抓包' }}
          </button>
          <button class="ghost" :disabled="!rooted || busy !== ''" @click="installCert">仅重装证书(幂等)</button>
          <p class="note">抓取 HTTP 请求/响应。IM 私有协议长连接不走代理, 不在抓包范围。暂停期间流量仍正常转发。</p>
        </div>
      </aside>

      <!-- 右列: TAB(请求 / 日志) -->
      <section class="panel work">
        <div class="work-tabs">
          <button :class="{ act: tab === 'flows' }" @click="tab = 'flows'">
            HTTP 请求 <em>{{ visibleFlows.length }}</em>
          </button>
          <button :class="{ act: tab === 'logs' }" @click="tab = 'logs'">
            运行日志 <em>{{ stepLogs.length }}</em>
          </button>
          <div class="tab-status" :class="paused ? 'paused' : capturing ? 'live' : ''">
            {{ paused ? 'PAUSED — 记录已暂停' : capturing ? '● CAPTURE ACTIVE' : '○ IDLE' }}
          </div>
        </div>

        <!-- TAB: 请求 -->
        <div v-show="tab === 'flows'" class="tab-pane tab-flows">
          <div class="flows-toolbar">
            <input v-model="filterText" class="input" placeholder="URL 过滤..." spellcheck="false" />
            <label class="chk"><input type="checkbox" v-model="noiseFilter" /> 屏蔽统计噪声</label>
            <button class="tool-btn" :disabled="!flows.length || busy !== ''"
                    title="清空请求列表, 同时删除落盘的响应体临时文件" @click="clearFlows">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14M10 11v5M14 11v5"/>
              </svg>
              清空请求
            </button>
          </div>
          <div class="flow-table">
            <table>
              <thead>
                <tr><th>时间</th><th>方法</th><th>URL</th><th>状态</th><th>耗时</th></tr>
              </thead>
              <tbody>
                <tr v-if="!visibleFlows.length">
                  <td colspan="5" class="empty">NO DATA — 完成左侧操作序列并在模拟器中操作应用</td>
                </tr>
                <tr v-for="f in visibleFlows" :key="f._id"
                    :class="{ sel: selectedFlow === f }" @click="showDetail(f)"
                    @contextmenu.prevent="openRowMenu($event, f)">
                  <td class="mono">{{ f.ts }}</td>
                  <td><span class="method" :class="f.type === 'tls_fail' ? 'm-tls' : 'm-' + f.method.toLowerCase()">{{ f.type === 'tls_fail' ? 'TLS!' : f.method }}</span></td>
                  <td class="url mono">{{ f.type === 'tls_fail' ? `TLS握手失败: ${f.sni}` : f.url }}</td>
                  <td><span class="status" :class="statusClass(f.status)">{{ f.status || '-' }}</span></td>
                  <td class="mono">{{ f.duration_ms ? f.duration_ms + 'ms' : '' }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <!-- 复制为下拉的点击外部关闭层 -->
          <div v-if="copyMenuOpen && selectedFlow" class="menu-backdrop" @click="copyMenuOpen = false"></div>

          <div v-if="selectedFlow" class="detail">
            <div class="detail-tabs">
              <button :class="{ act: detailTab === 'resp' }" @click="detailTab = 'resp'">响应体</button>
              <button :class="{ act: detailTab === 'hex' }" :disabled="!selectedFlow.resp_body_file"
                      title="响应体十六进制(HEX)查看" @click="detailTab = 'hex'">HEX</button>
              <button :class="{ act: detailTab === 'req' }" @click="detailTab = 'req'">请求体</button>
              <button :class="{ act: detailTab === 'reqhead' }" @click="detailTab = 'reqhead'">请求头</button>
              <button :class="{ act: detailTab === 'resphead' }" @click="detailTab = 'resphead'">响应头</button>
              <div class="dt-actions">
                <button class="dt-action" :disabled="!selectedFlow.resp_body_file"
                        :title="selectedFlow.resp_body_file ? '将完整响应体另存到磁盘' : '无完整响应体(pending/空 body/超过 256MB 未落盘)'"
                        @click="saveResponse">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12"/><path d="m7 10 5 5 5-5"/><path d="M4 21h16"/></svg>
                  另存为
                </button>
                <div class="copy-wrap">
                  <button class="dt-action" :disabled="!selectedFlow.url" title="复制请求为命令行" @click.stop="copyMenuOpen = !copyMenuOpen">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="12" height="12" rx="1"/><path d="M5 15V5a1 1 0 0 1 1-1h10"/></svg>
                    复制为
                    <svg class="chev" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>
                  </button>
                  <div v-if="copyMenuOpen" class="copy-menu">
                    <button @click="copyAs('curl')">cURL (bash)</button>
                    <button @click="copyAs('ps')">PowerShell</button>
                  </div>
                </div>
                <button class="close" @click="selectedFlow = null; copyMenuOpen = false">关闭</button>
              </div>
            </div>
            <!-- 媒体响应: 图片直接预览(点击看原图), 视频/音频点击播放 -->
            <div v-if="detailTab === 'resp' && mediaKind(selectedFlow)" class="media-view">
              <div class="media-meta">
                <span class="mono">{{ selectedFlow.resp_content_type }}</span> · {{ mediaSizeText(selectedFlow) }}
                <template v-if="!selectedFlow.resp_body_b64"> · 超过 8MB 未内联, 无预览</template>
                <template v-else-if="mediaKind(selectedFlow) === 'image'"> · 点击图片查看原图</template>
                <template v-else> · 点击播放</template>
              </div>
              <img v-if="selectedFlow.resp_body_b64 && mediaKind(selectedFlow) === 'image'"
                   class="media-img" :src="mediaDataUrl(selectedFlow)"
                   @click="lightboxUrl = mediaDataUrl(selectedFlow)" />
              <video v-else-if="selectedFlow.resp_body_b64 && mediaKind(selectedFlow) === 'video'"
                     class="media-video" :src="mediaDataUrl(selectedFlow)"
                     controls preload="metadata"></video>
              <audio v-else-if="selectedFlow.resp_body_b64 && mediaKind(selectedFlow) === 'audio'"
                     :src="mediaDataUrl(selectedFlow)" controls></audio>
              <pre v-else class="body">{{ selectedFlow.resp_body || '(空)' }}{{ selectedFlow.truncated ? '\n...[已截断]' : '' }}</pre>
            </div>
            <pre v-else-if="detailTab === 'resp'" class="body">{{ selectedFlow.resp_body || '(空)' }}{{ selectedFlow.truncated ? '\n...[已截断]' : '' }}</pre>
            <pre v-else-if="detailTab === 'req'" class="body">{{ selectedFlow.req_body || '(空)' }}{{ selectedFlow.req_truncated ? '\n...[已截断]' : '' }}</pre>
            <pre v-else-if="detailTab === 'reqhead'" class="body">{{ fmtHeaders(selectedFlow.req_headers) }}</pre>
            <pre v-else-if="detailTab === 'resphead'" class="body">{{ fmtHeaders(selectedFlow.resp_headers) }}</pre>
            <div v-else-if="detailTab === 'hex'" class="hex-pane">
              <div class="hex-meta">
                {{ selectedFlow.resp_content_type || 'application/octet-stream' }} · 共 {{ fmtSize(hexTotal || selectedFlow.resp_body_size) }}
                <template v-if="hexLoaded"> · 已加载 {{ fmtSize(hexLoaded) }}</template>
              </div>
              <pre class="body hexbody">{{ hexText || (hexLoading ? '加载中...' : '(空)') }}</pre>
              <button v-if="hexHasMore" class="hex-more" :disabled="hexLoading" @click="ensureHex(true)">
                {{ hexLoading ? '加载中...' : '加载更多' }}
              </button>
            </div>
          </div>
        </div>

        <!-- TAB: 日志 -->
        <div v-show="tab === 'logs'" class="tab-pane tab-logs">
          <div class="logs-toolbar">
            <button class="tool-btn" :disabled="!stepLogs.length" title="清空运行日志" @click="clearLogs">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14M10 11v5M14 11v5"/>
              </svg>
              清空日志
            </button>
          </div>
          <div class="logs-full">
            <div v-for="(l, i) in stepLogs" :key="i" class="logline">{{ l }}</div>
            <div v-if="!stepLogs.length" class="empty">NO LOG</div>
          </div>
        </div>
      </section>
    </div>

    <!-- 底部状态条 -->
    <footer class="statusbar">
      <span>SERIAL: <b>{{ serial || '---' }}</b></span>
      <span>PORT: <b>8888</b></span>
      <span>PKG: <b>{{ targetPkg || '---' }}</b></span>
      <span class="right" :class="paused ? 'paused' : capturing ? 'live' : ''">{{ paused ? 'PAUSED' : capturing ? 'CAPTURE ACTIVE' : 'STANDBY' }}</span>
    </footer>

    <!-- 请求行右键菜单 -->
    <div v-if="rowMenu" class="menu-backdrop" @click="rowMenu = null" @contextmenu.prevent="rowMenu = null"></div>
    <div v-if="rowMenu" class="ctx-menu" :style="{ left: rowMenu.x + 'px', top: rowMenu.y + 'px' }" @contextmenu.prevent>
      <div class="ctx-title">
        <b>{{ rowMenu.flow.type === 'tls_fail' ? 'TLS!' : rowMenu.flow.method || '-' }}</b>
        <span>{{ rowMenu.flow.status || 'pending' }}</span>
      </div>
      <button :disabled="!rowMenu.flow.url" @click="copyFromMenu('url')">复制 URL</button>
      <button :disabled="!rowMenu.flow.url" @click="copyFromMenu('curl')">复制为 cURL</button>
      <div class="ctx-sep"></div>
      <button :disabled="!rowMenu.flow.req_headers" @click="copyFromMenu('reqhead')">复制请求头</button>
      <button :disabled="!rowMenu.flow.resp_headers" @click="copyFromMenu('resphead')">复制响应头</button>
      <button :disabled="!rowMenu.flow.resp_body && !rowMenu.flow.resp_body_b64" @click="copyFromMenu('respbody')">复制响应体</button>
    </div>

    <!-- 图片灯箱(点击任意处关闭) -->
    <div v-if="lightboxUrl" class="lightbox" @click="lightboxUrl = ''">
      <img :src="lightboxUrl" alt="原图预览" />
      <div class="lightbox-tip">点击任意处关闭</div>
    </div>
  </div>
</template>
