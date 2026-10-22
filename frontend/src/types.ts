// 前端类型定义(与 Go 绑定结构对应)

export interface EmuInstance {
  index: number
  name: string
  serial: string
  running: boolean
  android_started: boolean
  android_version: string
  pid: number
}

export interface InstallInfo {
  installed: boolean
  dir: string
  manager_path: string
  adb_path: string
}

export interface FlowEvent {
  type: 'flow' | 'request' | 'ws' | 'tls_fail'
  ts: string
  method: string
  url: string
  status: number
  duration_ms: number
  req_headers: Record<string, string>
  resp_headers: Record<string, string>
  req_body: string
  resp_body: string
  truncated: boolean
  direction: string
  content: string
  side: string
  sni: string
  error: string
}

export interface ProcConnection {
  local: string
  remote: string
  state: string
  pid: number
  proc: string
}
